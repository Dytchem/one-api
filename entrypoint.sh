#!/bin/sh
set -e
# pi-bridge：agent/聊天后台会话。ONEAPI_BASE 默认本容器 one-api
# dyt-96: BRIDGE_SECRET 必须与 one-api 的 AGENT_BRIDGE_SECRET 一致，缺失时 bridge 拒绝服务
export ONEAPI_BASE="${ONEAPI_BASE:-http://127.0.0.1:3000}"
export BRIDGE_PORT="${BRIDGE_PORT:-3005}"
if [ -z "$BRIDGE_SECRET" ]; then
  echo "[entrypoint] WARNING: BRIDGE_SECRET 未设置，pi-bridge 将拒绝一切请求（请与 AGENT_BRIDGE_SECRET 保持一致）" >&2
fi
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
