package game

// Room 战斗规则单元测试：覆盖出生点、步进移动、攻击/技能范围与冷却、
// 死亡结算、超时判定、玩家退出、帧操作确定性排序等纯逻辑，
// 通过 stub 实现 roomSink，无需真实 gRPC / Redis 依赖。

import (
	"context"
	"math"
	"testing"

	"gameserver/api/pb"
	"gameserver/internal/common"
)

// stubSink 内存版 roomSink：记录推送给房间的帧/快照/结算与上报的分数。
type stubSink struct {
	scores   map[int64]int32     // playerID -> 累计分数
	frames   []*pb.FrameData     // pushFrame 收到的帧（同一帧每玩家推一次）
	snaps    []*pb.StateSnapshot // pushSnapshot 收到的快照
	results  []*pb.BattleResult  // 对局结果
	scoreErr error               // 可选：让 submitScore 报错
}

func newStubSink() *stubSink {
	return &stubSink{scores: make(map[int64]int32)}
}

func (s *stubSink) submitScore(playerID int64, score int32) (int32, error) {
	if s.scoreErr != nil {
		return -1, s.scoreErr
	}
	s.scores[playerID] += score
	return s.scores[playerID], nil
}

func (s *stubSink) pushFrame(_ *RoomPlayer, frame *pb.FrameData) { s.frames = append(s.frames, frame) }
func (s *stubSink) pushSnapshot(_ *RoomPlayer, snap *pb.StateSnapshot) {
	s.snaps = append(s.snaps, snap)
}
func (s *stubSink) pushResult(_ *RoomPlayer, result *pb.BattleResult) {
	s.results = append(s.results, result)
}

// newTestRoom 创建标准测试房间：玩家 1/2，满血存活，mode 可选。
func newTestRoom(mode int32) (*Room, *RoomPlayer, *RoomPlayer, *stubSink) {
	p1 := &RoomPlayer{PlayerID: 1, Name: "p1", HP: MaxHp, Alive: true}
	p2 := &RoomPlayer{PlayerID: 2, Name: "p2", HP: MaxHp, Alive: true}
	sink := newStubSink()
	return NewRoom(1001, p1, p2, mode, sink), p1, p2, sink
}

