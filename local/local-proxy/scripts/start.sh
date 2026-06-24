#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_PROXY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT="$(cd "$LOCAL_PROXY_DIR/../.." && pwd)"

BASE_ROOT="${LAZYMIND_LOCAL_PROXY_BASE_ROOT:-./data/local-proxy}"
case "$BASE_ROOT" in
  /*) BASE_ROOT_ABS="$BASE_ROOT" ;;
  *) BASE_ROOT_ABS="$ROOT/$BASE_ROOT" ;;
esac

export LAZYMIND_FRONTEND_PORT="${LAZYMIND_FRONTEND_PORT:-8090}"
export LAZYMIND_LOCAL_PROXY_ADDRESS="${LAZYMIND_LOCAL_PROXY_ADDRESS:-0.0.0.0}"
export LAZYMIND_LOCAL_PROXY_PORT="${LAZYMIND_LOCAL_PROXY_PORT:-5024}"
export LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT="${LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT:-18000}"
export LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT="${LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT:-18001}"
export LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT="${LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT:-18046}"
export LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT="${LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT:-18080}"
export LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT="${LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT:-18047}"

BIN_PATH="${LAZYMIND_LOCAL_PROXY_BIN:-$BASE_ROOT_ABS/bin/local-proxy}"
CONFIG_PATH="${LAZYMIND_LOCAL_PROXY_CONFIG:-$LOCAL_PROXY_DIR/configs/cloud-replace-kong.yaml}"
PID_FILE="${LAZYMIND_LOCAL_PROXY_PID_FILE:-$BASE_ROOT_ABS/run/local-proxy.pid}"
LOG_FILE="${LAZYMIND_LOCAL_PROXY_LOG_FILE:-$BASE_ROOT_ABS/logs/local-proxy.console.log}"
HEALTH_URL="http://127.0.0.1:${LAZYMIND_LOCAL_PROXY_PORT}/_local/healthz"

if [ ! -x "$BIN_PATH" ]; then
  echo "local-proxy binary not found or not executable: $BIN_PATH" >&2
  exit 1
fi
if [ ! -f "$CONFIG_PATH" ]; then
  echo "local-proxy config not found: $CONFIG_PATH" >&2
  exit 1
fi

mkdir -p "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")"

echo "Starting local-proxy..."
if command -v setsid >/dev/null 2>&1; then
  setsid "$BIN_PATH" --config "$CONFIG_PATH" >> "$LOG_FILE" 2>&1 &
else
  nohup "$BIN_PATH" --config "$CONFIG_PATH" >> "$LOG_FILE" 2>&1 &
fi
echo $! > "$PID_FILE"

sleep 1
pid="$(cat "$PID_FILE")"
if ! kill -0 "$pid" 2>/dev/null; then
  echo "local-proxy failed to start. Recent log:" >&2
  tail -n 80 "$LOG_FILE" 2>/dev/null || true
  rm -f "$PID_FILE"
  exit 1
fi

if command -v curl >/dev/null 2>&1; then
  if curl -fsS "$HEALTH_URL" >/dev/null; then
    echo "local-proxy started and healthy (pid=$pid)"
  else
    echo "local-proxy health check failed for $HEALTH_URL" >&2
    tail -n 80 "$LOG_FILE" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
  fi
else
  echo "curl not found; local-proxy started (pid=$pid), skipping health check"
fi

echo "local-proxy health: $HEALTH_URL"
echo "Frontend URL: http://localhost:${LAZYMIND_FRONTEND_PORT}"
echo "local-proxy log: $LOG_FILE"
