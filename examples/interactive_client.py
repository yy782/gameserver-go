#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""交互式手动客户端：真人可亲自操作对打（与 client_demo.py 自动演示相对）

用法:
    python3 examples/interactive_client.py                  # 默认 127.0.0.1:8000
    python3 examples/interactive_client.py --host 127.0.0.1 --port 8000

配套（对打需要 2 人）:
    终端 A: python3 examples/auto_bot.py             # 自动陪练
    终端 B: python3 examples/interactive_client.py   # 真人
    （或开两个终端都跑 interactive_client.py 真人互打）

战斗键位（即时按键，无需回车）:
    w/a/s/d  朝对应方向移动一格（按一下走一格，到达自动停）
    f        普攻最近的存活对手（射程 8，伤害 10，冷却 0.5s）
    g        技能攻击最近的存活对手（射程 15，伤害 25，冷却 2s）
    h        帮助
    q        退出战斗（返回主菜单）
    Ctrl-C   退出战斗
    支持连续输入一串（如 asdw），按顺序依次执行
"""
import argparse
import os
import select
import socket
import struct
import sys
import termios
import time
import tty

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts", "pyproto"))
import protocol_pb2  # noqa: E402

# 帧格式: 4B 总长度 | 2B msg_id | 2B flags | payload（见 src/net/protocol.cpp 对齐）
FRAME_HEADER = 8
MAX_FRAME = 65535

# msg_id（与 src/net/protocol.h 对齐）
MSG_LOGIN_REQ, MSG_LOGIN_RSP = 1, 2
MSG_MATCH_REQ, MSG_MATCH_RSP = 3, 4
MSG_OP_INPUT = 5
MSG_SNAPSHOT, MSG_RESULT = 6, 7
MSG_RANK_QUERY, MSG_RANK_RSP = 8, 9
MSG_REGISTER_REQ, MSG_REGISTER_RSP = 10, 11
MSG_FRAME_DATA = 12  # 帧同步输入帧（服务器推送，客户端本地确定性模拟）
MSG_KICK = 13  # 被挤下线通知（服务器推送，body=原因文本，随后断开）

# flags
FLAG_REQ, FLAG_RSP, FLAG_PUSH, FLAG_HEARTBEAT = 0x0001, 0x0002, 0x0004, 0x0008

# 对局模式（与 MatchReq.mode 对齐）：0=帧同步 1=状态同步
MODE_FRAMESYNC, MODE_STATESYNC = 0, 1

# 战斗参数（与 src/game/room.h 一致）
ATK_RANGE, SKILL_RANGE = 8.0, 15.0
ATK_DAMAGE, SKILL_DAMAGE = 10, 25
MOVE_SPEED = 60.0
CELL_W, CELL_H = 2.0, 4.0
TICK_S = 0.05                   # 逻辑帧 20Hz
ATK_COOLDOWN_FRAMES, SKILL_COOLDOWN_FRAMES = 10, 40  # 帧同步冷却（帧号计）
HEARTBEAT_INTERVAL = 25.0  # 秒

# 地图参数（与 src/game/room.h 的 kMapSize 对齐）
MAP_SIZE = 100.0                # 正方形战场边长
MAP_ROWS, MAP_COLS = 25, 50     # 终端字符网格（每格宽2.0 / 高4.0，保持纵横比）

# python3 examples/interactive_client.py

class Conn:
    def __init__(self, host, port):
        self.sock = socket.create_connection((host, port), timeout=5.0)
        self.sock.setblocking(False)
        self.buf = b""

    def send(self, msg_id, flags, body=b""):
        frame = struct.pack(">IHH", FRAME_HEADER + len(body), msg_id, flags) + body
        try:
            self.sock.sendall(frame)
        except OSError as e:
            kick = self._drain_kick()
            if kick is not None:
                raise ConnectionError(kick) from e
            raise ConnectionError("连接已断开: %s" % e) from e

    def _drain_kick(self):
        try:
            while True:
                data = self.sock.recv(65536)
                if not data:
                    return None  
                self.buf += data
        except BlockingIOError:
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

    def read_frames(self):
        eof = False
        try:
            while True:
                data = self.sock.recv(65536)
                if not data:
                    eof = True
                    break
                self.buf += data
        except BlockingIOError:
            pass  # 本次没有更多数据
        frames = []
        while len(self.buf) >= FRAME_HEADER:
            total = struct.unpack(">I", self.buf[:4])[0]
            if total < FRAME_HEADER or total > MAX_FRAME:
                raise ConnectionError("非法帧长度 %d" % total)
            if len(self.buf) < total:
                break
            msg_id, flags = struct.unpack(">HH", self.buf[4:8])
            body = self.buf[8:total]
            self.buf = self.buf[total:]
            if msg_id == MSG_KICK:
                reason = body.decode("utf-8", "ignore") or "你已被挤下线"
                raise ConnectionError(reason)
            frames.append((msg_id, flags, body))
        if eof and not frames:
            raise ConnectionError("连接关闭")
        return frames

    def wait_frames(self, timeout, wanted=None):
        deadline = time.time() + timeout
        while True:
            r, _, _ = select.select([self.sock], [], [], max(0.1, deadline - time.time()))
            if r:
                frames = self.read_frames()
                if wanted is None:
                    if frames:
                        return frames
                else:
                    for msg_id, flags, body in frames:
                        if msg_id == wanted:
                            return [(msg_id, flags, body)]
            if time.time() >= deadline:
                return []

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass


class ManualPlayer:

    def __init__(self, host, port, mode=MODE_FRAMESYNC):
        self.host, self.port = host, port
        self.account = ""
        self.token = ""
        self.player_id = 0
        self.name = ""
        self.room_id = 0
        self.mode = mode  # 0=帧同步 1=状态同步
        self.fs = {}
        self.opponent_id = 0
        self.frame_seq = 0  # 帧同步逻辑帧号
    def login(self, account, password="", token=""):
        conn = Conn(self.host, self.port)
        req = protocol_pb2.LoginReq()
        req.account, req.password = account, password
        if token:
            req.token = token
        conn.send(MSG_LOGIN_REQ, FLAG_REQ, req.SerializeToString())
        frames = conn.wait_frames(5.0, MSG_LOGIN_RSP)
        for msg_id, _, body in frames:
            if msg_id == MSG_LOGIN_RSP:
                rsp = protocol_pb2.LoginRsp()
                rsp.ParseFromString(body)
                if not rsp.ok:
                    conn.close()
                    raise RuntimeError("登录失败: %s" % rsp.reason)
                self.token = rsp.token
                self.player_id = rsp.player.player_id
                self.name = rsp.player.name or account
                self.account = account
                print("  登录成功: id=%d name=%s token=%s" % (self.player_id, self.name,
                                                            self.token[:16] + "..."))
                return conn
        conn.close()
        raise RuntimeError("登录响应超时")
    def register(self, account, password, name=""):
        conn = Conn(self.host, self.port)
        req = protocol_pb2.RegisterReq()
        req.account, req.password = account, password
        if name:
            req.name = name
        conn.send(MSG_REGISTER_REQ, FLAG_REQ, req.SerializeToString())
        frames = conn.wait_frames(5.0, MSG_REGISTER_RSP)
        for msg_id, _, body in frames:
            if msg_id == MSG_REGISTER_RSP:
                rsp = protocol_pb2.RegisterRsp()
                rsp.ParseFromString(body)
                if not rsp.ok:
                    conn.close()
                    raise RuntimeError("注册失败: %s" % rsp.reason)
                self.token = rsp.token
                self.player_id = rsp.player.player_id
                self.name = rsp.player.name or account
                self.account = account
                print("  注册成功，已自动登录: id=%d name=%s" % (self.player_id, self.name))
                return conn
        conn.close()
        raise RuntimeError("注册响应超时")
    def start_match(self, conn):
        req = protocol_pb2.MatchReq()
        req.token = self.token
        req.mode = self.mode  # 0=帧同步 1=状态同步
        t0 = time.time()
        print("  [debug] 发送匹配请求 t=%.1f player_id=%d mode=%d" % (t0, self.player_id,
                                                                     self.mode))
        conn.send(MSG_MATCH_REQ, FLAG_REQ, req.SerializeToString())
        deadline = time.time() + 15.0
        while True:
            r, _, _ = select.select([conn.sock], [], [], max(0.1, deadline - time.time()))
            if r:
                for msg_id, _, body in conn.read_frames():
                    if msg_id == MSG_FRAME_DATA:
                        frame = protocol_pb2.FrameData()
                        frame.ParseFromString(body)
                        if frame.full:
                            self._apply_frame(frame)  
                    elif msg_id == MSG_MATCH_RSP:
                        rsp = protocol_pb2.MatchRsp()
                        rsp.ParseFromString(body)
                        t1 = time.time()
                        print("  [debug] 收到匹配响应 t=%.1f (%.1fs) ok=%s room=%d reason=%s" %
                              (t1, t1 - t0, rsp.ok, rsp.room_id, rsp.reason))
                        if not rsp.ok:
                            raise RuntimeError("匹配失败: %s" % rsp.reason)
                        self.room_id = rsp.room_id
                        print("  匹配成功，进入房间 %d！战斗开始（按 h 查看键位）" % self.room_id)
                        return
            if time.time() >= deadline:
                break
        print("  [debug] 匹配响应 15s 超时")
        raise RuntimeError("匹配响应超时（请确认另一端有玩家在匹配）")
    def query_rank(self, conn, n=10):
        req = protocol_pb2.RankQuery()
        req.n = n
        conn.send(MSG_RANK_QUERY, FLAG_REQ, req.SerializeToString())
        frames = conn.wait_frames(5.0, MSG_RANK_RSP)
        for msg_id, _, body in frames:
            if msg_id == MSG_RANK_RSP:
                rsp = protocol_pb2.RankRsp()
                rsp.ParseFromString(body)
                print("  排行榜 Top%d:" % n)
                for i, p in enumerate(rsp.players, 1):
                    print("    %2d. %-12s (id=%d) 分数=%d" % (i, p.name or "?", p.player_id,
                                                            p.score))
                return
        raise RuntimeError("排行榜响应超时")
    def battle(self, conn):
        if self.mode == MODE_STATESYNC:
            self.fs = {}
            self.opponent_id = 0
            self.frame_seq = 0
        tty_mode = sys.stdin.isatty()
        fd = sys.stdin.fileno()
        old_attr = None
        if tty_mode:
            old_attr = termios.tcgetattr(fd)
            tty.setcbreak(fd)
            attrs = termios.tcgetattr(fd)
            attrs[3] &= ~termios.ECHO  # 关闭回显：按键不显示在屏幕上
            termios.tcsetattr(fd, termios.TCSANOW, attrs)
        self._help()
        last_hb = time.time()
        try:
            while True:
                now = time.time()
                rlist, _, _ = select.select([conn.sock, sys.stdin], [], [], 0.2)
                if conn.sock in rlist:
                    frames = conn.read_frames()
                    for msg_id, flags, body in frames:
                        if self._handle_frame(msg_id, flags, body, conn):
                            return  
                if sys.stdin in rlist:
                    if tty_mode:
                        try:
                            data = os.read(fd, 64)
                        except OSError:
                            data = b""
                    else:
                        line = sys.stdin.readline()
                        data = line.encode("utf-8", "ignore") if line else b""
                    if not data:
                        return  
                    for ch in data.decode("utf-8", "ignore").lower():
                        if ch in "wasdfghq?":
                            if self._do_cmd(ch, conn):
                                return  # 用户退出战斗
                # 心跳
                if now - last_hb > HEARTBEAT_INTERVAL:
                    conn.send(0, FLAG_HEARTBEAT)
                    last_hb = now
        except KeyboardInterrupt:
            print("\n  Ctrl-C 退出战斗")
        finally:
            if tty_mode:
                termios.tcsetattr(fd, termios.TCSADRAIN, old_attr)
            print("  已恢复行输入模式")

    def _handle_frame(self, msg_id, flags, body, conn):
        if msg_id == MSG_FRAME_DATA:
            # 帧同步：服务器只广播本帧输入，本地确定性模拟后重绘
            frame = protocol_pb2.FrameData()
            frame.ParseFromString(body)
            self._apply_frame(frame)
            self._render()
        elif msg_id == MSG_SNAPSHOT:
            snap = protocol_pb2.StateSnapshot()
            snap.ParseFromString(body)
            for e in snap.entities:
                s = self.fs.get(e.player_id)
                if s is None:
                    s = {"x": 0.0, "y": 0.0, "hp": 0, "alive": True,
                         "step_dx": 0.0, "step_dy": 0.0, "stepping": False,
                         "last_atk_frame": 0, "last_skill_frame": 0}
                    self.fs[e.player_id] = s
                s["x"], s["y"], s["hp"], s["alive"] = e.x, e.y, e.hp, e.alive
                if e.player_id != self.player_id:
                    self.opponent_id = e.player_id

            EV = protocol_pb2.BattleEvent  # 嵌套枚举：EV_ATK/EV_SKILL/EV_DEAD
            for ev in snap.events:
                if ev.type == EV.EV_ATK:
                    print("    [事件] id=%d 普攻 id=%d 伤害 %d" % (
                        ev.attacker_id, ev.target_id, ev.damage))
                elif ev.type == EV.EV_SKILL:
                    print("    [事件] id=%d 技能 id=%d 伤害 %d" % (
                        ev.attacker_id, ev.target_id, ev.damage))
                elif ev.type == EV.EV_DEAD:
                    print("    [事件] id=%d 阵亡！" % ev.target_id)
            self._render()  # 服务端事件驱动：收到快照即重绘
        elif msg_id == MSG_RESULT:
            result = protocol_pb2.BattleResult()
            result.ParseFromString(body)
            if result.winner_id == self.player_id:
                print("\n  🏆 你赢了！(%s, %ds)" % (result.reason, result.duration_s))
            elif result.winner_id == 0:
                print("\n  平局")
            else:
                print("\n  你输了 (%s, %ds)" % (result.reason, result.duration_s))
            return True  # 战斗结束
        return False

    def _apply_frame(self, frame):
        """帧同步：应用服务器广播的一帧（本地确定性模拟，规则与 src/game/room.cpp 一致）"""
        self.frame_seq = frame.frame_seq
        if frame.full:
            # 首帧：出生点全量初始状态
            self.fs = {}
            self.opponent_id = 0
            for e in frame.entities:
                self.fs[e.player_id] = {
                    "x": e.x, "y": e.y, "hp": e.hp, "alive": e.alive,
                    "step_dx": 0.0, "step_dy": 0.0, "stepping": False,
                    "last_atk_frame": 0, "last_skill_frame": 0,
                }
                if e.player_id != self.player_id:
                    self.opponent_id = e.player_id
            return
        if not self.fs:
            return  # 尚未收到首帧（防御丢帧导致状态分叉）

        # 本帧所有玩家的输入
        for fo in frame.ops:
            self._apply_fs_op(fo.player_id, fo.op)

        # 步进移动
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
                print("    [事件] id=%d 阵亡！" % pid)

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
            print("    [事件] id=%d 普攻 id=%d 伤害 %d" % (pid, op.target_id, ATK_DAMAGE))
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
            print("    [事件] id=%d 技能 id=%d 伤害 %d" % (pid, op.target_id, SKILL_DAMAGE))

    def _render(self):
        """ASCII 地图渲染：100x100 战场 → 终端网格。@=你 O=对手 x=阵亡"""
        x_step = MAP_SIZE / MAP_COLS
        y_step = MAP_SIZE / MAP_ROWS
        grid = [[" "] * MAP_COLS for _ in range(MAP_ROWS)]
        # 边框
        for c in range(MAP_COLS):
            grid[0][c] = "#"
            grid[MAP_ROWS - 1][c] = "#"
        for r in range(MAP_ROWS):
            grid[r][0] = "#"
            grid[r][MAP_COLS - 1] = "#"
        # 实体位置
        for pid, s in self.fs.items():
            cx = min(max(int(s["x"] / x_step), 1), MAP_COLS - 2)
            cy = min(max(int(s["y"] / y_step), 1), MAP_ROWS - 2)
            grid[cy][cx] = ("@" if pid == self.player_id else "O") if s["alive"] else "x"
        # 地图 + 状态
        for row in grid:
            print("  " + "".join(row))
        print("  " + "-" * MAP_COLS)
        for pid, s in self.fs.items():
            tag = "你" if pid == self.player_id else "对手"
            print("    [%s id=%d] 位置=(%.1f, %.1f) HP=%d %s" % (
                tag, pid, s["x"], s["y"], s["hp"], "存活" if s["alive"] else "已阵亡"))

    def _do_cmd(self, cmd, conn):
        if cmd in ("h", "help", "?"):
            self._help()
            return False
        if cmd == "q":
            print("  退出战斗（连接断开，对方将等待你重连或超时）")
            conn.close()
            return True
        if cmd in ("w", "a", "s", "d"):
            # 屏幕坐标系：x 向右、y 向下（w 上 = y 减小，s 下 = y 增大）；一次按一下走一格
            dirs = {"w": (0.0, -1.0), "s": (0.0, 1.0), "a": (-1.0, 0.0), "d": (1.0, 0.0)}
            dx, dy = dirs[cmd]
            self._send_op(conn, protocol_pb2.OP_MOVE, dx, dy)
            print("  移动一格 (%s)" % {"w": "上", "s": "下", "a": "左", "d": "右"}[cmd])
            return False
        if cmd in ("f", "g"):
            target = self._nearest_opponent()
            if target is None:
                print("  没有可攻击的对手")
                return False
            # 帧同步下冷却由本地模拟判定（帧号计）
            if self.mode == MODE_FRAMESYNC:
                me = self.fs.get(self.player_id)
                if me:
                    if cmd == "f" and self.frame_seq - me["last_atk_frame"] < ATK_COOLDOWN_FRAMES:
                        remain = ATK_COOLDOWN_FRAMES - (self.frame_seq - me["last_atk_frame"])
                        print("  普攻冷却中（%.1fs）" % (remain * TICK_S))
                        return False
                    if cmd == "g" and self.frame_seq - me["last_skill_frame"] < SKILL_COOLDOWN_FRAMES:
                        remain = SKILL_COOLDOWN_FRAMES - (self.frame_seq - me["last_skill_frame"])
                        print("  技能冷却中（%.1fs）" % (remain * TICK_S))
                        return False
            if cmd == "f":
                self._send_op(conn, protocol_pb2.OP_ATK, target_id=target)
                print("  普攻 id=%d" % target)
            else:
                self._send_op(conn, protocol_pb2.OP_SKILL, target_id=target, skill_id=1)
                print("  技能 id=%d" % target)
            return False
        print("  未知命令: %s（输入 h 查看帮助）" % cmd)
        return False

    def _nearest_opponent(self):
        me = self.fs.get(self.player_id)
        if not me or not me["alive"]:
            return None
        x, y = me["x"], me["y"]
        best, best_dist = None, None
        for pid, s in self.fs.items():
            if pid != self.player_id and s["alive"]:
                d = ((x - s["x"]) ** 2 + (y - s["y"]) ** 2) ** 0.5
                if best_dist is None or d < best_dist:
                    best, best_dist = pid, d
        return best

    def _send_op(self, conn, op_type, dx=0.0, dy=0.0, target_id=0, skill_id=0):
        op = protocol_pb2.OpInput()
        op.op_type = op_type
        op.move_dx, op.move_dy = dx, dy
        op.target_id = target_id
        op.skill_id = skill_id
        conn.send(MSG_OP_INPUT, FLAG_REQ, op.SerializeToString())

    def _help(self):
        print("  ── 操作键位（即时按键，无需回车）──")
        print("    w/a/s/d  立即移动一格（上/左/下/右，到达自动停）")
        print("    f        普攻最近对手（射程 8，伤害 10，冷却 0.5s）")
        print("    g        技能攻击最近对手（射程 15，伤害 25，冷却 2s）")
        print("    h        帮助")
        print("    q        退出战斗")
        print("    Ctrl-C   退出战斗")
        print("    提示：支持连续按键，一次输入一串（如 asdw）按顺序依次执行")
        mode_name = "帧同步" if self.mode == MODE_FRAMESYNC else "状态同步"
        print("    当前模式: %s（地图为本地模拟渲染）" % mode_name)
        print("  ───────────")


    def run(self):
        while True:
            print("\n===== 游戏大厅 =====")
            print("  [1] 登录")
            print("  [2] 注册新账号")
            print("  [3] 退出")
            choice = input("选择: ").strip()
            if choice == "1":
                account = input("账号: ").strip()
                password = input("密码: ").strip()
                try:
                    conn = self.login(account, password)
                    break
                except RuntimeError as e:
                    print("  [!] %s，请重试" % e)
            elif choice == "2":
                account = input("账号: ").strip()
                password = input("密码: ").strip()
                name = input("昵称（回车默认用账号）: ").strip()
                try:
                    conn = self.register(account, password, name)
                    break
                except RuntimeError as e:
                    print("  [!] %s，请重试" % e)
            elif choice == "3":
                return 0
            else:
                print("  无效选择，请输入 1/2/3")
        while True:
            print("\n===== 主菜单 =====")
            print("  已登录: id=%d name=%s" % (self.player_id, self.name))
            print("  [1] 匹配对打")
            print("  [2] 查排行榜")
            print("  [3] 断线重连（token 重新登录）")
            print("  [0] 退出")
            choice = input("选择: ").strip()
            try:
                if choice == "1":
                    self.start_match(conn)
                    self.battle(conn)
                    conn = self.login(self.account, token=self.token)
                elif choice == "2":
                    self.query_rank(conn)
                elif choice == "3":
                    conn.close()
                    conn = self.login(self.account, token=self.token)
                elif choice == "0":
                    conn.close()
                    print("再见！")
                    return 0
                else:
                    print("  无效选择")
            except (RuntimeError, ConnectionError) as e:
                print("  [!] %s" % e)
                if "被挤下线" in str(e):
                  
                    print("  已退出。同一账号不能同时登录多个客户端")
                    return 1
                try:
                    conn = self.login(self.account, token=self.token)
                except Exception as e2: 
                    print("  [!] 重连失败: %s" % e2)
                    return 1


def main():
    ap = argparse.ArgumentParser(description="交互式手动客户端")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8000)
    ap.add_argument("--mode", type=int, default=MODE_FRAMESYNC,
                    help="对局模式: 0=帧同步(默认) 1=状态同步")
    args = ap.parse_args()
    if args.mode not in (MODE_FRAMESYNC, MODE_STATESYNC):
        print("对局模式参数非法: %d（0=帧同步 1=状态同步）" % args.mode)
        return 1
    mode_name = "帧同步" if args.mode == MODE_FRAMESYNC else "状态同步"
    print("===== 游戏演示（手动模式，%s） @ %s:%d =====" % (mode_name, args.host,
                                                          args.port))
    return ManualPlayer(args.host, args.port, args.mode).run()


if __name__ == "__main__":
    sys.exit(main())
