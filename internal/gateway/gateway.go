package gateway

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	netproto "gameserver/internal/net"
	"gameserver/internal/rpc"
	"google.golang.org/protobuf/proto"
	"io"
	"strconv"
	"sync"
	"time"
)

// 常量配置
const (
	heartbeatTimeoutMs = 90 * 1000 // 90s 无数据踢线
	routeTTLSec        = 120       // 玩家路由表 Redis TTL
	refreshPeersMs     = 5000      // 服务发现刷新周期
	scanTimeoutMs      = 1000      // 超时扫描周期
	matchPollInterval  = 500 * time.Millisecond
	matchPollTimes     = 22 // 匹配轮询上限（≈11s）
)

// Session 玩家会话（一个在线玩家对应一条 TCP 连接）
type Session struct {
	Conn       io.ReadWriteCloser
	PlayerID   int64
	PlayerName string
	Token      string
	Player     *pb.PlayerBase
	RoomID     int64 // 0 = 不在房间
	MatchMode  int32 // -1 = 不在匹配；0=帧同步 1=状态同步
	LastPingMs int64
	writeMu    sync.Mutex // 串行化向该连接写帧
}

// Gateway 网关服务器
// 职责：客户端唯一入口，管理连接/会话，把登录/匹配/操作/排行请求路由到后端微服务，
// 并把后端推送（快照/帧/结果/踢线）转发给对应客户端连接。
type Gateway struct {
	name     string
	host     string // TCP 监听地址（0.0.0.0 时对外通告用 127.0.0.1）
	tcpPort  int
	grpcPort int

	redis *rpc.RedisClient
	cc    *rpc.ClusterClient

	sessions  map[int64]*Session
	sessionMu sync.RWMutex

	stop chan struct{}
}

// NewGateway 创建网关
func NewGateway(name, host string, tcpPort, grpcPort int) *Gateway {
	return &Gateway{
		name:     name,
		host:     host,
		tcpPort:  tcpPort,
		grpcPort: grpcPort,
		sessions: make(map[int64]*Session),
		stop:     make(chan struct{}),
	}
}

// Init 初始化网关（Redis + 集群客户端地址）
func (gw *Gateway) Init(redisHost string, redisPort int, centerHost string, centerPort int, loginHost string, loginPort int) {
	gw.redis = rpc.NewRedisClient(redisHost, redisPort)
	gw.cc = rpc.NewClusterClient()
	gw.cc.SetCenterAddr(rpc.ServiceAddr{Host: centerHost, Port: int32(centerPort)})
	gw.cc.SetLoginAddr(rpc.ServiceAddr{Host: loginHost, Port: int32(loginPort)})
}

// AdvertiseAddr 对外通告的 gRPC 地址（game 据此向本网关推送数据）
func (gw *Gateway) AdvertiseAddr() string {
	h := gw.host
	if h == "" || h == "0.0.0.0" {
		h = "127.0.0.1"
	}
	return common.FormatAddr(h, gw.grpcPort)
}

// Start 启动后台任务：向 center 注册自己并心跳 + 刷新服务发现 + 心跳超时扫描
func (gw *Gateway) Start(ctx context.Context) {
	go gw.registerAndHeartbeatLoop(ctx)
	go gw.refreshPeersLoop(ctx)
	go gw.scanTimeoutLoop()
}

// registerAndHeartbeatLoop 注册到中心服（失败重试，最多 ~5s），随后 5s 心跳
func (gw *Gateway) registerAndHeartbeatLoop(ctx context.Context) {
	registered := false
	for i := 0; i < 10; i++ {
		if err := gw.cc.RegisterService(ctx, gw.name, gw.host, gw.grpcPort, "gateway"); err == nil {
			registered = true
			break
		}
		common.Warn("[gateway] 中心服注册失败（第 %d 次），500ms 后重试", i+1)
		select {
		case <-time.After(500 * time.Millisecond):
		case <-gw.stop:
			return
		}
	}
	if !registered {
		common.Error("[gateway] 中心服注册失败（重试 10 次仍失败）")
	}

	ticker := time.NewTicker(refreshPeersMs * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := gw.cc.Heartbeat(ctx, gw.name); err != nil {
				common.Warn("[gateway] 中心服心跳失败，尝试重新注册")
				_ = gw.cc.RegisterService(ctx, gw.name, gw.host, gw.grpcPort, "gateway")
			}
		case <-gw.stop:
			return
		}
	}
}

