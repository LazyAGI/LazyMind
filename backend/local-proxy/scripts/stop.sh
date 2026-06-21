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

PORT="${LAZYMIND_LOCAL_PROXY_PORT:-5024}"
PID_FILE="${LAZYMIND_LOCAL_PROXY_PID_FILE:-$BASE_ROOT_ABS/run/local-proxy.pid}"

if [ -f "$PID_FILE" ]; then
  pid="$(cat "$PID_FILE")"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    echo "Stopping local-proxy ($pid)..."
    kill "$pid" 2>/dev/null || true
    for _ in 1 2 3 4 5; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
      echo "local-proxy still running ($pid), please stop it manually if needed." >&2
    fi
  fi
  rm -f "$PID_FILE"
fi

if command -v lsof >/dev/null 2>&1; then
  for pid in $(lsof -t -nP -iTCP:"$PORT" -sTCP:LISTEN 2>/dev/null | sort -u); do
    cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    case "$cmd" in
      *local-proxy*|*local_proxy*)
        echo "Stopping local-proxy on :$PORT ($pid)..."
        kill "$pid" 2>/dev/null || true
        ;;
    esac
  done
fi