func approxEq(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

func opMove(dx, dy float32) *pb.OpInput {
	return &pb.OpInput{OpType: pb.OpType_OP_MOVE, MoveDx: dx, MoveDy: dy}
}

func opAtk(targetID int64) *pb.OpInput {
	return &pb.OpInput{OpType: pb.OpType_OP_ATK, TargetId: targetID}
}

func opSkill(targetID int64) *pb.OpInput {
	return &pb.OpInput{OpType: pb.OpType_OP_SKILL, TargetId: targetID, SkillId: 1}
}

// 把两名玩家放到同一位置，保证在普攻/技能射程内（距离 0）。
func putTogether(p1, p2 *RoomPlayer) {
	p1.X, p1.Y = 50, 50
	p2.X, p2.Y = 50, 50
}

// 首个 Tick：完成出生点初始化并广播全量帧/快照，本帧不做移动模拟。
func TestRoomFirstTickSpawns(t *testing.T) {
	for _, mode := range []int32{0, 1} {
		room, p1, p2, sink := newTestRoom(mode)
		room.Tick()
		if !room.Started || room.TickCount != 1 {
			t.Fatalf("mode=%d: 首帧后 Started=%v TickCount=%d", mode, room.Started, room.TickCount)
		}
		if !approxEq(p1.X, SpawnOffset) || !approxEq(p1.Y, SpawnOffset) {
			t.Fatalf("mode=%d: p1 出生点=(%.1f,%.1f), want (%.1f,%.1f)",
				mode, p1.X, p1.Y, SpawnOffset, SpawnOffset)
		}
		if !approxEq(p2.X, MapSize-SpawnOffset) || !approxEq(p2.Y, MapSize-SpawnOffset) {
			t.Fatalf("mode=%d: p2 出生点=(%.1f,%.1f), want (%.1f,%.1f)",
				mode, p2.X, p2.Y, MapSize-SpawnOffset, MapSize-SpawnOffset)
		}
		if room.Finished {
			t.Fatalf("mode=%d: 首帧不应结束", mode)
		}
		if mode == 0 {
			if len(sink.frames) != 2 { // 每个玩家各收到一次全量帧
				t.Fatalf("mode=0: 首帧应广播 2 次, got %d", len(sink.frames))
			}
			fd := sink.frames[len(sink.frames)-1]
			if !fd.Full || len(fd.Entities) != 2 || fd.FrameSeq != 1 {
				t.Fatalf("mode=0: 全量帧异常 full=%v entities=%d seq=%d", fd.Full, len(fd.Entities), fd.FrameSeq)
			}
		} else {
			if len(sink.snaps) != 2 {
				t.Fatalf("mode=1: 首帧应广播 2 次快照, got %d", len(sink.snaps))
			}
			if !sink.snaps[0].Full || len(sink.snaps[0].Entities) != 2 {
				t.Fatalf("mode=1: 全量快照异常 full=%v", sink.snaps[0].Full)
			}
		}
	}
}

// 水平一格位移 2.0：单 tick 的移动量(3.0)足够走完，落点精确 +2。
func TestRoomMoveHorizontalOneCell(t *testing.T) {
	room, p1, _, sink := newTestRoom(0)
	room.Tick() // 出生帧
	startX := p1.X
	room.SubmitOp(p1.PlayerID, opMove(1.0, 0.0))
	room.Tick()
	if !approxEq(p1.X, startX+CellW) {
		t.Fatalf("水平移动一格后 X=%.1f, want %.1f", p1.X, startX+CellW)
	}
	if p1.Stepping {
		t.Fatalf("走完一格后应停止 stepping")
	}
	if room.TickCount != 2 || len(sink.frames) != 4 { // 出生1次 + 本帧1次，各推两人
		t.Fatalf("TickCount=%d frames=%d", room.TickCount, len(sink.frames))
	}
}

// 垂直一格位移 4.0 超过单 tick 移动量 3.0，需要两个 tick 分帧完成。
func TestRoomMoveVerticalTakesTwoTicks(t *testing.T) {
	room, p1, _, _ := newTestRoom(0)
	room.Tick()                                   // 出生帧
	room.SubmitOp(p1.PlayerID, opMove(0.0, -1.0)) // 向上走一格 = -4.0
	room.Tick()
	if !approxEq(p1.Y, SpawnOffset-3.0) {
		t.Fatalf("第1 tick 后 Y=%.1f, want %.1f", p1.Y, SpawnOffset-3.0)
	}
	if !p1.Stepping || !approxEq(p1.StepDy, -1.0) {
		t.Fatalf("剩余位移未结转: stepping=%v StepDy=%.1f", p1.Stepping, p1.StepDy)
	}
	room.Tick()
	if !approxEq(p1.Y, SpawnOffset-CellH) {
		t.Fatalf("第2 tick 后 Y=%.1f, want %.1f", p1.Y, SpawnOffset-CellH)
	}
	if p1.Stepping {
		t.Fatalf("走完一格后应停止 stepping")
	}
}

// 位移越出地图边界时坐标应 clamp 到 [0, MapSize]。
func TestRoomMoveClampedAtBoundary(t *testing.T) {
	room, p1, _, _ := newTestRoom(0)
	room.Tick()
	p1.X, p1.Y = MapSize-1, 1 // 白盒摆位：x 接近右边界，y 接近上边界
	room.SubmitOp(p1.PlayerID, opMove(1.0, -1.0))
	room.Tick()
	if !approxEq(p1.X, MapSize) {
		t.Fatalf("x 应 clamp 到 %.1f, got %.1f", MapSize, p1.X)
	}
	if !approxEq(p1.Y, 0) {
		t.Fatalf("y 应 clamp 到 0, got %.1f", p1.Y)
	}
}

// 普攻：射程外无效；射程内扣血并进入帧冷却（AtkCooldownFrames 帧内重复攻击无效）。
func TestRoomAttackRangeAndCooldown(t *testing.T) {
	room, p1, p2, _ := newTestRoom(0)
	room.Tick()
	// 帧冷却按帧号记录（LastAtkFrame=0 起步），要尽早发起攻击需把最近攻击帧拨到过去
	p1.LastAtkFrame = -AtkCooldownFrames

	// 距离 10：在技能射程内(15)、普攻射程外(8)
	p1.X, p1.Y = 40, 50
	p2.X, p2.Y = 50, 50
	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID))
	room.Tick()
	if p2.HP != MaxHp {
		t.Fatalf("射程外普攻不应命中, HP=%d", p2.HP)
	}

	// 注：ApplyOp 在范围判定前即记录冷却帧，故此处无需断言 LastAtkFrame。
	putTogether(p1, p2)
	p1.LastAtkFrame = -AtkCooldownFrames           // 拨回冷却，确保下一次命中帧可执行
	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID)) // 本帧命中，冷却开始
	room.Tick()
	if p2.HP != MaxHp-AtkDamage || p1.Score != 2 {
		t.Fatalf("命中后 HP=%d Score=%d, want HP=%d Score=2", p2.HP, p1.Score, MaxHp-AtkDamage)
	}
	lastAtkFrame := p1.LastAtkFrame
	hitHP := p2.HP

	// 冷却期内每帧尝试攻击都不生效
	for i := 0; i < AtkCooldownFrames-1; i++ {
		room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID))
		room.Tick()
	}
	if p1.LastAtkFrame != lastAtkFrame {
		t.Fatalf("冷却期内不应允许再攻击")
	}
	if p2.HP != hitHP {
		t.Fatalf("冷却期内 HP 不应再降, HP=%d", p2.HP)
	}

	// 冷却期结束后的下一帧可以再次命中
	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID))
	room.Tick()
	if p1.LastAtkFrame == lastAtkFrame || p2.HP != MaxHp-2*AtkDamage {
		t.Fatalf("冷却结束后应能再次命中, HP=%d", p2.HP)
	}
}

