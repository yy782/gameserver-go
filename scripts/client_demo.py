#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""游戏服演示客户端：模拟 N 人对战 + 查排行榜（见 DESIGN.md 9）

用法:
    python3 scripts/client_demo.py            # 默认 2 个机器人对打
    python3 scripts/client_demo.py --bots=4   # 4 个机器人
    python3 scripts/client_demo.py --reconnect# 演示断线重连（首局断线后 token 续玩）

流程: 登录(注册即建档) -> 匹配 -> 对打(默认帧同步，--mode 1 切状态同步) -> 对局结果 -> 查排行
"""
import argparse
import os
import socket
import struct
import sys
import threading
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "pyproto"))
import protocol_pb2  # noqa: E402
import common_pb2  # noqa: E402

# 帧格式: 4B 总长度 | 2B msg_id | 2B flags | payload（与 src/net/protocol.cpp 对齐）
FRAME_HEADER = 8
MAX_FRAME = 65535

# msg_id（与 src/net/protocol.h 对齐）
MSG_LOGIN_REQ, MSG_LOGIN_RSP = 1, 2
MSG_MATCH_REQ, MSG_MATCH_RSP = 3, 4
MSG_OP_INPUT = 5
MSG_SNAPSHOT, MSG_RESULT = 6, 7
MSG_RANK_QUERY, MSG_RANK_RSP = 8, 9
MSG_REGISTER_REQ, MSG_REGISTER_RSP = 10, 11
MSG_FRAME_DATA = 12  # 帧同步输入帧（服务器推送，客户端本地模拟）
MSG_KICK = 13  # 被挤下线通知（服务器推送，body=原因文本，随后断开）

# flags
FLAG_REQ, FLAG_RSP, FLAG_PUSH, FLAG_HEARTBEAT = 0x0001, 0x0002, 0x0004, 0x0008

# 对局模式（与 MatchReq.mode 对齐）：0=帧同步 1=状态同步
MODE_FRAMESYNC, MODE_STATESYNC = 0, 1

# 战斗参数（与 src/game/room.h 一致）
MAP_SIZE, MOVE_SPEED = 100.0, 60.0
ATK_RANGE, SKILL_RANGE = 8.0, 15.0
ATK_DAMAGE, SKILL_DAMAGE = 10, 25
CELL_W, CELL_H = 2.0, 4.0
TICK_S = 0.05  # 逻辑帧 20Hz
ATK_COOLDOWN, SKILL_COOLDOWN = 0.5, 2.0  # 秒（状态同步 AI 决策用）
ATK_COOLDOWN_FRAMES, SKILL_COOLDOWN_FRAMES = 10, 40  # 帧（帧同步冷却，20Hz）
HEARTBEAT_INTERVAL = 25.0  # 秒


class Conn:
    """TCP 帧编解码封装"""

    def __init__(self, host, port):
        self.sock = socket.create_connection((host, port), timeout=5.0)
        self.buf = b""
        self.sock.settimeout(0.1)

    def send(self, msg_id, flags, body=b""):
        frame = struct.pack(">IHH", FRAME_HEADER + len(body), msg_id, flags) + body
        try:
            self.sock.sendall(frame)
        except OSError as e:
            # 被顶号/断网时 send 会失败。被顶号时服务器已先推 Kick 帧，
            # send 失败前先读一次接收缓冲，给出明确原因而非笼统"连接断开"。
            kick = self._drain_kick()
            if kick is not None:
                raise ConnectionError(kick) from e
            raise ConnectionError("连接已断开: %s" % e) from e

    def _drain_kick(self):
        """非阻塞读一次：若缓冲/网络中已有 Kick 帧返回其原因文本，否则 None"""
        try:
            while True:
                data = self.sock.recv(65536)
                if not data:
                    return None  # 连接已关闭且无 Kick（普通断网）
                self.buf += data
        except socket.timeout:
            pass
        while len(self.buf) >= FRAME_HEADER:
            total = struct.unpack(">I", self.buf[:4])[0]
            if total < FRAME_HEADER or total > MAX_FRAME:
                return None
            if len(self.buf) < total:
                break
            msg_id, flags = struct.unpack(">HH", self.buf[4:8])
            body = self.buf[8:total]
            self.buf = self.buf[total:]
            if msg_id == MSG_KICK:
                return body.decode("utf-8", "ignore") or "你已被挤下线"
        return None

    def recv(self):
        """返回 (msg_id, flags, body)，无数据返回 None，断开抛异常"""
        while True:
            if len(self.buf) >= FRAME_HEADER:
                total = struct.unpack(">I", self.buf[:4])[0]
                if total < FRAME_HEADER or total > MAX_FRAME:
                    raise ConnectionError("非法帧长度 %d" % total)
                if len(self.buf) >= total:
                    msg_id, flags = struct.unpack(">HH", self.buf[4:8])
                    body = self.buf[8:total]
                    self.buf = self.buf[total:]
                    if msg_id == MSG_KICK:
                        # 被挤下线：服务器随后断开连接，给出明确原因而非"连接关闭"
                        reason = body.decode("utf-8", "ignore") or "你已被挤下线"
                        raise ConnectionError(reason)
                    return msg_id, flags, body
            try:
                data = self.sock.recv(65536)
            except socket.timeout:
                return None  # 本次无数据（调用方决定是否继续等待）
            if not data:
                raise ConnectionError("连接关闭")
            self.buf += data

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass


class Bot:
    def __init__(self, index, host, port, reconnect=False, mode=MODE_FRAMESYNC):
        self.index = index
        self.host, self.port = host, port
        self.account = "bot%d" % index
        self.password = "pass123"
        self.reconnect = reconnect
        self.mode = mode  # 0=帧同步 1=状态同步
        self.player_id = 0
        self.token = ""
        self.room_id = 0

        # 状态同步战斗世界（服务器快照驱动）
        self.me = None  # player_id
        self.entities = {}  # player_id -> (x, y, hp, alive)
        self.last_atk = 0.0
        self.last_skill = 0.0

        # 帧同步本地世界（服务器输入帧驱动，规则与 src/game/room.cpp 一致）
        self.frame_seq = 0
        self.fs = {}  # player_id -> dict(x,y,hp,alive,step_dx,step_dy,stepping,last_atk_frame,last_skill_frame)

    # ---------- 基础操作 ----------
    def login(self, token=""):
        conn = Conn(self.host, self.port)
        req = protocol_pb2.LoginReq()
        req.account, req.password = self.account, self.password
        if token:
            req.token = token
        conn.send(MSG_LOGIN_REQ, FLAG_REQ, req.SerializeToString())
        deadline = time.time() + 5.0
        while True:
            r = conn.recv()
            if r is None:
                if time.time() > deadline:
                    raise RuntimeError("登录响应超时")
                continue
            msg_id, _, body = r
            if msg_id == MSG_LOGIN_RSP:
                rsp = protocol_pb2.LoginRsp()
                rsp.ParseFromString(body)
                if not rsp.ok:
                    conn.close()
                    if rsp.reason == "账号不存在":
                        # 注册/登录分离：首次运行需先注册，注册成功自动登录
                        return self.register()
                    raise RuntimeError("登录失败: %s" % rsp.reason)
                self.token = rsp.token
                self.player_id = rsp.player.player_id
                return conn

    def register(self):
        conn = Conn(self.host, self.port)
        req = protocol_pb2.RegisterReq()
        req.account, req.password = self.account, self.password
        conn.send(MSG_REGISTER_REQ, FLAG_REQ, req.SerializeToString())
        deadline = time.time() + 5.0
        while True:
            r = conn.recv()
            if r is None:
                if time.time() > deadline:
                    raise RuntimeError("注册响应超时")
                continue
            msg_id, _, body = r
            if msg_id == MSG_REGISTER_RSP:
                rsp = protocol_pb2.RegisterRsp()
                rsp.ParseFromString(body)
                if not rsp.ok:
                    conn.close()
                    raise RuntimeError("注册失败: %s" % rsp.reason)
                self.token = rsp.token
                self.player_id = rsp.player.player_id
                print("[bot%d] 注册成功: id=%d name=%s" % (self.index, self.player_id,
                                                          self.account))
                return conn

    def match(self, conn):
        req = protocol_pb2.MatchReq()
        req.token = self.token
        req.mode = self.mode  # 0=帧同步 1=状态同步
        conn.send(MSG_MATCH_REQ, FLAG_REQ, req.SerializeToString())
        deadline = time.time() + 15.0
        while True:
            r = conn.recv()
            if r is None:
                if time.time() > deadline:
                    raise RuntimeError("匹配响应超时")
                continue
            msg_id, _, body = r
            if msg_id == MSG_FRAME_DATA:
                # 帧同步首帧可能在匹配应答前到达（服务器配对成功即广播），
                # 必须在等待应答期间就初始化本地世界，否则后续帧无从应用。
                frame = protocol_pb2.FrameData()
                frame.ParseFromString(body)
                if frame.full:
                    self._apply_frame(conn, frame)
                continue
            if msg_id == MSG_MATCH_RSP:
                rsp = protocol_pb2.MatchRsp()
                rsp.ParseFromString(body)
                if not rsp.ok:
                    raise RuntimeError("匹配失败: %s" % rsp.reason)
                self.room_id = rsp.room_id
                print("[bot%d] 匹配成功，进入房间 %d" % (self.index, self.room_id))
                return

    # ---------- 战斗 ----------
    def battle(self, conn):
        """收快照 -> AI 决策发操作，直到收到对局结果"""
        last_heartbeat = time.time()
        while True:
            r = conn.recv()
            now = time.time()
            if r is None:
                # 心跳（30s 内无数据则踢线，25s 主动保活）
                if now - last_heartbeat > HEARTBEAT_INTERVAL:
                    conn.send(0, FLAG_HEARTBEAT)
                    last_heartbeat = now
                continue
            msg_id, flags, body = r

            if msg_id == MSG_SNAPSHOT:
                snap = protocol_pb2.StateSnapshot()
                snap.ParseFromString(body)
                for e in snap.entities:
                    self.entities[e.player_id] = (e.x, e.y, e.hp, e.alive)
                # 事件展示
                EV = protocol_pb2.BattleEvent  # 嵌套枚举：EV_ATK/EV_SKILL/EV_DEAD
                for ev in snap.events:
                    if ev.type != EV.EV_NONE:
                        print("[bot%d] 事件: %s dmg=%d" % (
                            self.index,
                            "普攻" if ev.type == EV.EV_ATK else "技能",
                            ev.damage))
                self._ai_send(conn)

            elif msg_id == MSG_RESULT:
                result = protocol_pb2.BattleResult()
                result.ParseFromString(body)
                if result.winner_id == self.player_id:
                    print("[bot%d] 对局胜利! (%s, %ds)" % (self.index, result.reason,
                                                          result.duration_s))
                elif result.winner_id == 0:
                    print("[bot%d] 平局" % self.index)
                else:
                    print("[bot%d] 对局失败 (%s, %ds)" % (self.index, result.reason,
                                                        result.duration_s))
                return

            # 心跳（30s 内无数据则踢线，25s 主动保活）
            if now - last_heartbeat > HEARTBEAT_INTERVAL:
                conn.send(0, FLAG_HEARTBEAT)
                last_heartbeat = now

    def _ai_send(self, conn):
        """极简 AI：朝对手移动，够近就普攻/技能"""
        me = self.entities.get(self.player_id)
        if not me:
            return
        x, y, hp, alive = me
        if not alive:
            return
        opponent = None
        for pid, (ox, oy, ohp, oalive) in self.entities.items():
            if pid != self.player_id and oalive:
                opponent = (pid, ox, oy, ohp)
                break
        if not opponent:
            return
        pid, ox, oy, ohp = opponent
        dist = ((x - ox) ** 2 + (y - oy) ** 2) ** 0.5

        op = protocol_pb2.OpInput()
        now = time.time()

        if dist <= ATK_RANGE:
            if now - self.last_atk >= ATK_COOLDOWN:
                self.last_atk = now
                op.op_type = protocol_pb2.OP_ATK
                op.target_id = pid
            else:
                return  # 冷却中不动
        elif dist <= SKILL_RANGE and now - self.last_skill >= SKILL_COOLDOWN:
            self.last_skill = now
            op.op_type = protocol_pb2.OP_SKILL
            op.target_id = pid
            op.skill_id = 1
        else:
            # 朝对手移动（归一化方向）
            dx, dy = (ox - x) / dist, (oy - y) / dist
            op.op_type = protocol_pb2.OP_MOVE
            op.move_dx, op.move_dy = dx, dy

        conn.send(MSG_OP_INPUT, FLAG_REQ, op.SerializeToString())

    # ---------- 帧同步战斗（本地确定性模拟） ----------
    def battle_fs(self, conn):
        """帧同步：收服务器广播的输入帧 -> 本地模拟 -> AI 决策发操作，直到对局结果"""
        last_heartbeat = time.time()
        while True:
            r = conn.recv()
            now = time.time()
            if r is None:
                if now - last_heartbeat > HEARTBEAT_INTERVAL:
                    conn.send(0, FLAG_HEARTBEAT)
                    last_heartbeat = now
                continue
            msg_id, _, body = r

            if msg_id == MSG_FRAME_DATA:
                frame = protocol_pb2.FrameData()
                frame.ParseFromString(body)
                self._apply_frame(conn, frame)

            elif msg_id == MSG_RESULT:
                result = protocol_pb2.BattleResult()
                result.ParseFromString(body)
                self._print_result(result)
                return

            if now - last_heartbeat > HEARTBEAT_INTERVAL:
                conn.send(0, FLAG_HEARTBEAT)
                last_heartbeat = now

    def _battle_once(self, conn):
        if self.mode == MODE_FRAMESYNC:
            self.battle_fs(conn)
        else:
            self.battle(conn)

    def _print_result(self, result):
        if result.winner_id == self.player_id:
            print("[bot%d] 对局胜利! (%s, %ds)" % (self.index, result.reason,
                                                  result.duration_s))
        elif result.winner_id == 0:
            print("[bot%d] 平局" % self.index)
        else:
            print("[bot%d] 对局失败 (%s, %ds)" % (self.index, result.reason,
                                                result.duration_s))

    def _apply_frame(self, conn, frame):
        """应用一帧：首帧重建世界；常规帧先应用输入，再推进移动/死亡判定"""
        self.frame_seq = frame.frame_seq
        if frame.full:
            # 首帧：出生点全量初始状态
            self.fs = {}
            for e in frame.entities:
                self.fs[e.player_id] = {
                    "x": e.x, "y": e.y, "hp": e.hp, "alive": e.alive,
                    "step_dx": 0.0, "step_dy": 0.0, "stepping": False,
                    "last_atk_frame": 0, "last_skill_frame": 0,
                }
            print("[bot%d] 帧同步开局: room=%d frame=%d" % (
                self.index, frame.room_id, frame.frame_seq))
            return

        if not self.fs:
            return  # 尚未收到首帧（匹配阶段应已初始化；防御丢帧导致状态分叉）

        # 本帧所有玩家的输入（含自己的——服务器广播统一应用，两端状态一致）
        for fo in frame.ops:
            self._apply_fs_op(fo.player_id, fo.op)

        # 步进移动（与 room.cpp 一致：一次按键走一格，到达自动停止）
        for s in self.fs.values():
            if not s["alive"] or not s["stepping"]:
                continue
            remain_x, remain_y = s["step_dx"], s["step_dy"]
            remain = (remain_x * remain_x + remain_y * remain_y) ** 0.5
            step = MOVE_SPEED * TICK_S  # 60 * 0.05 = 3.0
            if step >= remain:
                s["x"] = min(max(s["x"] + remain_x, 0.0), MAP_SIZE)
                s["y"] = min(max(s["y"] + remain_y, 0.0), MAP_SIZE)
                s["stepping"] = False
            else:
                frac = step / remain
                mx, my = remain_x * frac, remain_y * frac
                s["x"] = min(max(s["x"] + mx, 0.0), MAP_SIZE)
                s["y"] = min(max(s["y"] + my, 0.0), MAP_SIZE)
                s["step_dx"] -= mx
                s["step_dy"] -= my

        # 死亡判定（本地表现；结算以服务器权威 BattleResult 为准）
        for pid, s in self.fs.items():
            if s["alive"] and s["hp"] <= 0:
                s["alive"] = False
                s["hp"] = 0
                print("[bot%d] 本地模拟: %d 阵亡" % (self.index, pid))

        # AI 决策并发送本帧输入
        self._fs_ai(conn)

    def _apply_fs_op(self, pid, op):
        """确定性规则：与 src/game/room.cpp ApplyOp 保持一致（冷却按帧号）"""
        s = self.fs.get(pid)
        if not s or not s["alive"]:
            return
        t = op.op_type
        if t == protocol_pb2.OP_MOVE:
            s["step_dx"] = op.move_dx * CELL_W
            s["step_dy"] = op.move_dy * CELL_H
            s["stepping"] = True
        elif t == protocol_pb2.OP_ATK:
            if self.frame_seq - s["last_atk_frame"] < ATK_COOLDOWN_FRAMES:
                return
            s["last_atk_frame"] = self.frame_seq
            tg = self.fs.get(op.target_id)
            if not tg or not tg["alive"]:
                return
            dist = ((s["x"] - tg["x"]) ** 2 + (s["y"] - tg["y"]) ** 2) ** 0.5
            if dist > ATK_RANGE:
                return
            tg["hp"] -= ATK_DAMAGE
            print("[bot%d] 帧同步: 普攻 %d -> %d dmg=%d" % (
                self.index, pid, op.target_id, ATK_DAMAGE))
        elif t == protocol_pb2.OP_SKILL:
            if self.frame_seq - s["last_skill_frame"] < SKILL_COOLDOWN_FRAMES:
                return
            s["last_skill_frame"] = self.frame_seq
            tg = self.fs.get(op.target_id)
            if not tg or not tg["alive"]:
                return
            dist = ((s["x"] - tg["x"]) ** 2 + (s["y"] - tg["y"]) ** 2) ** 0.5
            if dist > SKILL_RANGE:
                return
            tg["hp"] -= SKILL_DAMAGE
            print("[bot%d] 帧同步: 技能 %d -> %d dmg=%d" % (
                self.index, pid, op.target_id, SKILL_DAMAGE))

    def _fs_ai(self, conn):
        """帧同步 AI：基于本地模拟状态决策，冷却按帧号"""
        me = self.fs.get(self.player_id)
        if not me or not me["alive"]:
            return
        opp = None
        for pid, s in self.fs.items():
            if pid != self.player_id and s["alive"]:
                opp = (pid, s)
                break
        if not opp:
            return
        opp_pid, os_ = opp
        dist = ((me["x"] - os_["x"]) ** 2 + (me["y"] - os_["y"]) ** 2) ** 0.5

        op = protocol_pb2.OpInput()
        if dist <= ATK_RANGE:
            if self.frame_seq - me["last_atk_frame"] >= ATK_COOLDOWN_FRAMES:
                op.op_type = protocol_pb2.OP_ATK
                op.target_id = opp_pid
            else:
                return  # 冷却中不动
        elif dist <= SKILL_RANGE and self.frame_seq - me["last_skill_frame"] >= SKILL_COOLDOWN_FRAMES:
            op.op_type = protocol_pb2.OP_SKILL
            op.target_id = opp_pid
            op.skill_id = 1
        else:
            dx, dy = (os_["x"] - me["x"]) / dist, (os_["y"] - me["y"]) / dist
            op.op_type = protocol_pb2.OP_MOVE
            op.move_dx, op.move_dy = dx, dy

        conn.send(MSG_OP_INPUT, FLAG_REQ, op.SerializeToString())

    # ---------- 排行榜 ----------
    def query_rank(self, conn, n=10):
        req = protocol_pb2.RankQuery()
        req.n = n
        conn.send(MSG_RANK_QUERY, FLAG_REQ, req.SerializeToString())
        deadline = time.time() + 5.0
        while True:
            r = conn.recv()
            if r is None:
                if time.time() > deadline:
                    raise RuntimeError("排行榜响应超时")
                continue
            msg_id, _, body = r
            if msg_id == MSG_RANK_RSP:
                rsp = protocol_pb2.RankRsp()
                rsp.ParseFromString(body)
                print("[bot%d] 排行榜 Top%d:" % (self.index, n))
                for i, p in enumerate(rsp.players, 1):
                    print("  %2d. %-12s (id=%d) 分数=%d" % (i, p.name or "?", p.player_id,
                                                          p.score))
                return

    # ---------- 主流程 ----------
    def run(self):
        try:
            conn = self.login()
            mode_name = "帧同步" if self.mode == MODE_FRAMESYNC else "状态同步"
            print("[bot%d] 登录成功: id=%d name=%s (%s)" % (
                self.index, self.player_id, self.account, mode_name))
            self.match(conn)
            self._battle_once(conn)
            conn.close()

            # 断线重连演示：用 token 重新登录再打一局
            if self.reconnect:
                print("[bot%d] 模拟断线重连..." % self.index)
                conn = self.login(token=self.token)
                self.match(conn)
                self._battle_once(conn)
                conn.close()

            # 查排行榜
            conn = self.login(token=self.token)
            self.query_rank(conn)
            conn.close()
        except Exception as e:  # noqa: BLE001
            print("[bot%d] 异常: %s" % (self.index, e))


def main():
    ap = argparse.ArgumentParser(description="游戏服演示客户端")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8000)
    ap.add_argument("--bots", type=int, default=2, help="机器人数量(2~4)")
    ap.add_argument("--reconnect", action="store_true", help="演示断线重连")
    ap.add_argument("--mode", type=int, default=MODE_FRAMESYNC,
                    help="对局模式: 0=帧同步(默认) 1=状态同步")
    args = ap.parse_args()

    if not 2 <= args.bots <= 4:
        print("机器人数量需在 2~4 之间")
        return 1
    if args.mode not in (MODE_FRAMESYNC, MODE_STATESYNC):
        print("对局模式参数非法: %d（0=帧同步 1=状态同步）" % args.mode)
        return 1

    mode_name = "帧同步" if args.mode == MODE_FRAMESYNC else "状态同步"
    print("===== 游戏服演示开始: %d 个机器人 @ %s:%d（%s）=====" % (
        args.bots, args.host, args.port, mode_name))
    threads = []
    for i in range(1, args.bots + 1):
        t = threading.Thread(target=Bot(i, args.host, args.port, args.reconnect,
                                        args.mode).run,
                             daemon=True)
        t.start()
        threads.append(t)
        time.sleep(0.3)  # 错开登录/匹配，保证成对

    for t in threads:
        t.join()
    print("===== 演示结束 =====")
    return 0


if __name__ == "__main__":
    sys.exit(main())