// refreshPeersLoop 周期刷新 game/rank 服务列表
func (gw *Gateway) refreshPeersLoop(ctx context.Context) {
	ticker := time.NewTicker(refreshPeersMs * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			gw.refreshPeers(ctx)
		case <-gw.stop:
			return
		}
	}
}

func (gw *Gateway) refreshPeers(ctx context.Context) {
	services, err := gw.cc.GetServiceList(ctx)
	if err != nil {
		return
	}
	var games []rpc.ServiceAddr
	for _, e := range services {
		switch e.Kind {
		case "game":
			games = append(games, rpc.ServiceAddr{Host: e.Host, Port: e.Port})
		case "rank":
			gw.cc.SetRankAddr(rpc.ServiceAddr{Host: e.Host, Port: e.Port})
		}
	}
	if len(games) > 0 {
		gw.cc.SetGameList(games)
	}
}

// scanTimeoutLoop 心跳超时扫描，超时连接踢下线
func (gw *Gateway) scanTimeoutLoop() {
	ticker := time.NewTicker(scanTimeoutMs * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := common.NowMs()
			for _, s := range gw.snapshotSessions() {
				if now-s.LastPingMs > heartbeatTimeoutMs {
					gw.SendKick(s, "心跳超时")
					s.Conn.Close()
				}
			}
		case <-gw.stop:
			return
		}
	}
}

// Close 停止后台任务
func (gw *Gateway) Close() {
	select {
	case <-gw.stop:
	default:
		close(gw.stop)
	}
}

// ---------------- 会话管理 ----------------

// AddSession 绑定玩家会话（若该玩家已在线则顶号）
func (gw *Gateway) BindSession(session *Session) {
	gw.sessionMu.Lock()
	old := gw.sessions[session.PlayerID]
	gw.sessions[session.PlayerID] = session
	gw.sessionMu.Unlock()

	if old != nil && old != session {
		common.Info("[gateway] player %d 顶号，踢掉旧连接", session.PlayerID)
		gw.SendKick(old, "账号在其他设备登录，你已被挤下线")
		old.Conn.Close()
	}
}

// GetSession 获取玩家会话
func (gw *Gateway) GetSession(playerID int64) *Session {
	gw.sessionMu.RLock()
	defer gw.sessionMu.RUnlock()
	return gw.sessions[playerID]
}

// RemoveSession 移除玩家会话（仅当仍是当前会话时）
func (gw *Gateway) RemoveSession(session *Session) {
	gw.sessionMu.Lock()
	if cur, ok := gw.sessions[session.PlayerID]; ok && cur == session {
		delete(gw.sessions, session.PlayerID)
	}
	gw.sessionMu.Unlock()
}

func (gw *Gateway) snapshotSessions() []*Session {
	gw.sessionMu.RLock()
	defer gw.sessionMu.RUnlock()
	out := make([]*Session, 0, len(gw.sessions))
	for _, s := range gw.sessions {
		out = append(out, s)
	}
	return out
}

// OnClientDisconnect 玩家 TCP 断开：清理路由、匹配队列/房间
func (gw *Gateway) OnClientDisconnect(ctx context.Context, session *Session) {
	if session == nil || session.PlayerID == 0 {
		return
	}
	session.writeMu.Lock()
	roomID := session.RoomID
	matchMode := session.MatchMode
	playerName := session.PlayerName
	playerID := session.PlayerID
	session.writeMu.Unlock()

	gw.RemoveSession(session)

	if roomID != 0 {
		// 对局中断线 = 判负，通知房间
		if route, err := gw.redis.Get(ctx, "room:route:"+strconv.FormatInt(roomID, 10)); err == nil && route != "" {
			_ = gw.cc.QuitRoomTo(ctx, route, roomID, playerID)
		}
	} else if matchMode >= 0 {
		// 匹配等待中断线，广播移出匹配池
		gw.cc.BroadcastLeaveMatch(ctx, playerID, playerName, gw.AdvertiseAddr())
	}

	// 删除本网关的玩家路由（防重连定位到已下线玩家）
	if cur, err := gw.redis.Get(ctx, "gateway:route:"+strconv.FormatInt(playerID, 10)); err == nil && cur == gw.AdvertiseAddr() {
		_ = gw.redis.Del(ctx, "gateway:route:"+strconv.FormatInt(playerID, 10))
	}
	common.Info("[gateway] player %d 下线", playerID)
}

