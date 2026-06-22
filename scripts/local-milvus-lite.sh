#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${LAZYMIND_LOCAL_MILVUS_PORT:-19530}"
BASE_ROOT="${LAZYMIND_LOCAL_MILVUS_BASE_ROOT:-$ROOT_DIR/.lazymind-local/milvus-lite}"
DATA_DIR="${LAZYMIND_LOCAL_MILVUS_DATA_DIR:-$BASE_ROOT/data}"
LOG_DIR="${LAZYMIND_LOCAL_MILVUS_LOG_DIR:-$BASE_ROOT/logs}"
RUN_DIR="${LAZYMIND_LOCAL_MILVUS_RUN_DIR:-$BASE_ROOT/run}"
PID_FILE="$RUN_DIR/milvus-lite.pid"
LOG_FILE="$LOG_DIR/milvus-lite.console.log"

usage() {
  echo "Usage: $0 {start|stop|status}"
}

ensure_server_command() {
  if ! command -v milvus-lite >/dev/null 2>&1; then
    echo "milvus-lite command not found. Install a Milvus Lite version with server mode, for example: python -m pip install -U milvus-lite" >&2
    exit 1
  fi
  if ! milvus-lite server --help >/dev/null 2>&1; then
    echo "milvus-lite server mode is not available in this environment. Use milvus-lite 3.0 or newer for the host server launcher." >&2
    exit 1
  fi
}

is_running() {
  if [ ! -f "$PID_FILE" ]; then
    return 1
  fi
  local pid
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

wait_for_port() {
  local deadline=$((SECONDS + 30))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if python3 - "$PORT" <<'PY' >/dev/null 2>&1
import socket
import sys

port = int(sys.argv[1])
with socket.create_connection(("127.0.0.1", port), timeout=1):
    pass
PY
    then
      return 0
    fi
    sleep 1
  done
  return 1
}

start() {
  ensure_server_command
  mkdir -p "$DATA_DIR" "$LOG_DIR" "$RUN_DIR"
  if is_running; then
    echo "Milvus Lite already running (pid $(cat "$PID_FILE"), port $PORT)"
    return 0
  fi

  nohup milvus-lite server --data-dir "$DATA_DIR" --port "$PORT" >>"$LOG_FILE" 2>&1 &
  echo "$!" >"$PID_FILE"

  if wait_for_port && is_running; then
    echo "Milvus Lite started on 127.0.0.1:$PORT"
    echo "Data: $DATA_DIR"
    echo "Log:  $LOG_FILE"
    return 0
  fi

  echo "Milvus Lite failed to start. Recent log:" >&2
  tail -n 80 "$LOG_FILE" >&2 || true
  rm -f "$PID_FILE"
  exit 1
}

stop() {
  if ! is_running; then
    rm -f "$PID_FILE"
    echo "Milvus Lite is not running"
    return 0
  fi

  local pid
  pid="$(cat "$PID_FILE")"
  kill "$pid" 2>/dev/null || true
  for _ in 1 2 3 4 5; do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$PID_FILE"
      echo "Milvus Lite stopped"
      return 0
    fi
    sleep 1
  done

  echo "Milvus Lite still running (pid $pid); please stop it manually if needed" >&2
  exit 1
}

status() {
  if is_running; then
    echo "Milvus Lite running (pid $(cat "$PID_FILE"), port $PORT)"
    return 0
  fi
  echo "Milvus Lite is not running"
  return 1
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  *) usage; exit 2 ;;
esac
