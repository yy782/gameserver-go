#!/bin/bash

# 启动脚本

set -e

# 检查 Redis 连接
echo "Checking Redis..."
redis-cli ping > /dev/null 2>&1 || {
    echo "Redis not available!"
    exit 1
}

# 检查 MySQL 连接
echo "Checking MySQL..."
mysql -h 127.0.0.1 -u root -e "SELECT 1" > /dev/null 2>&1 || {
    echo "MySQL not available!"
    exit 1
}

# 获取脚本所在目录
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$SCRIPT_DIR"

# 确保二进制文件存在
if [ ! -f "bin/center" ]; then
    echo "Binaries not found. Building..."
    bash build.sh
fi

echo "Starting gameserver cluster..."

# 启动各个服务，每个在后台
./bin/center -config config/center.json &
CENTER_PID=$!
echo "center started (PID: $CENTER_PID)"

sleep 1

./bin/login -config config/login.json &
LOGIN_PID=$!
echo "login started (PID: $LOGIN_PID)"

sleep 1

./bin/gateway -config config/gateway.json &
GATEWAY_PID=$!
echo "gateway started (PID: $GATEWAY_PID)"

sleep 1

./bin/game -config config/game.json &
GAME1_PID=$!
echo "game-1 started (PID: $GAME1_PID)"

./bin/game -config config/game-2.json &
GAME2_PID=$!
echo "game-2 started (PID: $GAME2_PID)"

sleep 1

./bin/rank -config config/rank.json &
RANK_PID=$!
echo "rank started (PID: $RANK_PID)"

echo ""
echo "All services started!"
echo "Center: $CENTER_PID"
echo "Login: $LOGIN_PID"
echo "Gateway: $GATEWAY_PID"
echo "Game-1: $GAME1_PID"
echo "Game-2: $GAME2_PID"
echo "Rank: $RANK_PID"
echo ""
echo "Press Ctrl+C to stop all services..."

# 捕获 SIGINT，停止所有服务
trap "kill $CENTER_PID $LOGIN_PID $GATEWAY_PID $GAME1_PID $GAME2_PID $RANK_PID" INT

# 等待信号
wait