// 技能：距离 10 时普攻打不到、技能可以；伤害为 SkillDamage 且计 5 分。
func TestRoomSkillRangeAndDamage(t *testing.T) {
	room, p1, p2, _ := newTestRoom(0)
	room.Tick()
	p1.LastAtkFrame, p1.LastSkillFrame = -AtkCooldownFrames, -SkillCooldownFrames
	p1.X, p1.Y = 40, 50
	p2.X, p2.Y = 50, 50

	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID)) // 普攻射程外：无效
	room.Tick()
	if p2.HP != MaxHp {
		t.Fatalf("普攻在射程外不应命中, HP=%d", p2.HP)
	}
	room.SubmitOp(p1.PlayerID, opSkill(p2.PlayerID)) // 技能射程内：命中
	room.Tick()
	if p2.HP != MaxHp-SkillDamage {
		t.Fatalf("技能命中后 HP=%d, want %d", p2.HP, MaxHp-SkillDamage)
	}
	if p1.Score != 5 {
		t.Fatalf("技能命中应计 5 分, got %d", p1.Score)
	}
	lastSkillFrame := p1.LastSkillFrame

	// 技能冷却未到前不能再次释放
	room.SubmitOp(p1.PlayerID, opSkill(p2.PlayerID))
	room.Tick()
	if p2.HP != MaxHp-SkillDamage {
		t.Fatalf("技能冷却期内不应重复命中, HP=%d", p2.HP)
	}
	if p1.LastSkillFrame != lastSkillFrame {
		t.Fatalf("冷却期内不应更新技能冷却帧")
	}
}

// 攻击已阵亡目标 / 非法目标应被忽略。
func TestRoomAttackDeadTargetIgnored(t *testing.T) {
	room, p1, p2, _ := newTestRoom(0)
	room.Tick()
	p1.LastAtkFrame = -AtkCooldownFrames
	putTogether(p1, p2)
	p2.Alive = false
	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID))
	room.Tick()
	if p2.HP != MaxHp {
		t.Fatalf("不应命中已阵亡目标")
	}
}

