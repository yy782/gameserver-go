package game

import (
	"gameserver/api/pb"
	"gameserver/internal/common"
	"math"
	"sort"
	"sync"
)

// 战斗平衡常数（与 C++ src/game/room.h 中 battle 常量一一对应）
const (
	MapSize               = 100.0 // 正方形地图边长
	MaxHp                 = 100
	MoveSpeed             = 60.0 // 每秒移动距离
	AtkRange              = 8.0  // 普攻距离
	AtkDamage             = 10
	AtkCooldownMs         = 500
	SkillRange            = 15.0 // 技能距离
	SkillDamage           = 25
	SkillCooldownMs       = 2000
	TimeoutMs             = 60 * 1000 // 对局最长 60s
	TickMs                = 50        // 逻辑 tick 频率 20Hz
	FinishCleanupMs       = 2000      // 房间结束后延迟清理时间（保证结果推送完成）
	CellW                 = 2.0       // 终端网格水平一格
	CellH                 = 4.0       // 终端网格垂直一格
	SpawnOffset           = 20.0
	WinScore        int32 = 10 // 胜方得分
	LoseScore       int32 = 1  // 负方得分

	// 帧同步冷却以逻辑帧号计（确定性）：帧号 = 毫秒 / kTickMs
	AtkCooldownFrames   = AtkCooldownMs / TickMs   // 10 帧
	SkillCooldownFrames = SkillCooldownMs / TickMs // 40 帧
)

// roomSink 房间对外部服务的出口：广播帧/快照、推送结算、上报分数。
// 声明为接口而非 *GameService，便于单元测试注入内存桩，无需真实 gRPC/Redis。
type roomSink interface {
	submitScore(playerID int64, score int32) (int32, error)
	pushFrame(p *RoomPlayer, frame *pb.FrameData)
	pushSnapshot(p *RoomPlayer, snap *pb.StateSnapshot)
	pushResult(p *RoomPlayer, result *pb.BattleResult)
}

// RoomPlayer 房间内玩家战斗状态
type RoomPlayer struct {
	PlayerID       int64
	Name           string
	GatewayAddr    string
	X, Y           float32
	HP             int32
	Alive          bool
	StepDx         float32 // 一次移动指令的总位移（待走完的余量）
	StepDy         float32
	Stepping       bool
	LastAtkFrame   int64 // 上次普攻的帧号
	LastSkillFrame int64 // 上次技能的帧号
	Score          int64
}

// Room 对局房间（2 人）
type Room struct {
	sink roomSink

	RoomID    int64
	CreatedMs int64
	FinishMs  int64
	TickCount int64 // 逻辑帧号，Tick 前自增（与 C++ tick_ 一致）
	Finished  bool
	Started   bool
	Mode      int32 // 0=帧同步 1=状态同步

	WinnerID int64
	LoserID  int64
	Reason   string

	P1, P2 *RoomPlayer

	mu         sync.Mutex
	pendingOps []opRecord // 待消费的操作
	frameOps   []opRecord // 本帧已消费的操作
}

// opRecord 一条输入记录
type opRecord struct {
	PlayerID int64
	Op       *pb.OpInput
}

