#!/usr/bin/env bash
# Go 版一键停止：结束 logs/*.pid 记录的进程
set -euo pipefail
# ./scripts/stop_all.sh
cd "$(dirname "$0")/.."

for pidfile in logs/*.pid; do
  [[ -f "$pidfile" ]] || continue
  name="$(basename "$pidfile" .pid)"
  pid="$(cat "$pidfile")"
  if kill -0 "$pid" 2>/dev/null; then
    echo "==> 停止 ${name} (pid ${pid})"
    kill "$pid"
    # 最多等 3s 退出
    for _ in $(seq 1 30); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$pidfile"
done

echo "全部已停止。"
