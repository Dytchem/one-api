#!/bin/sh
set -e
# pi-bridge：agent/聊天后台会话。ONEAPI_BASE 默认本容器 one-api
export ONEAPI_BASE="${ONEAPI_BASE:-http://127.0.0.1:3000}"
export BRIDGE_PORT="${BRIDGE_PORT:-3005}"
PORT=$BRIDGE_PORT node /pi-bridge/server.js >> /tmp/pi-bridge.log 2>&1 &
# 等待 bridge 就绪
for i in $(seq 1 20); do
  if curl -sf --max-time 2 http://127.0.0.1:$BRIDGE_PORT/health >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
# one-api 前台运行（透传 CMD 参数，如 --port；SIGTERM 直传）
exec /one-api "$@"
