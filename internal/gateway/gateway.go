package gateway

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/net"
	"gameserver/internal/rpc"
	"google.golang.org/protobuf/proto"
	"io"
	"sync"
	"time"
)

// Session 玩家会话
type Session struct {
	Conn       io.ReadWriteCloser
	PlayerID   int64
	Token      string
	Player     *pb.PlayerBase
	RoomID     int64
	MatchMode  int32
	LastPingMs int64
	mu         sync.Mutex
}

// Gateway 网关服务器
type Gateway struct {
	name       string
	host       string
	tcpPort    int
	grpcPort   int
	centerHost string
	centerPort int
	loginHost  string
	loginPort  int
	redisHost  string
	redisPort  int

	sessions   map[int64]*Session
	sessionMu  sync.RWMutex

	redis *rpc.RedisClient
}

// NewGateway 创建网关
func NewGateway(name string, tcpPort, grpcPort int) *Gateway {
	return &Gateway{
		name:     name,
		tcpPort:  tcpPort,
		grpcPort: grpcPort,
		sessions: make(map[int64]*Session),
	}
}

// Init 初始化网关
func (gw *Gateway) Init(redisHost string, redisPort int) {
	gw.redis = rpc.NewRedisClient(redisHost, redisPort)
	gw.redisHost = redisHost
	gw.redisPort = redisPort
}

// AddSession 添加会话
func (gw *Gateway) AddSession(session *Session) {
	gw.sessionMu.Lock()
	defer gw.sessionMu.Unlock()
	if session.PlayerID > 0 {
		gw.sessions[session.PlayerID] = session
	}
}

// RemoveSession 移除会话
func (gw *Gateway) RemoveSession(playerID int64) {
	gw.sessionMu.Lock()
	defer gw.sessionMu.Unlock()
	delete(gw.sessions, playerID)
}

// GetSession 获取会话
func (gw *Gateway) GetSession(playerID int64) *Session {
	gw.sessionMu.RLock()
	defer gw.sessionMu.RUnlock()
	return gw.sessions[playerID]
}

func (gw *Gateway) HandleLoginReq(ctx context.Context, session *Session, body []byte) {
	req := &pb.LoginReq{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("Failed to unmarshal LoginReq: %v", err)
		return
	}

	// TODO: 调用 login 服务进行身份验证
	resp := &pb.LoginRsp{
		Ok:    true,
		Token: "token_" + time.Now().Format("20060102150405"),
		Player: &pb.PlayerBase{
			PlayerId: 1,
			Name:     "player_" + time.Now().Format("20060102150405"),
		},
	}

	respBody, _ := proto.Marshal(resp)
	frame := net.EncodeFrame(net.MsgLoginRsp, net.FlagRsp, respBody)
	session.Conn.Write(frame)

	session.PlayerID = resp.Player.PlayerId
	session.Token = resp.Token
	session.Player = resp.Player
	gw.AddSession(session)
}

func (gw *Gateway) HandleMatchReq(ctx context.Context, session *Session, body []byte) {
	req := &pb.MatchReq{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("Failed to unmarshal MatchReq: %v", err)
		return
	}

	// TODO: 调用 game 服务进行匹配
	resp := &pb.MatchRsp{
		Ok:     true,
		RoomId: 0,
	}

	respBody, _ := proto.Marshal(resp)
	frame := net.EncodeFrame(net.MsgMatchRsp, net.FlagRsp, respBody)
	session.Conn.Write(frame)

	session.MatchMode = req.Mode
}

func (gw *Gateway) HandleOpInput(ctx context.Context, session *Session, body []byte) {
	op := &pb.OpInput{}
	if err := proto.Unmarshal(body, op); err != nil {
		common.Error("Failed to unmarshal OpInput: %v", err)
		return
	}

	// TODO: 转发给 game 服务
}

func (gw *Gateway) HandleRankQuery(ctx context.Context, session *Session, body []byte) {
	req := &pb.RankQuery{}
	if err := proto.Unmarshal(body, req); err != nil {
		common.Error("Failed to unmarshal RankQuery: %v", err)
		return
	}

	// TODO: 调用 rank 服务获取排行榜
	resp := &pb.RankRsp{
		Ok:      true,
		Players: []*pb.PlayerBase{},
	}

	respBody, _ := proto.Marshal(resp)
	frame := net.EncodeFrame(net.MsgRankRsp, net.FlagRsp, respBody)
	session.Conn.Write(frame)
}

func (gw *Gateway) PushSnapshot(playerID int64, snapshot *pb.StateSnapshot) {
	session := gw.GetSession(playerID)
	if session == nil {
		return
	}

	body, _ := proto.Marshal(snapshot)
	frame := net.EncodeFrame(net.MsgSnapshot, net.FlagPush, body)
	session.Conn.Write(frame)
}

func (gw *Gateway) PushFrame(playerID int64, frameData *pb.FrameData) {
	session := gw.GetSession(playerID)
	if session == nil {
		return
	}

	body, _ := proto.Marshal(frameData)
	frameBytes := net.EncodeFrame(net.MsgFrameData, net.FlagPush, body)
	session.Conn.Write(frameBytes)
}

func (gw *Gateway) PushResult(playerID int64, result *pb.BattleResult) {
	session := gw.GetSession(playerID)
	if session == nil {
		return
	}

	body, _ := proto.Marshal(result)
	frameBytes := net.EncodeFrame(net.MsgResult, net.FlagPush, body)
	session.Conn.Write(frameBytes)
}

func (gw *Gateway) HandleHeartbeat(session *Session) {
	session.mu.Lock()
	session.LastPingMs = common.NowMs()
	session.mu.Unlock()
}