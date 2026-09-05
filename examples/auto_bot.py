#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""自动陪练 bot：真人 vs 机器人（步进移动版）

用法:
    python3 examples/auto_bot.py                  # 默认 127.0.0.1:8000
    python3 examples/auto_bot.py --host 127.0.0.1 --port 8000 --account bot_ai

配套（真人 vs 自动陪练）:
    终端 A: python3 examples/auto_bot.py             # 自动陪练
    终端 B: python3 examples/interactive_client.py   # 真人（登录后选 1 匹配）

流程: 登录(账号不存在自动注册) -> 匹配(失败自动重试) -> 战斗 -> 打完自动等下一局对手。

AI 说明: 服务器 OP_MOVE 语义为"按一下走一格"（水平一格 2.0 / 垂直一格 4.0，到达自动停），
因此 bot 不再发归一化持续移动方向，而是沿差距更大的主轴发 ±1 方向、走完一格再走下一格；
进入射程后按冷却普攻/技能。
"""
import argparse
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts"))
from client_demo import (  # noqa: E402
    Bot,
    protocol_pb2,
    ATK_RANGE,
    SKILL_RANGE,
    ATK_COOLDOWN,
    SKILL_COOLDOWN,
    ATK_COOLDOWN_FRAMES,
    SKILL_COOLDOWN_FRAMES,
    MODE_FRAMESYNC,
    MSG_OP_INPUT,
    FLAG_REQ,
)


class AutoBot(Bot):
    """真人陪练 bot：复用 client_demo.Bot 的登录/匹配/战斗协议封装，AI 适配步进移动。

    帧同步（默认）与状态同步（--mode 1）都支持：AI 逻辑一致（步进移动 + 冷却），
    区别仅在战斗循环（_battle_once 分流）与读取的世界表示（self.fs / self.entities）。
    """

    MOVE_INTERVAL = 0.15  # 相邻两格移动指令的最短间隔（s）：垂直一格 4.0/60 ≈ 67ms，留余量

    def __init__(self, host="127.0.0.1", port=8000, account="",
                 mode=MODE_FRAMESYNC):
        # 账号留空时自动生成唯一账号（pid+时间戳），避免多开时撞用默认账号
        # 互相挤线（gateway 同账号登录会踢掉旧连接）。
        if not account:
            account = "bot_ai_%d_%d" % (os.getpid(), int(time.time()))
        super().__init__(9, host, port, mode=mode)  # index 仅用于日志前缀
        self.account = account
        self.last_move = 0.0

    def _ai_send(self, conn):
        """步进版 AI：朝对手一格一格走，进入射程就普攻/技能"""
        me = self.entities.get(self.player_id)
        if not me or not me[3]:
            return
        x, y = me[0], me[1]
        opponent = None
        for pid, (ox, oy, _, oalive) in self.entities.items():
            if pid != self.player_id and oalive:
                opponent = (pid, ox, oy)
                break
        if not opponent:
            return
        pid, ox, oy = opponent
        dx, dy = ox - x, oy - y
        dist = (dx * dx + dy * dy) ** 0.5
        now = time.time()

        op = protocol_pb2.OpInput()
        if dist <= ATK_RANGE:
            if now - self.last_atk >= ATK_COOLDOWN:
                self.last_atk = now
                op.op_type = protocol_pb2.OP_ATK
                op.target_id = pid
            else:
                return  # 普攻冷却中：已贴脸，站桩等冷却
        elif dist <= SKILL_RANGE and now - self.last_skill >= SKILL_COOLDOWN:
            self.last_skill = now
            op.op_type = protocol_pb2.OP_SKILL
            op.target_id = pid
            op.skill_id = 1
        else:
            # 步进移动：沿差距更大的轴向对手走一格（方向 ±1）
            if now - self.last_move < self.MOVE_INTERVAL:
                return  # 上一格尚未走完，等落地再发下一格
            self.last_move = now
            op.op_type = protocol_pb2.OP_MOVE
            if abs(dx) >= abs(dy):
                op.move_dx = 1.0 if dx > 0 else -1.0
            else:
                op.move_dy = 1.0 if dy > 0 else -1.0
        conn.send(MSG_OP_INPUT, FLAG_REQ, op.SerializeToString())

    def _fs_ai(self, conn):
        """帧同步 AI：基于本地模拟状态（self.fs）决策，步进移动 + 帧号冷却。

        与 client_demo.Bot._fs_ai 的差别：覆盖为步进移动（一格一格走），
        与状态同步 _ai_send 的行为保持一致，避免归一化连续方向在"按一下走一格"
        语义下走得过慢。
        """
        me = self.fs.get(self.player_id)
        if not me or not me["alive"]:
            return
        x, y = me["x"], me["y"]
        opp = None
        for pid, s in self.fs.items():
            if pid != self.player_id and s["alive"]:
                opp = (pid, s["x"], s["y"])
                break
        if not opp:
            return
        pid, ox, oy = opp
        dx, dy = ox - x, oy - y
        dist = (dx * dx + dy * dy) ** 0.5
        now = time.time()

        op = protocol_pb2.OpInput()
        if dist <= ATK_RANGE:
            if self.frame_seq - me["last_atk_frame"] >= ATK_COOLDOWN_FRAMES:
                op.op_type = protocol_pb2.OP_ATK
                op.target_id = pid
            else:
                return  # 普攻冷却中：已贴脸，站桩等冷却
        elif dist <= SKILL_RANGE and self.frame_seq - me["last_skill_frame"] >= SKILL_COOLDOWN_FRAMES:
            self.last_skill = now
            op.op_type = protocol_pb2.OP_SKILL
            op.target_id = pid
            op.skill_id = 1
        else:
            # 步进移动：沿差距更大的轴向对手走一格（方向 ±1）
            if now - self.last_move < self.MOVE_INTERVAL:
                return  # 上一格尚未走完，等落地再发下一格
            self.last_move = now
            op.op_type = protocol_pb2.OP_MOVE
            if abs(dx) >= abs(dy):
                op.move_dx = 1.0 if dx > 0 else -1.0
            else:
                op.move_dy = 1.0 if dy > 0 else -1.0
        conn.send(MSG_OP_INPUT, FLAG_REQ, op.SerializeToString())

    def run(self):
        """陪练循环：登录 -> 匹配(失败重试) -> 战斗(按模式分流) -> 打完自动等下一局对手"""
        try:
            while True:
                conn = self.login()
                print("[bot] 登录成功: id=%d name=%s" % (self.player_id, self.account))
                # 一直挂匹配：真人随时选 1 都能配上；失败/超时稍等重试
                while True:
                    try:
                        self.match(conn)
                        break
                    except RuntimeError as e:
                        print("[bot] %s，2s 后重新匹配" % e)
                        time.sleep(2.0)
                self._battle_once(conn)  # 按 self.mode 分流：帧同步 battle_fs / 状态同步 battle
                conn.close()
                self.entities = {}
                self.fs = {}
                self.frame_seq = 0
                print("[bot] 本局结束，等待下一局对手...")
                time.sleep(1.0)
        except KeyboardInterrupt:
            print("[bot] 已退出（Ctrl-C）")
        except Exception as e:  # noqa: BLE001
            print("[bot] 异常退出: %s" % e)


def main():
    ap = argparse.ArgumentParser(description="自动陪练 bot（真人 vs 机器人，步进移动）")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8000)
    ap.add_argument("--account", default="", help="bot 账号（默认自动生成唯一账号，多开不冲突）")
    ap.add_argument("--mode", type=int, default=MODE_FRAMESYNC,
                    help="对局模式: 0=帧同步(默认) 1=状态同步")
    args = ap.parse_args()
    if args.mode not in (MODE_FRAMESYNC, 1):
        print("对局模式参数非法: %d（0=帧同步 1=状态同步）" % args.mode)
        return 1
    print("===== 自动陪练 bot @ %s:%d =====" % (args.host, args.port))
    AutoBot(host=args.host, port=args.port, account=args.account,
            mode=args.mode).run()
    return 0


if __name__ == "__main__":
    sys.exit(main())
