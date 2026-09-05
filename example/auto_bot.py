#!/usr/bin/env python3
"""
自动机器人客户端 - 自动对战演示
"""

import socket
import struct
import time
import argparse
from google.protobuf import message
import sys
import os

# 添加proto生成文件路径
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'api', 'pb'))

# 导入protobuf消息（需要从C++项目生成的pb2文件）
# from protocol_pb2 import LoginReq, LoginRsp, MatchReq, MatchRsp
# from common_pb2 import PlayerBase

class GameClient:
    """游戏客户端"""

    def __init__(self, host='127.0.0.1', port=8000):
        self.host = host
        self.port = port
        self.socket = None
        self.player_id = None
        self.token = None
        self.room_id = None

    def connect(self):
        """连接到服务器"""
        self.socket = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.socket.connect((self.host, self.port))
        print(f"✅ 已连接到 {self.host}:{self.port}")

    def disconnect(self):
        """断开连接"""
        if self.socket:
            self.socket.close()
            print("❌ 已断开连接")

    def send_message(self, msg_id, msg_data):
        """发送消息"""
        # 消息格式: [4B 长度] [2B msg_id] [2B flags] [protobuf payload]
        flags = 0x0001  # Request
        body = msg_data
        frame_len = 8 + len(body)

        frame = struct.pack('>I', frame_len)  # 4B 长度
        frame += struct.pack('>HH', msg_id, flags)  # 2B msg_id + 2B flags
        frame += body

        self.socket.send(frame)

    def recv_message(self):
        """接收消息"""
        # 先读帧头
        header = self.socket.recv(4)
        if not header:
            return None, None, None

        frame_len = struct.unpack('>I', header)[0]

        # 读消息ID和flags
        header2 = self.socket.recv(4)
        msg_id, flags = struct.unpack('>HH', header2)

        # 读payload
        body_len = frame_len - 8
        body = self.socket.recv(body_len)

        return msg_id, flags, body

    def login(self, account, password):
        """登录"""
        print(f"🔑 正在登录账号: {account}")
        # 这里需要序列化LoginReq protobuf消息
        # 暂时使用占位符
        time.sleep(0.5)
        self.player_id = hash(account) % 1000000
        self.token = f"token_{self.player_id}"
        print(f"✅ 登录成功! Player ID: {self.player_id}")

    def match(self, mode=0):
        """匹配"""
        print(f"🎮 正在匹配... (模式: {mode})")
        time.sleep(1)
        self.room_id = int(time.time() * 1000) % 1000000
        print(f"✅ 匹配成功! Room ID: {self.room_id}")

    def play_battle(self, duration=10):
        """对战"""
        print(f"⚔️  开始对战 ({duration}秒)")
        start_time = time.time()

        while time.time() - start_time < duration:
            # 模拟游戏操作
            time.sleep(0.1)

        print(f"🏁 对战结束!")

    def run_auto_bot(self, num_battles=1, mode=0):
        """运行自动机器人"""
        self.connect()

        try:
            account = f"bot_{int(time.time() * 1000) % 10000}"
            self.login(account, "password123")

            for i in range(num_battles):
                print(f"\n--- 对战 {i+1}/{num_battles} ---")
                self.match(mode)
                self.play_battle(duration=5)

            print("\n✅ 全部对战完成!")

        finally:
            self.disconnect()


def main():
    parser = argparse.ArgumentParser(description='游戏自动机器人')
    parser.add_argument('--host', default='127.0.0.1', help='服务器地址')
    parser.add_argument('--port', type=int, default=8000, help='服务器端口')
    parser.add_argument('--bots', type=int, default=1, help='机器人数量')
    parser.add_argument('--mode', type=int, default=0, help='对战模式 (0=帧同步, 1=状态同步)')
    parser.add_argument('--battles', type=int, default=3, help='每个机器人的对战数')

    args = parser.parse_args()

    print("🤖 游戏自动机器人客户端")
    print(f"服务器: {args.host}:{args.port}")
    print(f"机器人数: {args.bots}")
    print(f"模式: {'帧同步' if args.mode == 0 else '状态同步'}")
    print(f"每个机器人对战数: {args.battles}\n")

    for bot_id in range(args.bots):
        print(f"\n🤖 机器人 {bot_id+1}/{args.bots}")
        client = GameClient(args.host, args.port)
        client.run_auto_bot(num_battles=args.battles, mode=args.mode)
        time.sleep(0.5)

    print("\n✅ 所有机器人完成!")


if __name__ == '__main__':
    main()