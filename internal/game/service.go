package game

import (
	"context"
	"gameserver/api/pb"
	"gameserver/internal/common"
	"gameserver/internal/rpc"
	"sync"
	"time"
)

// GameService 游戏服务
type GameService struct {
	pb.UnimplementedGameServiceServer

	rooms     map[int64]*Room
	roomMu    sync.RWMutex
	matchPool map[int32][]*MatchPlayer // 匹配池
	matchMu   sync.RWMutex
}

// MatchPlayer 匹配中的玩家
type MatchPlayer struct {
	PlayerID    int64
	PlayerName  string
	GatewayAddr string
	JoinTimeMs  int64
}

// NewGameService 创建游戏服务
func NewGameService() *GameService {
	return &GameService{
		rooms:     make(map[int64]*Room),
		matchPool: make(map[int32][]*MatchPlayer),
	}
}

func (gs *GameService) JoinMatch(ctx context.Context, req *pb.MatchJoinReq) (*pb.MatchJoinRsp, error) {
	gs.matchMu.Lock()
	defer gs.matchMu.Unlock()

	mode := req.Mode
	if mode != 0 && mode != 1 {
		mode = 1
	}

	players := gs.matchPool[mode]
	if len(players) == 0 {
		gs.matchPool[mode] = append(players, &MatchPlayer{
			PlayerID:    req.PlayerId,
			PlayerName:  req.PlayerName,
			GatewayAddr: req.GatewayAddr,
			JoinTimeMs:  common.NowMs(),
		})
		return &pb.MatchJoinRsp{
			Ok:     true,
			RoomId: 0,
		}, nil
	}

	// 找到一个玩家配对
	p1 := players[0]
	gs.matchPool[mode] = players[1:]

	// 创建房间
	roomID := common.GenerateRoomID()
	p1Player := &RoomPlayer{
		PlayerID:    p1.PlayerID,
		Name:        p1.PlayerName,
		GatewayAddr: p1.GatewayAddr,
		X:           25,
		Y:           25,
		HP:          MaxHp,
		Alive:       true,
	}

	p2Player := &RoomPlayer{
		PlayerID:    req.PlayerId,
		Name:        req.PlayerName,
		GatewayAddr: req.GatewayAddr,
		X:           75,
		Y:           75,
		HP:          MaxHp,
		Alive:       true,
	}

	room := NewRoom(roomID, p1Player, p2Player, mode)
	gs.rooms[roomID] = room
	room.Started = true

	common.Info("[game] Created room %d: %d vs %d", roomID, p1.PlayerID, req.PlayerId)

	return &pb.MatchJoinRsp{
		Ok:     true,
		RoomId: roomID,
	}, nil
}

func (gs *GameService) QueryMatchResult(ctx context.Context, req *pb.MatchQueryReq) (*pb.MatchQueryRsp, error) {
	gs.matchMu.RLock()
	defer gs.matchMu.RUnlock()

	for _, players := range gs.matchPool {
		for _, p := range players {
			if p.PlayerID == req.PlayerId {
				return &pb.MatchQueryRsp{
					Ok:     true,
					RoomId: 0,
				}, nil
			}
		}
	}

	return &pb.MatchQueryRsp{
		Ok:     true,
		RoomId: 0,
	}, nil
}

func (gs *GameService) SubmitOp(ctx context.Context, req *pb.OpForwardReq) (*pb.OpForwardRsp, error) {
	gs.roomMu.RLock()
	room, ok := gs.rooms[req.RoomId]
	gs.roomMu.RUnlock()

	if !ok {
		return &pb.OpForwardRsp{
			Ok:     false,
			Reason: "room not found",
		}, nil
	}

	room.SubmitOp(req.PlayerId, req.Op)

	return &pb.OpForwardRsp{Ok: true}, nil
}

func (gs *GameService) QuitRoom(ctx context.Context, req *pb.QuitRoomReq) (*pb.Empty, error) {
	gs.roomMu.RLock()
	room, ok := gs.rooms[req.RoomId]
	gs.roomMu.RUnlock()

	if ok {
		room.OnPlayerQuit(req.PlayerId)
	}

	return &pb.Empty{}, nil
}

func (gs *GameService) LeaveMatch(ctx context.Context, req *pb.LeaveMatchReq) (*pb.Empty, error) {
	gs.matchMu.Lock()
	defer gs.matchMu.Unlock()

	for mode, players := range gs.matchPool {
		newPlayers := make([]*MatchPlayer, 0)
		for _, p := range players {
			if p.PlayerID != req.PlayerId {
				newPlayers = append(newPlayers, p)
			}
		}
		gs.matchPool[mode] = newPlayers
	}

	return &pb.Empty{}, nil
}

func (gs *GameService) Tick() {
	gs.roomMu.Lock()
	for _, room := range gs.rooms {
		room.Tick()
	}
	gs.roomMu.Unlock()
}

func (gs *GameService) GetRoom(roomID int64) *Room {
	gs.roomMu.RLock()
	defer gs.roomMu.RUnlock()
	return gs.rooms[roomID]
}

func (gs *GameService) RemoveRoom(roomID int64) {
	gs.roomMu.Lock()
	defer gs.roomMu.Unlock()
	delete(gs.rooms, roomID)
}

func (gs *GameService) Start() {
	ticker := time.NewTicker(time.Duration(TickMs) * time.Millisecond)
	go func() {
		for range ticker.C {
			gs.Tick()
		}
	}()
}