// 击杀：HP 归零后下一帧死亡判定触发结算（reason=kill），
// 双方收到结果、胜 +WinScore / 负 +LoseScore，且结算幂等。
func TestRoomKillFinishesBattle(t *testing.T) {
	room, p1, p2, sink := newTestRoom(0)
	room.Tick()
	p1.LastAtkFrame = -AtkCooldownFrames
	putTogether(p1, p2)
	p2.HP = AtkDamage - 1 // 一击即死

	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID))
	room.Tick()
	if !room.Finished {
		t.Fatal("击杀后房间应结束")
	}
	if room.WinnerID != p1.PlayerID || room.LoserID != p2.PlayerID || room.Reason != "kill" {
		t.Fatalf("结算异常 winner=%d loser=%d reason=%q", room.WinnerID, room.LoserID, room.Reason)
	}
	if p2.Alive || p2.HP != 0 {
		t.Fatalf("阵亡方应置为死亡且 HP=0, alive=%v hp=%d", p2.Alive, p2.HP)
	}
	if len(sink.results) != 2 {
		t.Fatalf("双方应各收到一次结果, got %d", len(sink.results))
	}
	if sink.scores[p1.PlayerID] != WinScore || sink.scores[p2.PlayerID] != LoseScore {
		t.Fatalf("分数结算异常: p1=%d want %d, p2=%d want %d",
			sink.scores[p1.PlayerID], WinScore, sink.scores[p2.PlayerID], LoseScore)
	}

	// 已结束房间：再次提交输入 / 再次退出都不再触发新结算
	room.SubmitOp(p2.PlayerID, opMove(1, 0))
	room.Tick()
	room.OnPlayerQuit(p1.PlayerID)
	if len(sink.results) != 2 {
		t.Fatalf("结算应幂等, results=%d", len(sink.results))
	}
}

// 玩家中途退出：对方直接获胜（reason=opponent_left），已结束/已死亡玩家退出无效。
func TestRoomQuitLetsOpponentWin(t *testing.T) {
	room, p1, p2, sink := newTestRoom(0)
	room.Tick()

	room.OnPlayerQuit(p2.PlayerID)
	if !room.Finished || room.WinnerID != p1.PlayerID || room.Reason != "opponent_left" {
		t.Fatalf("退出后结算异常 finished=%v winner=%d reason=%q",
			room.Finished, room.WinnerID, room.Reason)
	}
	if sink.scores[p1.PlayerID] != WinScore || sink.scores[p2.PlayerID] != LoseScore {
		t.Fatalf("退出结算分数异常")
	}

	// 重复退出同一房间：幂等
	room.OnPlayerQuit(p1.PlayerID)
	if len(sink.results) != 2 {
		t.Fatalf("重复退出不应重复结算")
	}

	// 已死亡玩家退出不改变结算
	room2, _, p2b, sink2 := newTestRoom(0)
	room2.Tick()
	p2b.Alive = false
	room2.OnPlayerQuit(p2b.PlayerID)
	if room2.Finished {
		t.Fatal("已死亡玩家退出不应触发结算")
	}
	if len(sink2.results) != 0 {
		t.Fatalf("不应有结算结果")
	}
}

// 超时判定：血量高者胜（reason=timeout）。
func TestRoomTimeoutHigherHpWins(t *testing.T) {
	room, p1, p2, sink := newTestRoom(0)
	room.CreatedMs = common.NowMs() - TimeoutMs - 1 // 制造超时
	room.Tick()                                     // 出生帧不判超时
	p1.HP, p2.HP = MaxHp/2, MaxHp                   // p2 血更多
	room.Tick()
	if !room.Finished || room.WinnerID != p2.PlayerID || room.Reason != "timeout" {
		t.Fatalf("超时结算异常 winner=%d reason=%q finished=%v", room.WinnerID, room.Reason, room.Finished)
	}
	if sink.scores[p2.PlayerID] != WinScore || sink.scores[p1.PlayerID] != LoseScore {
		t.Fatalf("超时结算分数异常")
	}
}

// 超时且血量相同 → 平局（winner=0），不结算分数。
func TestRoomTimeoutDraw(t *testing.T) {
	room, _, _, sink := newTestRoom(0)
	room.CreatedMs = common.NowMs() - TimeoutMs - 1
	room.Tick()
	room.Tick()
	if !room.Finished || room.WinnerID != 0 || room.Reason != "draw" {
		t.Fatalf("平局结算异常 winner=%d reason=%q finished=%v", room.WinnerID, room.Reason, room.Finished)
	}
	if len(sink.results) != 2 || sink.results[0].WinnerId != 0 {
		t.Fatalf("平局结果推送异常: %+v", sink.results)
	}
	if len(sink.scores) != 0 {
		t.Fatalf("平局不应上报分数, scores=%v", sink.scores)
	}
}