// HandleHeartbeat 收到任意数据/心跳时刷新活跃时间
func (gw *Gateway) HandleHeartbeat(session *Session) {
	session.writeMu.Lock()
	session.LastPingMs = common.NowMs()
	session.writeMu.Unlock()
}

// PongHeartbeat 心跳回执（msg_id=0，仅带心跳标志）
func (gw *Gateway) PongHeartbeat(session *Session) {
	gw.send(session, 0, netproto.FlagHeartbeat, nil)
}

// ---------------- 消息发送 ----------------

// send 向连接写一帧（并发安全）。注意：出站推送不刷新心跳时间，
// 只有入站数据（HandleHeartbeat）才算客户端活跃。
func (gw *Gateway) send(session *Session, msgID uint16, flags uint16, body []byte) {
	if session == nil {
		return
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	frame := netproto.EncodeFrame(msgID, flags, body)
	_, _ = session.Conn.Write(frame)
}

func (gw *Gateway) sendProto(session *Session, msgID uint16, flags uint16, msg proto.Message) {
	body, err := proto.Marshal(msg)
	if err != nil {
		common.Error("[gateway] marshal msg %d 失败: %v", msgID, err)
		return
	}
	gw.send(session, msgID, flags, body)
}

// SendKick 推送踢线通知（明文原因），随后由调用方关闭连接
func (gw *Gateway) SendKick(session *Session, reason string) {
	common.Info("[gateway] kick player %d: %s", session.PlayerID, reason)
	gw.send(session, netproto.MsgKick, netproto.FlagPush, []byte(reason))
}

// PushSnapshot 向玩家推送状态快照（状态同步模式）
func (gw *Gateway) PushSnapshot(playerID int64, snapshot *pb.StateSnapshot) {
	if s := gw.GetSession(playerID); s != nil {
		gw.sendProto(s, netproto.MsgSnapshot, netproto.FlagPush, snapshot)
	}
}

// PushFrame 向玩家推送输入帧（帧同步模式）
func (gw *Gateway) PushFrame(playerID int64, frameData *pb.FrameData) {
	if s := gw.GetSession(playerID); s != nil {
		gw.sendProto(s, netproto.MsgFrameData, netproto.FlagPush, frameData)
	}
}

// PushResult 向玩家推送对局结果
func (gw *Gateway) PushResult(playerID int64, result *pb.BattleResult) {
	if s := gw.GetSession(playerID); s != nil {
		gw.sendProto(s, netproto.MsgResult, netproto.FlagPush, result)
	}
}

// ---------------- 消息处理 ----------------

// HandleLoginReq 客户端登录：携带有效 token 则走断线重连（跳过密码），否则调用 login 服认证
func (gw *Gateway) HandleLoginReq(ctx context.Context, session *Session, body []byte) {
	req := &pb.LoginReq{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("[gateway] 解析 LoginReq 失败: %v", err)
		return
	}

	var player *pb.PlayerBase
	var token string

	if req.Token != "" {
		// 断线重连：复用 token 重新登录
		p, err := gw.cc.VerifyToken(ctx, req.Token)
		if err != nil {
			gw.sendProto(session, netproto.MsgLoginRsp, netproto.FlagRsp, &pb.LoginRsp{
				Ok: false, Reason: "token 失效，请重新登录",
			})
			return
		}
		player = p
		token = req.Token
	} else {
		arsp, err := gw.cc.Authenticate(ctx, req.Account, req.Password)
		if err != nil {
			gw.sendProto(session, netproto.MsgLoginRsp, netproto.FlagRsp, &pb.LoginRsp{
				Ok: false, Reason: "登录服不可用",
			})
			return
		}
		if !arsp.Ok {
			gw.sendProto(session, netproto.MsgLoginRsp, netproto.FlagRsp, &pb.LoginRsp{
				Ok: false, Reason: arsp.Reason,
			})
			return
		}
		player = arsp.Player
		token = arsp.Token
	}

	// 绑定会话（顶号检查）
	session.PlayerID = player.PlayerId
	session.PlayerName = player.Name
	session.Token = token
	session.Player = player
	session.MatchMode = -1
	gw.BindSession(session)

	// 写玩家路由表：gateway:route:{player_id} = 本网关 gRPC 地址
	_ = gw.redis.SetEx(ctx, "gateway:route:"+strconv.FormatInt(player.PlayerId, 10),
		gw.AdvertiseAddr(), routeTTLSec)

	common.Info("[gateway] player 登录: %d (%s)", player.PlayerId, player.Name)
	gw.sendProto(session, netproto.MsgLoginRsp, netproto.FlagRsp, &pb.LoginRsp{
		Ok: true, Token: token, Player: player,
	})
}

// HandleRegisterReq 注册账号（成功即自动登录绑定会话）
func (gw *Gateway) HandleRegisterReq(ctx context.Context, session *Session, body []byte) {
	req := &pb.RegisterReq{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("[gateway] 解析 RegisterReq 失败: %v", err)
		return
	}

	rrsp, err := gw.cc.Register(ctx, req.Account, req.Password, req.Name)
	if err != nil {
		gw.sendProto(session, netproto.MsgRegisterRsp, netproto.FlagRsp, &pb.RegisterRsp{
			Ok: false, Reason: "登录服不可用",
		})
		return
	}
	if !rrsp.Ok {
		gw.sendProto(session, netproto.MsgRegisterRsp, netproto.FlagRsp, rrsp)
		return
	}

	player := rrsp.Player
	session.PlayerID = player.PlayerId
	session.PlayerName = player.Name
	session.Token = rrsp.Token
	session.Player = player
	session.MatchMode = -1
	gw.BindSession(session)
	_ = gw.redis.SetEx(ctx, "gateway:route:"+strconv.FormatInt(player.PlayerId, 10),
		gw.AdvertiseAddr(), routeTTLSec)

	common.Info("[gateway] player 注册: %d (%s)", player.PlayerId, player.Name)
	gw.sendProto(session, netproto.MsgRegisterRsp, netproto.FlagRsp, rrsp)
}

// HandleMatchReq 发起匹配：入队到某 game 实例；异步轮询配对结果后回包
func (gw *Gateway) HandleMatchReq(ctx context.Context, session *Session, body []byte) {
	req := &pb.MatchReq{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("[gateway] 解析 MatchReq 失败: %v", err)
		return
	}

	// 校验登录态与 token
	if session.PlayerID == 0 {
		gw.sendProto(session, netproto.MsgMatchRsp, netproto.FlagRsp, &pb.MatchRsp{Ok: false, Reason: "未登录"})
		return
	}
	if req.Token != "" && req.Token != session.Token {
		gw.sendProto(session, netproto.MsgMatchRsp, netproto.FlagRsp, &pb.MatchRsp{Ok: false, Reason: "token 不匹配"})
		return
	}

	mode := req.Mode
	if mode != 0 && mode != 1 {
		mode = 1
	}
	session.MatchMode = mode

	go gw.matchWorker(ctx, session, mode)
}

// matchWorker 在独立 goroutine 执行同步 gRPC，避免阻塞 TCP 处理
func (gw *Gateway) matchWorker(ctx context.Context, session *Session, mode int32) {
	playerID, playerName, gwAddr := session.PlayerID, session.PlayerName, gw.AdvertiseAddr()

	fail := func(reason string) {
		gw.sendMatchRsp(session, false, 0, reason)
	}
	alive := func() bool {
		return gw.GetSession(playerID) == session
	}

	game, ok := gw.cc.PickGame()
	if !ok {
		fail("游戏服不可用")
		return
	}

	// 入队前确认会话仍在线（与 C++ MatchWorkerLoop 的 IsSessionAlive 一致）
	if !alive() {
		return
	}

	// 入队（非阻塞，room_id=0 表示排队中）
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rsp, err := gw.cc.JoinMatch(ctx, game, playerID, playerName, gwAddr, mode)
	if err != nil {
		common.Warn("[match] player=%d JoinMatch RPC 失败: %v", playerID, err)
		fail("游戏服不可用")
		return
	}
	if !rsp.Ok {
		fail(rsp.Reason)
		return
	}
	if rsp.RoomId != 0 {
		gw.sendMatchRsp(session, true, rsp.RoomId, "")
		return
	}

	// 排队中：每 500ms 轮询配对结果，最多 11s
	common.Info("[match] player=%d 排队等待对手 (mode=%d)", playerID, mode)
	ticker := time.NewTicker(matchPollInterval)
	defer ticker.Stop()

	deadline := time.Now().Add(matchPollInterval * matchPollTimes)
	for {
		select {
		case <-ctx.Done():
			if alive() {
				fail("匹配超时，未找到对手")
			}
			return
		case <-ticker.C:
			if !alive() {
				return
			}
			if time.Now().After(deadline) {
				fail("匹配超时，未找到对手")
				return
			}
			qrsp, err := gw.cc.QueryMatchResult(ctx, game, playerID)
			if err != nil {
				continue
			}
			if qrsp.Ok && qrsp.RoomId != 0 {
				gw.sendMatchRsp(session, true, qrsp.RoomId, "")
				return
			}
		}
	}
}

func (gw *Gateway) sendMatchRsp(session *Session, ok bool, roomID int64, reason string) {
	if ok && roomID != 0 {
		session.writeMu.Lock()
		session.RoomID = roomID
		session.MatchMode = -1
		session.writeMu.Unlock()
		gw.sendProto(session, netproto.MsgMatchRsp, netproto.FlagRsp, &pb.MatchRsp{Ok: true, RoomId: roomID})
		return
	}
	session.writeMu.Lock()
	session.MatchMode = -1
	session.writeMu.Unlock()
	r := reason
	if r == "" {
		r = "匹配失败"
	}
	gw.sendProto(session, netproto.MsgMatchRsp, netproto.FlagRsp, &pb.MatchRsp{Ok: false, Reason: r})
}

// HandleOpInput 转发玩家操作：查 room:route 定向到房间所在 game 实例
func (gw *Gateway) HandleOpInput(ctx context.Context, session *Session, body []byte) {
	op := &pb.OpInput{}
	if err := proto.Unmarshal(body, op); err != nil {
		common.Error("[gateway] 解析 OpInput 失败: %v", err)
		return
	}

	session.writeMu.Lock()
	roomID := session.RoomID
	session.writeMu.Unlock()
	if roomID == 0 {
		return
	}
	route, err := gw.redis.Get(ctx, "room:route:"+strconv.FormatInt(roomID, 10))
	if err != nil || route == "" {
		// 路由不存在：房间可能已结束
		return
	}
	_, _ = gw.cc.SubmitOpTo(ctx, route, roomID, session.PlayerID, op)
}

// HandleRankQuery 查询排行榜 TopN
func (gw *Gateway) HandleRankQuery(ctx context.Context, session *Session, body []byte) {
	req := &pb.RankQuery{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("[gateway] 解析 RankQuery 失败: %v", err)
		return
	}
	n := req.N
	if n <= 0 {
		n = 10
	}

	trsp, err := gw.cc.GetTopN(ctx, n)
	if err != nil {
		gw.sendProto(session, netproto.MsgRankRsp, netproto.FlagRsp, &pb.RankRsp{Ok: false})
		return
	}
	gw.sendProto(session, netproto.MsgRankRsp, netproto.FlagRsp, &pb.RankRsp{Ok: true, Players: trsp.Players})
}
