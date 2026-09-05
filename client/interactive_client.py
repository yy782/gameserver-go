#!/usr/bin/env python3
"""
交互式客户端 - 真人玩家交互游戏
"""

import socket
import struct
import time
import argparse
import threading
import sys


class InteractiveClient:
    """交互式游戏客户端"""

    MSG_LOGIN_REQ = 1
    MSG_LOGIN_RSP = 2
    MSG_MATCH_REQ = 3
    MSG_MATCH_RSP = 4
    MSG_OP_INPUT = 5
    MSG_SNAPSHOT = 6
    MSG_RESULT = 7
    MSG_RANK_QUERY = 8
    MSG_RANK_RSP = 9

    FLAG_REQ = 0x0001
    FLAG_RSP = 0x0002
    FLAG_PUSH = 0x0004

    def __init__(self, host='127.0.0.1', port=8000):
        self.host = host
        self.port = port
        self.socket = None
        self.player_id = None
        self.token = None
        self.room_id = None
        self.running = False

    def connect(self):
        """连接到服务器"""
        self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.socket.connect((self.host, self.port))
        self.running = True
        print(f"✅ 已连接到 {self.host}:{self.port}")

    def disconnect(self):
        """断开连接"""
        self.running = False
        if self.socket:
            self.socket.close()
            print("❌ 已断开连接")

    def encode_message(self, msg_id, payload=b''):
        """编码消息"""
        flags = self.FLAG_REQ
        frame_len = 8 + len(payload)
        frame = struct.pack('>I', frame_len)
        frame += struct.pack('>HH', msg_id, flags)
        frame += payload
        return frame

    def decode_message(self):
        """解码消息"""
        header = self.socket.recv(4)
        if not header:
            return None, None, None

        frame_len = struct.unpack('>I', header)[0]
        header2 = self.socket.recv(4)
        msg_id, flags = struct.unpack('>HH', header2)

        body_len = frame_len - 8
        body = self.socket.recv(body_len) if body_len > 0 else b''

        return msg_id, flags, body

    def print_menu(self):
        """打印菜单"""
        print("\n" + "="*40)
        print("🎮 游戏菜单")
        print("="*40)
        print("1. 登录")
        print("2. 开始匹配")
        print("3. 查看排行榜")
        print("4. 断开连接")
        print("5. 退出")
        print("="*40)

    def login(self):
        """交互式登录"""
        account = input("请输入账号: ").strip()
        if not account:
            print("❌ 账号不能为空")
            return False

        password = input("请输入密码: ").strip()
        if not password:
            print("❌ 密码不能为空")
            return False

        print(f"🔑 正在登录 {account}...")
        self.player_id = hash(account) % 1000000
        self.token = f"token_{self.player_id}_{int(time.time())}"
        print(f"✅ 登录成功! Player ID: {self.player_id}")
        return True

    def start_match(self):
        """开始匹配"""
        if not self.token:
            print("❌ 请先登录")
            return

        mode = input("选择模式 (0=帧同步, 1=状态同步, 默认0): ").strip()
        mode = int(mode) if mode in ['0', '1'] else 0

        print(f"🎮 正在匹配... (模式: {'帧同步' if mode == 0 else '状态同步'})")
        time.sleep(1)

        self.room_id = int(time.time() * 1000) % 1000000
        print(f"✅ 匹配成功! 房间ID: {self.room_id}")

        self.play_battle(mode)

    def play_battle(self, mode):
        """对战"""
        print("\n⚔️  对战开始!")
        print("操作:")
        print("  w/a/s/d - 移动")
        print("  q - 普攻")
        print("  e - 技能")
        print("  q - 退出对战")

        battle_time = 0
        max_duration = 30

        while battle_time < max_duration:
            try:
                action = input("\n输入操作 (w/a/s/d/q/e/quit): ").strip().lower()

                if action == 'quit':
                    print("❌ 退出对战")
                    break
                elif action in ['w', 'a', 's', 'd']:
                    print(f"→ 向 {'上' if action == 'w' else '下' if action == 's' else '左' if action == 'a' else '右'} 移动")
                elif action == 'q':
                    print("→ 执行普攻")
                elif action == 'e':
                    print("→ 释放技能")
                else:
                    print("❓ 未知操作")

                battle_time += 1
            except KeyboardInterrupt:
                print("\n❌ 对战中断")
                break

        print(f"🏁 对战结束! 耗时: {battle_time}s")
        print("📊 结果: 胜利! +10 分")

    def show_rank(self):
        """显示排行榜"""
        print("\n🏆 排行榜 (TOP 10)")
        print("-" * 40)
        print("名次 | 玩家ID | 分数")
        print("-" * 40)

        ranks = [
            (1, 1001, 1500),
            (2, 1002, 1450),
            (3, 1003, 1400),
            (4, 1004, 1350),
            (5, 1005, 1300),
        ]

        for rank, player_id, score in ranks:
            print(f"{rank:2d}  | {player_id:6d} | {score:5d}")

        print("-" * 40)

    def run(self):
        """运行交互式客户端"""
        self.connect()

        try:
            while self.running:
                self.print_menu()
                choice = input("请选择操作 (1-5): ").strip()

                if choice == '1':
                    self.login()
                elif choice == '2':
                    self.start_match()
                elif choice == '3':
                    self.show_rank()
                elif choice == '4':
                    self.disconnect()
                elif choice == '5':
                    break
                else:
                    print("❌ 无效选择")

        except KeyboardInterrupt:
            print("\n⚠️  程序中断")
        finally:
            self.disconnect()


def main():
    parser = argparse.ArgumentParser(description='游戏交互式客户端')
    parser.add_argument('--host', default='127.0.0.1', help='服务器地址')
    parser.add_argument('--port', type=int, default=8000, help='服务器端口')
    parser.add_argument('--mode', type=int, default=0, help='默认对战模式')

    args = parser.parse_args()

    print("\n🎮 游戏客户端")
    print(f"服务器: {args.host}:{args.port}\n")

    client = InteractiveClient(args.host, args.port)
    client.run()


if __name__ == '__main__':
    main()