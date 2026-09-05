package game

import (
	"gameserver/api/pb"
	"gameserver/internal/common"
	"math"
	"sync"
)

// 游戏平衡常数
const (
	MapSize             = 100.0         // 地图大小
	MaxHp               = 100           // 最大血量
	MoveSpeed           = 60.0          // 移动速度
	AtkRange            = 8.0           // 普攻距离
	AtkDamage           = 10            // 普攻伤害
	AtkCooldownMs       = 500           // 普攻冷却（毫秒）
	SkillRange          = 15.0          // 技能距离
	SkillDamage         = 25            // 技能伤害
	SkillCooldownMs     = 2000          // 技能冷却（毫秒）
	TimeoutMs           = 60 * 1000     // 对局超时时间
	TickMs              = 50            // Tick 间隔（毫秒）
	AtkCooldownFrames   = AtkCooldownMs / TickMs
	SkillCooldownFrames = SkillCooldownMs / TickMs
)

// RoomPlayer 房间玩家信息
type RoomPlayer struct {
	PlayerID       int64
	Name           string
	GatewayAddr    string
	X, Y           float32
	HP             int32
	Alive          bool
	StepDx, StepDy float32
	Stepping       bool
	LastAtkFrame   int64
	LastSkillFrame int64
	Score          int64
}

// Room 游戏房间
type Room struct {
	RoomID    int64
	CreatedMs int64
	FinishMs  int64
	Tick      int64
	Finished  bool
	Started   bool
	Mode      int32

	P1, P2 *RoomPlayer

	mu         sync.Mutex
	pendingOps []opRecord
	frameOps   []opRecord
}

// opRecord 操作记录
type opRecord struct {
	PlayerID int64
	Op       *pb.OpInput
}

func NewRoom(roomID int64, p1, p2 *RoomPlayer, mode int32) *Room {
	return &Room{
		RoomID:    roomID,
		CreatedMs: common.NowMs(),
		Mode:      mode,
		P1:        p1,
		P2:        p2,
		Finished:  false,
		Started:   false,
	}
}

func (r *Room) FindPlayer(playerID int64) *RoomPlayer {
	if r.P1.PlayerID == playerID {
		return r.P1
	}
	if r.P2.PlayerID == playerID {
		return r.P2
	}
	return nil
}

func (r *Room) SubmitOp(playerID int64, op *pb.OpInput) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Finished {
		return
	}
	if r.FindPlayer(playerID) == nil {
		return
	}

	r.pendingOps = append(r.pendingOps, opRecord{playerID, op})
}

func (r *Room) OnPlayerQuit(playerID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Finished {
		return
	}

	quit := r.FindPlayer(playerID)
	if quit == nil || !quit.Alive {
		return
	}

	quit.Alive = false
	quit.HP = 0

	var other *RoomPlayer
	if r.P1.PlayerID == playerID {
		other = r.P2
	} else {
		other = r.P1
	}

	r.Finished = true
	r.FinishMs = common.NowMs()
	common.Info("[room %d] player %d quit, winner: %d", r.RoomID, playerID, other.PlayerID)
}

func (r *Room) ApplyOp(p *RoomPlayer, op *pb.OpInput) {
	switch op.OpType {
	case pb.OpType_OP_MOVE:
		p.StepDx = op.MoveDx * 2.0
		p.StepDy = op.MoveDy * 4.0
		p.Stepping = true

	case pb.OpType_OP_ATK:
		if r.Tick-p.LastAtkFrame < AtkCooldownFrames {
			break
		}
		p.LastAtkFrame = r.Tick

		target := r.FindPlayer(op.TargetId)
		if target == nil || !target.Alive {
			break
		}

		dist := common.Dist(p.X, p.Y, target.X, target.Y)
		if dist > AtkRange {
			break
		}

		target.HP -= AtkDamage
		p.Score += 2
		common.Info("[room %d] atk: %d -> %d, dmg=%d", r.RoomID, p.PlayerID, target.PlayerID, AtkDamage)

		if target.HP <= 0 {
			target.Alive = false
			r.Finished = true
			r.FinishMs = common.NowMs()
		}

	case pb.OpType_OP_SKILL:
		if r.Tick-p.LastSkillFrame < SkillCooldownFrames {
			break
		}
		p.LastSkillFrame = r.Tick

		target := r.FindPlayer(op.TargetId)
		if target == nil || !target.Alive {
			break
		}

		dist := common.Dist(p.X, p.Y, target.X, target.Y)
		if dist > SkillRange {
			break
		}

		target.HP -= SkillDamage
		p.Score += 5
		common.Info("[room %d] skill: %d -> %d, dmg=%d", r.RoomID, p.PlayerID, target.PlayerID, SkillDamage)

		if target.HP <= 0 {
			target.Alive = false
			r.Finished = true
			r.FinishMs = common.NowMs()
		}
	}
}

