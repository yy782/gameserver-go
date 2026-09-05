package game

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Redis Key（与 C++ src/game/main.cpp 一致）
const (
	kMatchPoolFsKey = "match:pool:fs"
	kMatchPoolSsKey = "match:pool:ss"
	kRoomSeqKey     = "room:seq"

	kMatchTimeoutMs  = 10000 // 匹配池等待对手超时
	kResultTtlSec    = 30    // 配对结果 TTL
	kRoomRouteTtlSec = 300   // 房间路由 TTL，覆盖 60s 对局 + 2s 清理

	matchResultPrefix = "match:result:"
	roomRoutePrefix   = "room:route:"
)

func MatchPoolKey(mode int32) string {
	if mode == 0 {
		return kMatchPoolFsKey
	}
	return kMatchPoolSsKey
}

func matchResultKey(playerID int64) string {
	return matchResultPrefix + strconv.FormatInt(playerID, 10)
}

func roomRouteKey(roomID int64) string {
	return roomRoutePrefix + strconv.FormatInt(roomID, 10)
}

// kPairLua 使用 Lua 天然原子：取分数最低的 2 个等待者并移除（与 C++ 一致）。
// 跨 game 实例并发时也不会重复取到同一对。
const kPairLua = `
local members = redis.call('ZRANGE', KEYS[1], 0, 1)
if #members >= 2 then
  redis.call('ZREM', KEYS[1], members[1], members[2])
  return {members[1], members[2]}
end
return {}`

// kInitRoomSeqLua 初始化全局房间号起点（不存在时 SET），防止两个 game 实例各自从 0 开始撞号
const kInitRoomSeqLua = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('SET', KEYS[1], ARGV[1])
  return 1
