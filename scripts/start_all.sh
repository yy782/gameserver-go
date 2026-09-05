#!/usr/bin/env bash
# Go 版一键启动：center → login → gateway → game → game-2 → rank
# 与 C++ gameserver/scripts/start_all.sh 的启动顺序一致。
# 用法: ./scripts/start_all.sh [--foreground]
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p logs

FOREGROUND=0
if [[ "${1:-}" == "--foreground" ]]; then
  FOREGROUND=1
fi

declare -a SERVICES=(
  "center:bin/center"
  "login:bin/login"
  "gateway:bin/gateway"
  "game:bin/game"
  "game-2:bin/game"
  "rank:bin/rank"
)

if ! command -v redis-cli >/dev/null 2>&1 || ! redis-cli ping 2>/dev/null | grep -q PONG; then
  echo "错误: Redis 未运行（需要 redis-cli ping 返回 PONG），先执行: redis-server --daemonize yes"
  exit 1
fi
if ! command -v mysql >/dev/null 2>&1; then
  echo "错误: 缺少 mysql 客户端"
  exit 1
fi

for entry in "${SERVICES[@]}"; do
  name="${entry%%:*}"
  bin="${entry#*:}"

  if [[ -f "logs/${name}.pid" ]] && kill -0 "$(cat "logs/${name}.pid")" 2>/dev/null; then
    echo "[跳过] ${name} 已在运行 (pid $(cat "logs/${name}.pid"))"
    continue
  fi

  if [[ ! -x "$bin" ]]; then
    echo "错误: 缺少可执行文件 $bin，请先运行: go build -o bin/ ./cmd/..."
    exit 1
  fi

  if [[ $FOREGROUND == 1 ]]; then
    echo "==> 启动 ${name}（前台）"
    "$bin" --config="config/${name}.json" &
    echo $! > "logs/${name}.pid"
  else
    echo "==> 启动 ${name}（后台，日志写入 logs/ 目录）"
    nohup "$bin" --config="config/${name}.json" >"logs/${name}.log" 2>&1 &
    echo $! > "logs/${name}.pid"
  fi

  # 中心服启动后等待其 gRPC 就绪（端口 9100，最多 5s），
  # 避免后续服务注册时中心服尚未监听导致注册失败。
  if [[ "$name" == "center" ]]; then
    for _ in $(seq 1 50); do
      if (exec 3<>/dev/tcp/127.0.0.1/9100) 2>/dev/null; then
        exec 3>&- 2>/dev/null || true
        break
      fi
      sleep 0.1
    done
  fi
done

if [[ $FOREGROUND != 1 ]]; then
  echo
  echo "全部启动完成。查看日志:"
  echo "  tail -f logs/gateway.log"
  echo "停止: ./scripts/stop_all.sh"
fi