func (r *Room) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Finished {
		return
	}

	r.Tick++

	// 消费输入
	r.frameOps = r.frameOps[:0]
	for _, op := range r.pendingOps {
		p := r.FindPlayer(op.PlayerID)
		if p != nil && p.Alive {
			r.ApplyOp(p, op.Op)
			r.frameOps = append(r.frameOps, op)
		}
	}
	r.pendingOps = r.pendingOps[:0]

	// 更新位置
	for _, p := range []*RoomPlayer{r.P1, r.P2} {
		if !p.Stepping || !p.Alive {
			continue
		}

		newX := p.X + p.StepDx*MoveSpeed/1000*(TickMs/1000)
		newY := p.Y + p.StepDy*MoveSpeed/1000*(TickMs/1000)

		if newX < 0 {
			newX = 0
		} else if newX > MapSize {
			newX = MapSize
		}
		if newY < 0 {
			newY = 0
		} else if newY > MapSize {
			newY = MapSize
		}

		p.X = newX
		p.Y = newY
	}

	// 超时判定
	now := common.NowMs()
	if now-r.CreatedMs > TimeoutMs {
		if r.P1.HP > r.P2.HP {
			r.Finished = true
		} else if r.P2.HP > r.P1.HP {
			r.Finished = true
		} else {
			r.Finished = true
		}
		r.FinishMs = now
	}
}

func (r *Room) GetSnapshot(full bool) *pb.StateSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot := &pb.StateSnapshot{
		RoomId: r.RoomID,
		Tick:   r.Tick,
		Full:   full,
	}

	snapshot.Entities = append(snapshot.Entities, &pb.EntityState{
		PlayerId: r.P1.PlayerID,
		X:        r.P1.X,
		Y:        r.P1.Y,
		Hp:       r.P1.HP,
		Alive:    r.P1.Alive,
	})

	snapshot.Entities = append(snapshot.Entities, &pb.EntityState{
		PlayerId: r.P2.PlayerID,
		X:        r.P2.X,
		Y:        r.P2.Y,
		Hp:       r.P2.HP,
		Alive:    r.P2.Alive,
	})

	return snapshot
}

func (r *Room) GetFrameData(full bool) *pb.FrameData {
	r.mu.Lock()
	defer r.mu.Unlock()

	frame := &pb.FrameData{
		RoomId:  r.RoomID,
		FrameSeq: r.Tick,
		Full:    full,
	}

	if full {
		frame.Entities = append(frame.Entities, &pb.EntityState{
			PlayerId: r.P1.PlayerID,
			X:        r.P1.X,
			Y:        r.P1.Y,
			Hp:       r.P1.HP,
			Alive:    r.P1.Alive,
		})
		frame.Entities = append(frame.Entities, &pb.EntityState{
			PlayerId: r.P2.PlayerID,
			X:        r.P2.X,
			Y:        r.P2.Y,
			Hp:       r.P2.HP,
			Alive:    r.P2.Alive,
		})
	}

	for _, op := range r.frameOps {
		frame.Ops = append(frame.Ops, &pb.FrameOp{
			PlayerId: op.PlayerID,
			Op:       op.Op,
		})
	}

	return frame
}

func (r *Room) IsFinished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Finished
}

func (r *Room) GetWinner() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.P1.Alive && !r.P2.Alive {
		return r.P1.PlayerID
	}
	if r.P2.Alive && !r.P1.Alive {
		return r.P2.PlayerID
	}
	if r.P1.HP > r.P2.HP {
		return r.P1.PlayerID
	}
	return r.P2.PlayerID
}