// NewRoom 创建房间。sink 负责房间对外推送/上报，通常传 *GameService，测试可传桩实现。
func NewRoom(roomID int64, p1, p2 *RoomPlayer, mode int32, sink roomSink) *Room {
	return &Room{
		RoomID:    roomID,
		CreatedMs: common.NowMs(),
		Mode:      mode,
		P1:        p1,
		P2:        p2,
		sink:      sink,
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

func (r *Room) other(p *RoomPlayer) *RoomPlayer {
	if p == r.P1 {
		return r.P2
	}
	return r.P1
}

// SubmitOp 接收玩家输入
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

// OnPlayerQuit 玩家中途退出/断线 = 对方获胜
func (r *Room) OnPlayerQuit(playerID int64) {
	r.mu.Lock()
	if r.Finished {
		r.mu.Unlock()
		return
	}
	quit := r.FindPlayer(playerID)
	if quit == nil || !quit.Alive {
		r.mu.Unlock()
		return
	}
	quit.Alive = false
	quit.HP = 0
	other := r.other(quit)
	r.mu.Unlock()
	r.finish(other.PlayerID, quit.PlayerID, "opponent_left")
}

// ApplyOp 应用一条输入（调用方需持有锁；不判死，死亡统一在 Tick 中判定）
func (r *Room) ApplyOp(p *RoomPlayer, op *pb.OpInput) {
	switch op.OpType {
	case pb.OpType_OP_MOVE:
		p.StepDx = op.MoveDx * CellW
		p.StepDy = op.MoveDy * CellH
		p.Stepping = true

	case pb.OpType_OP_ATK:
		if r.TickCount-p.LastAtkFrame < AtkCooldownFrames {
			break
		}
		p.LastAtkFrame = r.TickCount
		target := r.FindPlayer(op.TargetId)
		if target == nil || !target.Alive {
			break
		}
		if math.Sqrt(float64(DistSq(p.X, p.Y, target.X, target.Y))) > AtkRange {
			break
		}
		target.HP -= AtkDamage
		p.Score += 2
		common.Info("[room %d] atk: %d -> %d dmg=%d", r.RoomID, p.PlayerID, target.PlayerID, AtkDamage)

	case pb.OpType_OP_SKILL:
		if r.TickCount-p.LastSkillFrame < SkillCooldownFrames {
			break
		}
		p.LastSkillFrame = r.TickCount
		target := r.FindPlayer(op.TargetId)
		if target == nil || !target.Alive {
			break
		}
		if math.Sqrt(float64(DistSq(p.X, p.Y, target.X, target.Y))) > SkillRange {
			break
		}
		target.HP -= SkillDamage
		p.Score += 5
		common.Info("[room %d] skill: %d -> %d dmg=%d", r.RoomID, p.PlayerID, target.PlayerID, SkillDamage)

	default:
		break
	}
}

func DistSq(x1, y1, x2, y2 float32) float32 {
	dx := x1 - x2
	dy := y1 - y2
	return dx*dx + dy*dy
}

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// finish 结束对局并推送结算（幂等；内部自行加锁，调用时不可持有 r.mu）
func (r *Room) finish(winnerID, loserID int64, reason string) {
	r.mu.Lock()
	if r.Finished {
		r.mu.Unlock()
		return
	}
	r.Finished = true
	r.FinishMs = common.NowMs()
	r.WinnerID = winnerID
	r.LoserID = loserID
	r.Reason = reason
	common.Info("[room %d] finished reason=%s winner=%d loser=%d",
		r.RoomID, reason, winnerID, loserID)
	r.mu.Unlock()

	r.pushResult()
}

// pushResult 推送结算并上报分数（同步，同 C++ Room::Finish 行为）
func (r *Room) pushResult() {
	r.mu.Lock()
	winnerID := r.WinnerID
	loserID := r.LoserID
	reason := r.Reason
	p1 := r.P1
	p2 := r.P2
	durationS := int32((r.FinishMs - r.CreatedMs) / 1000)
	r.mu.Unlock()

	// 结算分数：胜 +10，败 +1（平局 winner=0 不结算）
	if winnerID != 0 {
		if _, err := r.sink.submitScore(winnerID, WinScore); err != nil {
			common.Warn("[room %d] 上报胜方分数失败 winner=%d: %v", r.RoomID, winnerID, err)
		}
		if _, err := r.sink.submitScore(loserID, LoseScore); err != nil {
			common.Warn("[room %d] 上报负方分数失败 loser=%d: %v", r.RoomID, loserID, err)
		}
	}

	result := &pb.BattleResult{
		RoomId:    r.RoomID,
		WinnerId:  winnerID,
		LoserId:   loserID,
		Reason:    reason,
		DurationS: durationS,
	}
	if p1.PlayerID != 0 {
		r.sink.pushResult(p1, result)
	}
	if p2.PlayerID != 0 {
		r.sink.pushResult(p2, result)
	}
}

// Tick 推进一个逻辑帧（20Hz）。与 C++ Room::Tick 逻辑完全一致：
// 消费输入 → 首帧初始化并全量广播 → 移动 → 死亡判定 → 超时判定 → 广播本帧。
func (r *Room) Tick() {
	r.mu.Lock()
	if r.Finished {
		r.mu.Unlock()
		return
	}
	r.TickCount++
	now := common.NowMs()

	// 1. 消费输入
	r.frameOps = r.frameOps[:0]
	for _, rec := range r.pendingOps {
		p := r.FindPlayer(rec.PlayerID)
		if p != nil && p.Alive {
			r.ApplyOp(p, rec.Op)
			r.frameOps = append(r.frameOps, rec)
		}
	}
	r.pendingOps = r.pendingOps[:0]

	// 2. 首帧初始化：设置出生点，广播全量帧后直接返回（本帧不模拟移动）
	if !r.Started {
		r.Started = true
		r.P1.X = SpawnOffset
		r.P1.Y = SpawnOffset
		r.P2.X = MapSize - SpawnOffset
		r.P2.Y = MapSize - SpawnOffset

		if r.Mode == 0 {
			fd := r.buildFrameDataLocked(true)
			r.mu.Unlock()
			r.sink.pushFrame(r.P1, fd)
			r.sink.pushFrame(r.P2, fd)
		} else {
			snap := r.buildSnapshotLocked(true)
			r.mu.Unlock()
			r.sink.pushSnapshot(r.P1, snap)
			r.sink.pushSnapshot(r.P2, snap)
		}
		return
	}

	// 3. 移动：本 tick 最多移动 kMoveSpeed*dt 距离，剩余距离保留到下帧继续
	dt := float32(TickMs) / 1000.0
	for _, p := range []*RoomPlayer{r.P1, r.P2} {
		if !p.Alive || !p.Stepping {
			continue
		}
		remainX := p.StepDx
		remainY := p.StepDy
		remain := float32(math.Sqrt(float64(remainX*remainX + remainY*remainY)))
		step := float32(MoveSpeed) * dt // 本 tick 可移动距离
		if step >= remain {
			p.X = clamp(p.X+remainX, 0, MapSize)
			p.Y = clamp(p.Y+remainY, 0, MapSize)
			p.Stepping = false
		} else {
			frac := step / remain
			mx := remainX * frac
			my := remainY * frac
			p.X = clamp(p.X+mx, 0, MapSize)
			p.Y = clamp(p.Y+my, 0, MapSize)
			p.StepDx -= mx
			p.StepDy -= my
		}
	}

	// 4. 死亡判定（与 C++ 一致：按 p1/p2 顺序找第一个 hp<=0 的玩家判负并返回）
	var deadPlayer, other *RoomPlayer
	for _, p := range []*RoomPlayer{r.P1, r.P2} {
		if p.Alive && p.HP <= 0 {
			deadPlayer = p
			other = r.other(p)
			break
		}
	}
	if deadPlayer != nil {
		deadPlayer.Alive = false
		deadPlayer.HP = 0
		common.Info("[room %d] kill: %d 击杀 %d", r.RoomID, other.PlayerID, deadPlayer.PlayerID)
		r.mu.Unlock()
		r.finish(other.PlayerID, deadPlayer.PlayerID, "kill")
		return
	}

	// 5. 超时判定
	if now-r.CreatedMs > TimeoutMs {
		p1HP := r.P1.HP
		p2HP := r.P2.HP
		r.mu.Unlock()
		if p1HP != p2HP {
			if p1HP > p2HP {
				r.finish(r.P1.PlayerID, r.P2.PlayerID, "timeout")
			} else {
				r.finish(r.P2.PlayerID, r.P1.PlayerID, "timeout")
			}
		} else {
			r.finish(0, 0, "draw") // 平局不结算分数
		}
		return
	}

	// 6. 广播本帧
	if r.Mode == 0 {
		fd := r.buildFrameDataLocked(false)
		r.mu.Unlock()
		r.sink.pushFrame(r.P1, fd)
		r.sink.pushFrame(r.P2, fd)
	} else {
		snap := r.buildSnapshotLocked(false)
		r.mu.Unlock()
		r.sink.pushSnapshot(r.P1, snap)
		r.sink.pushSnapshot(r.P2, snap)
	}
}

// buildFrameDataLocked 帧同步数据（调用方需持有锁）
func (r *Room) buildFrameDataLocked(full bool) *pb.FrameData {
	frame := &pb.FrameData{
		RoomId:   r.RoomID,
		FrameSeq: r.TickCount,
		Full:     full,
	}
	if full {
		// 首帧：带全量初始状态
		frame.Entities = append(frame.Entities, entityState(r.P1))
		frame.Entities = append(frame.Entities, entityState(r.P2))
	}
	// 本帧输入按 player_id 升序写入，保证确定性：
	// frame_ops_ 按操作到达顺序收集，而到达顺序受线程调度/网络延迟影响，本身不确定；
	// 移动/攻击会改变位置、血量等中间状态，操作并非可交换，顺序不同结果就不同。
	// player_id 全局唯一且两端一致，按它升序得到一个确定性全序，
	// 同一帧推给双方后客户端按相同顺序应用，两端本地模拟结果才一致（防 desync）。
	ops := make([]opRecord, len(r.frameOps))
	copy(ops, r.frameOps)
	sort.Slice(ops, func(i, j int) bool { return ops[i].PlayerID < ops[j].PlayerID })
	for _, rec := range ops {
		frame.Ops = append(frame.Ops, &pb.FrameOp{PlayerId: rec.PlayerID, Op: rec.Op})
	}
	return frame
}

// buildSnapshotLocked 状态同步快照（调用方需持有锁）
func (r *Room) buildSnapshotLocked(full bool) *pb.StateSnapshot {
	snap := &pb.StateSnapshot{
		RoomId:   r.RoomID,
		Tick:     r.TickCount,
		Full:     full,
		Entities: []*pb.EntityState{entityState(r.P1), entityState(r.P2)},
	}
	return snap
}

func entityState(p *RoomPlayer) *pb.EntityState {
	return &pb.EntityState{
		PlayerId: p.PlayerID,
		X:        p.X,
		Y:        p.Y,
		Hp:       p.HP,
		Alive:    p.Alive,
	}
}

// IsFinished 房间是否已结束
func (r *Room) IsFinished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Finished
}

// FinishTime 对局结束时间
func (r *Room) FinishTime() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.FinishMs
}