// 同帧多玩家操作广播时按 player_id 升序排列，保证两端确定性模拟一致。
func TestRoomFrameOpsSortedByPlayerID(t *testing.T) {
	room, p1, p2, sink := newTestRoom(0)
	room.Tick()
	putTogether(p1, p2)
	// 故意乱序提交：p2 的移动指令先到、p1 的攻击后到
	room.SubmitOp(p2.PlayerID, opMove(-1, 0))
	room.SubmitOp(p1.PlayerID, opAtk(p2.PlayerID))
	room.Tick()

	fd := sink.frames[len(sink.frames)-1] // 每帧推两人，取最后一帧
	if len(fd.Ops) != 2 {
		t.Fatalf("帧内应有 2 条操作, got %d", len(fd.Ops))
	}
	if fd.Ops[0].PlayerId != p1.PlayerID || fd.Ops[1].PlayerId != p2.PlayerID {
		t.Fatalf("帧操作未按 player_id 升序: [%d,%d]", fd.Ops[0].PlayerId, fd.Ops[1].PlayerId)
	}
}

// 状态同步模式：每 tick 推送服务端权威快照。
func TestRoomStateSyncSnapshotPush(t *testing.T) {
	room, p1, _, sink := newTestRoom(1)
	room.Tick() // 出生快照(full)
	room.SubmitOp(p1.PlayerID, opMove(1, 0))
	room.Tick()
	if len(sink.snaps) != 4 { // 两帧 × 两人
		t.Fatalf("快照推送次数=%d, want 4", len(sink.snaps))
	}
	snap := sink.snaps[len(sink.snaps)-1]
	if snap.Full || snap.Tick != 2 || len(snap.Entities) != 2 {
		t.Fatalf("增量快照异常 full=%v tick=%d entities=%d", snap.Full, snap.Tick, len(snap.Entities))
	}
	if !approxEq(snap.Entities[0].X, SpawnOffset+CellW) {
		t.Fatalf("快照实体位置未更新: X=%.1f", snap.Entities[0].X)
	}
}

// FindPlayer / 未知玩家操作 / 未知房间操作请求的边界。
func TestRoomFindPlayerAndUnknownInput(t *testing.T) {
	room, p1, _, _ := newTestRoom(0)
	room.Tick()
	if room.FindPlayer(p1.PlayerID) != p1 || room.FindPlayer(9999) != nil {
		t.Fatal("FindPlayer 行为异常")
	}
	// 未知玩家提交操作：忽略且不 panic
	room.SubmitOp(9999, opMove(1, 0))
	room.Tick()
	if !approxEq(p1.X, SpawnOffset) {
		t.Fatal("未知玩家操作不应影响房间")
	}
}

// GameService.SubmitOp / QuitRoom 通过房间路由转发到 Room。
func TestGameServiceOpRouting(t *testing.T) {
	gs := NewGameService(nil, nil, nil, "", "test")
	room, p1, _, _ := newTestRoom(0)
	gs.rooms[room.RoomID] = room

	ctx := context.Background()
	// 房间不存在
	rsp, err := gs.SubmitOp(ctx, &pb.OpForwardReq{RoomId: 4242, PlayerId: p1.PlayerID, Op: opMove(1, 0)})
	if err != nil || rsp.Ok {
		t.Fatalf("房间不存在时应返回 ok=false, got ok=%v err=%v", rsp.Ok, err)
	}
	// 房间存在
	rsp, err = gs.SubmitOp(ctx, &pb.OpForwardReq{RoomId: room.RoomID, PlayerId: p1.PlayerID, Op: opMove(1, 0)})
	if err != nil || !rsp.Ok {
		t.Fatalf("操作转发应成功, got ok=%v err=%v", rsp.Ok, err)
	}
	// 玩家退出路由到 Room.OnPlayerQuit
	if _, err := gs.QuitRoom(ctx, &pb.QuitRoomReq{RoomId: room.RoomID, PlayerId: 2}); err != nil {
		t.Fatalf("QuitRoom err=%v", err)
	}
	if !room.Finished || room.WinnerID != p1.PlayerID {
		t.Fatalf("退出未按预期结算 finished=%v winner=%d", room.Finished, room.WinnerID)
	}
}