end
return 0`

// MatchWaiter 匹配成员（member = "player_id|player_name|gateway_addr"）
type MatchWaiter struct {
	PlayerID    int64
	PlayerName  string
	GatewayAddr string
}

func ParseMember(s string) MatchWaiter {
	w := MatchWaiter{}
	p1 := strings.Index(s, "|")
	p2 := -1
	if p1 != -1 {
		p2 = strings.Index(s[p1+1:], "|")
	}
	if p1 != -1 {
		if v, err := strconv.ParseInt(s[:p1], 10, 64); err == nil {
			w.PlayerID = v
		}
	}
	if p2 != -1 {
		absP2 := p1 + 1 + p2
		w.PlayerName = s[p1+1 : absP2]
		w.GatewayAddr = s[absP2+1:]
	}
	return w
}

func encodeMember(w MatchWaiter) string {
	return strconv.FormatInt(w.PlayerID, 10) + "|" + w.PlayerName + "|" + w.GatewayAddr
}

// GameService 游戏服务
type GameService struct {
	pb.UnimplementedGameServiceServer

	name     string
	selfAddr string // 本实例对外地址（写房间路由表用）

	redis   *rpc.RedisClient
	cluster *rpc.ClusterClient
	pushCli *rpc.GatewayPushClient

	rooms     map[int64]*Room
	roomsLock sync.Mutex

	stop chan struct{}
}

func NewGameService(redis *rpc.RedisClient, cluster *rpc.ClusterClient, pushCli *rpc.GatewayPushClient,
	selfAddr, name string) *GameService {
	return &GameService{
		name:     name,
		selfAddr: selfAddr,
		redis:    redis,
		cluster:  cluster,
		pushCli:  pushCli,
		rooms:    make(map[int64]*Room),
		stop:     make(chan struct{}),
	}
}

// InitRoomSeq 初始化全局房间号起点（与 C++ main 一致）
func (gs *GameService) InitRoomSeq(ctx context.Context) {
	if _, err := gs.redis.EvalArray(ctx, kInitRoomSeqLua, []string{kRoomSeqKey}, []string{"1000"}); err != nil {
		common.Warn("[game] 初始化房间号起点失败: %v", err)
	}
}

// ---------------- 推送 ----------------

func (gs *GameService) pushSnapshot(p *RoomPlayer, snap *pb.StateSnapshot) {
	if !gs.pushCli.PushSnapshot(p.GatewayAddr, p.PlayerID, snap) {
		common.Error("[game] PushSnapshot 失败 player=%d gw=%s", p.PlayerID, p.GatewayAddr)
	}
}

func (gs *GameService) pushFrame(p *RoomPlayer, frame *pb.FrameData) {
	if !gs.pushCli.PushFrame(p.GatewayAddr, p.PlayerID, frame) {
		common.Error("[game] PushFrame 失败 player=%d gw=%s", p.PlayerID, p.GatewayAddr)
	}
}

func (gs *GameService) pushResult(p *RoomPlayer, result *pb.BattleResult) {
	if !gs.pushCli.PushResult(p.GatewayAddr, p.PlayerID, result) {
		common.Error("[game] PushResult 失败 player=%d gw=%s", p.PlayerID, p.GatewayAddr)
	}
}

// submitScore 上报战斗分数到排行榜服务
func (gs *GameService) submitScore(playerID int64, score int32) (int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return gs.cluster.SubmitScore(ctx, playerID, score)
}

// ---------------- gRPC：匹配 ----------------

func (gs *GameService) JoinMatch(ctx context.Context, req *pb.MatchJoinReq) (*pb.MatchJoinRsp, error) {
	member := encodeMember(MatchWaiter{
		PlayerID:    req.PlayerId,
		PlayerName:  req.PlayerName,
		GatewayAddr: req.GatewayAddr,
	})
	mode := req.Mode
	if mode != 0 {
		mode = 1 // proto3 无默认值，非 0 一律按状态同步
	}
	if err := gs.redis.ZAdd(ctx, MatchPoolKey(mode), float64(common.NowMs()), member); err != nil {
		common.Error("[game] 玩家 %d 入队失败: %v", req.PlayerId, err)
		return &pb.MatchJoinRsp{Ok: false, Reason: "匹配池不可用"}, nil
	}
	common.Info("[match] player %d(%s) joined pool mode=%d, gateway=%s",
		req.PlayerId, req.PlayerName, mode, req.GatewayAddr)
	return &pb.MatchJoinRsp{Ok: true, RoomId: 0}, nil // 还没配对成功
}

func (gs *GameService) QueryMatchResult(ctx context.Context, req *pb.MatchQueryReq) (*pb.MatchQueryRsp, error) {
	rsp := &pb.MatchQueryRsp{Ok: true}
	key := matchResultKey(req.PlayerId)
	if v, err := gs.redis.Get(ctx, key); err == nil && v != "" {
		roomID, perr := strconv.ParseInt(v, 10, 64)
		if perr == nil {
			rsp.RoomId = roomID
			_ = gs.redis.Del(ctx, key)
		}
	}
	return rsp, nil
}

func (gs *GameService) LeaveMatch(ctx context.Context, req *pb.LeaveMatchReq) (*pb.Empty, error) {
	member := encodeMember(MatchWaiter{
		PlayerID:    req.PlayerId,
		PlayerName:  req.PlayerName,
		GatewayAddr: req.GatewayAddr,
	})
	_ = gs.redis.ZRem(ctx, kMatchPoolFsKey, member)
	_ = gs.redis.ZRem(ctx, kMatchPoolSsKey, member)
	common.Info("[match] player %d left pool (offline): %s", req.PlayerId, member)
	return &pb.Empty{}, nil
}

// ---------------- gRPC：对战 ----------------

func (gs *GameService) SubmitOp(ctx context.Context, req *pb.OpForwardReq) (*pb.OpForwardRsp, error) {
	gs.roomsLock.Lock()
	room := gs.rooms[req.RoomId]
	gs.roomsLock.Unlock()
	if room == nil {
		return &pb.OpForwardRsp{Ok: false, Reason: "房间不存在"}, nil
	}
	room.SubmitOp(req.PlayerId, req.Op)
	return &pb.OpForwardRsp{Ok: true}, nil
}

func (gs *GameService) QuitRoom(ctx context.Context, req *pb.QuitRoomReq) (*pb.Empty, error) {
	gs.roomsLock.Lock()
	room := gs.rooms[req.RoomId]
	gs.roomsLock.Unlock()
	if room != nil {
		room.OnPlayerQuit(req.PlayerId)
	}
	return &pb.Empty{}, nil
}

// ---------------- 后台任务 ----------------

// Start 启动游戏服务后台任务：房间主循环 + 匹配线程
func (gs *GameService) Start(ctx context.Context) {
	go gs.tickLoop()
	go gs.matchLoop(ctx)
	common.Info("[game] %s 后台任务已启动", gs.name)
}

// tickLoop 以 kTickMs 频率推进所有房间（与 C++ g_tick_thread 一致）
func (gs *GameService) tickLoop() {
	ticker := time.NewTicker(time.Duration(TickMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-gs.stop:
			return
		case <-ticker.C:
			gs.TickAll()
		}
	}
}

// TickAll 推进所有房间逻辑帧，并清理已结束房间（延迟 2s）
func (gs *GameService) TickAll() {
	gs.roomsLock.Lock()
	rooms := make([]*Room, 0, len(gs.rooms))
	for _, r := range gs.rooms {
		rooms = append(rooms, r)
	}
	gs.roomsLock.Unlock()

	for _, r := range rooms {
		if !r.IsFinished() {
			r.Tick()
		}
	}

	// 清理已结束的房间（延迟 2s，保证结果推送完成；路由表由 Redis TTL 自动过期）
	now := common.NowMs()
	gs.roomsLock.Lock()
	for id, r := range gs.rooms {
		if r.IsFinished() && now-r.FinishTime() > FinishCleanupMs {
			delete(gs.rooms, id)
			common.Info("[game] remove finished room %d", id)
		}
	}
	gs.roomsLock.Unlock()
}

// matchLoop 匹配线程：每 200ms 尝试配对 + 清理超时成员
func (gs *GameService) matchLoop(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, mode := range []int32{0, 1} {
				gs.MatchTick(ctx, mode)
			}
		}
	}
}

// MatchTick 对一个 mode 的匹配池执行一轮配对
func (gs *GameService) MatchTick(ctx context.Context, mode int32) {
	pool := MatchPoolKey(mode)

	// 原子取出 2 个等待者
	members, err := gs.redis.EvalArray(ctx, kPairLua, []string{pool}, nil)
	if err != nil {
		common.Warn("[game] 配对脚本执行失败: %v", err)
		return
	}
	if len(members) >= 2 {
		a := ParseMember(members[0])
		b := ParseMember(members[1])
		roomID, err := gs.redis.Incr(ctx, kRoomSeqKey)
		if err != nil {
			common.Error("[game] 生成房间号失败: %v", err)
			return
		}
		p1 := &RoomPlayer{
			PlayerID:    a.PlayerID,
			Name:        a.PlayerName,
			GatewayAddr: a.GatewayAddr,
			HP:          MaxHp,
			Alive:       true,
		}
		p2 := &RoomPlayer{
			PlayerID:    b.PlayerID,
			Name:        b.PlayerName,
			GatewayAddr: b.GatewayAddr,
			HP:          MaxHp,
			Alive:       true,
		}
		room := NewRoom(roomID, p1, p2, mode, gs)

		gs.roomsLock.Lock()
		gs.rooms[roomID] = room
		gs.roomsLock.Unlock()

		_ = gs.redis.SetEx(ctx, matchResultKey(p1.PlayerID), strconv.FormatInt(roomID, 10), kResultTtlSec)
		_ = gs.redis.SetEx(ctx, matchResultKey(p2.PlayerID), strconv.FormatInt(roomID, 10), kResultTtlSec)
		_ = gs.redis.SetEx(ctx, roomRouteKey(roomID), gs.selfAddr, kRoomRouteTtlSec)

		common.Info("[match] room %d created on %s mode=%d: %d vs %d",
			roomID, gs.selfAddr, mode, p1.PlayerID, p2.PlayerID)
	}

	// 清理匹配池中超时的等待者
	expiredBefore := float64(common.NowMs() - kMatchTimeoutMs)
	expired, err := gs.redis.ZRangeByScore(ctx, pool, -1e18, expiredBefore)
	if err != nil {
		return
	}
	for _, m := range expired {
		w := ParseMember(m)
		_ = gs.redis.ZRem(ctx, pool, m)
		common.Info("[match] player %d match timeout, removed", w.PlayerID)
	}
}

// Stop 停止后台任务
func (gs *GameService) Stop() {
	select {
	case <-gs.stop:
	default:
		close(gs.stop)
	}
}
