#!/bin/sh
set -e
# pi-bridge：agent/聊天后台会话。ONEAPI_BASE 默认本容器 one-api
export ONEAPI_BASE="${ONEAPI_BASE:-http://127.0.0.1:3000}"
export BRIDGE_PORT="${BRIDGE_PORT:-3005}"

# dyt-105: bridge 鉴权改为"默认开启"。
# 原实现：BRIDGE_SECRET 未配置时 bridge 以兼容模式运行（requireAuth 直接 return true），
# 而 bridge 的 /chat 等接口信任调用方自报的 user_id —— 同机/同网络任何进程都能
# 以管理员身份驱动 Agent 工具（/api/channel、/api/user、/api/option），属提权面。
# 现改为：未显式配置时，由 entrypoint 生成随机密钥并同时注入 bridge 与 one-api，
# 使鉴权始终生效而无需任何配置；显式配置的值优先（用于跨容器/跨机部署，两侧必须一致）。
# dyt-107: 生成的密钥持久化到 /data（与 session_secret 同一挂载卷），
# 否则每次容器重启都会换新密钥——bridge 与 one-api 若分别重启（或超时不一致），
# 两侧密钥就desync，Chat/Agent 会以 401/404 静默失效。
BRIDGE_SECRET_FILE="/data/bridge_secret"
if [ -z "$BRIDGE_SECRET" ]; then
  if [ -s "$BRIDGE_SECRET_FILE" ]; then
    BRIDGE_SECRET="$(cat "$BRIDGE_SECRET_FILE" 2>/dev/null || true)"
  fi
  if [ -z "$BRIDGE_SECRET" ]; then
    if [ -r /proc/sys/kernel/random/uuid ]; then
      BRIDGE_SECRET="$(cat /proc/sys/kernel/random/uuid)"
    else
      BRIDGE_SECRET="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    fi
    # 尽力持久化；不可写则退回每次重启生成（不影响本次鉴权生效）
    if mkdir -p "$(dirname "$BRIDGE_SECRET_FILE")" 2>/dev/null; then
      (umask 077 && printf '%s' "$BRIDGE_SECRET" > "$BRIDGE_SECRET_FILE") 2>/dev/null || true
    fi
    echo "[entrypoint] BRIDGE_SECRET 未配置，已生成随机密钥并持久化到 $BRIDGE_SECRET_FILE（bridge 鉴权已启用，无需人工配置）" >&2
  else
    echo "[entrypoint] BRIDGE_SECRET 未配置，复用已持久化密钥 $BRIDGE_SECRET_FILE（bridge 鉴权已启用）" >&2
  fi
  export BRIDGE_SECRET
  export AGENT_BRIDGE_SECRET="$BRIDGE_SECRET"
else
  # 显式配置时保持一致：one-api 侧读 AGENT_BRIDGE_SECRET
  export AGENT_BRIDGE_SECRET="${AGENT_BRIDGE_SECRET:-$BRIDGE_SECRET}"
fi

# dyt-105: bridge 日志写容器内可持久化路径并同时输出到 stderr，
# 原 /tmp 不挂卷且无输出，bridge 挂掉时外部完全无信号。
BRIDGE_LOG="${BRIDGE_LOG:-/data/pi-bridge.log}"
mkdir -p "$(dirname "$BRIDGE_LOG")" 2>/dev/null || BRIDGE_LOG=/tmp/pi-bridge.log
PORT=$BRIDGE_PORT node /pi-bridge/server.js >>"$BRIDGE_LOG" 2>&1 &

# 等待 bridge 就绪（dyt-105: 原实现 20 次循环后无条件继续，bridge 启动失败时
# 外部只会看到 502，没有任何失败信号。现在失败则打印日志并非零退出）
BRIDGE_OK=0
for i in $(seq 1 40); do
  if curl -sf --max-time 2 http://127.0.0.1:$BRIDGE_PORT/health >/dev/null 2>&1; then
    BRIDGE_OK=1
    break
  fi
  sleep 0.5
done
if [ "$BRIDGE_OK" != "1" ]; then
  echo "[entrypoint] ERROR: pi-bridge 未在 20s 内就绪（端口 $BRIDGE_PORT）。日志尾部：" >&2
  tail -n 40 "$BRIDGE_LOG" 2>/dev/null >&2 || true
  echo "[entrypoint] one-api 将继续启动（Chat/Agent 功能不可用）" >&2
fi

# one-api 前台运行（透传 CMD 参数，如 --port；SIGTERM 直传）
exec /one-api "$@"
