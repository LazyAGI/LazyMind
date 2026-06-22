#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/algo-env.sh"

LAZYMIND_LOCAL_ALGO_PYTHON_PATH="$("$SCRIPT_DIR/ensure-algo-python.sh")"
export LAZYMIND_LOCAL_ALGO_PYTHON_PATH

STARTED_SERVICES=()

log() {
  echo "[up-algo-host] $*"
}

fail() {
  echo "Error: $*" >&2
  return 1
}

ensure_dirs() {
  mkdir -p \
    "$LAZYMIND_LOCAL_ALGO_LOG_DIR" \
    "$LAZYMIND_LOCAL_ALGO_RUN_DIR" \
    "$LAZYMIND_HOME" \
    "$LAZYMIND_LOCAL_ALGO_ROOT" \
    "$LAZYMIND_SHARED_UPLOAD_DIR" \
    "$REPO_ROOT/data/core/uploads/.lazyllm_temp" \
    "$REPO_ROOT/data/core/uploads/.image_cache" \
    "$REPO_ROOT/data/traces"

  mkdir -p "$LAZYMIND_SHARED_UPLOAD_DIR" \
    "$LAZYMIND_UPLOAD_ROOT" \
    "$LAZYMIND_UPLOAD_DIR"
}

pid_file_for() {
  echo "$LAZYMIND_LOCAL_ALGO_RUN_DIR/$1.pid"
}

unit_for() {
  echo "lazymind-local-algo-$1.service"
}

use_systemd_user() {
  [ "${LAZYMIND_LOCAL_ALGO_SUPERVISOR:-systemd}" = "systemd" ] || return 1
  command -v systemd-run >/dev/null 2>&1 || return 1
  command -v systemctl >/dev/null 2>&1 || return 1
  systemctl --user is-system-running >/dev/null 2>&1
}

log_file_for() {
  echo "$LAZYMIND_LOCAL_ALGO_LOG_DIR/$1.log"
}

port_for() {
  case "$1" in
    processor-server) echo "$LAZYMIND_LOCAL_PROCESSOR_SERVER_PORT" ;;
    processor-worker) echo "$LAZYMIND_LOCAL_PROCESSOR_WORKER_PORT" ;;
    lazyllm-algo) echo "$LAZYMIND_LOCAL_ALGO_PORT" ;;
    doc-server) echo "$LAZYMIND_LOCAL_DOC_SERVER_PORT" ;;
    chat-router) echo "$LAZYMIND_LOCAL_CHAT_ROUTER_PORT" ;;
    *) return 1 ;;
  esac
}

pid_listening_on_port() {
  local port="$1"
  ss -ltnp "sport = :$port" 2>/dev/null \
    | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' \
    | head -n 1
}

is_pid_running() {
  local pid="$1"
  kill -0 "$pid" >/dev/null 2>&1
}

is_pid_file_for_service() {
  local pid_file="$1"
  local pattern="$2"
  local pid

  [ -f "$pid_file" ] || return 1
  pid="$(cat "$pid_file")"
  [ -n "$pid" ] || return 1
  if ! is_pid_running "$pid"; then
    return 1
  fi

  if [ -z "$pattern" ]; then
    return 0
  fi

  ps -ww -p "$pid" -o args= | grep -Fq "$pattern"
}

stop_by_name() {
  local name="$1"
  local pattern="$2"
  local pid_file
  local pid
  local unit

  unit="$(unit_for "$name")"
  if use_systemd_user && systemctl --user --quiet is-active "$unit"; then
    systemctl --user stop "$unit" >/dev/null 2>&1 || true
  fi

  pid_file="$(pid_file_for "$name")"
  [ -f "$pid_file" ] || return 0
  pid="$(cat "$pid_file")"
  [ -n "$pid" ] || return 0

  if ! is_pid_running "$pid"; then
    rm -f "$pid_file"
    return 0
  fi

  if [ -n "$pattern" ] && ! ps -ww -p "$pid" -o args= | grep -Fq "$pattern"; then
    rm -f "$pid_file"
    return 0
  fi

  kill "$pid" || true
  for _ in 1 2 3 4 5; do
    if ! is_pid_running "$pid"; then
      rm -f "$pid_file"
      return 0
    fi
    sleep 1
  done

  kill -9 "$pid" || true
  rm -f "$pid_file"
}

rollback_started() {
  local i
  if [ "${#STARTED_SERVICES[@]}" -eq 0 ]; then
    return
  fi

  for ((i = ${#STARTED_SERVICES[@]} - 1; i >= 0; i--)); do
    case "${STARTED_SERVICES[$i]}" in
      processor-server)
        stop_by_name processor-server "lazymind.processor.service.server"
        ;;
      processor-worker)
        stop_by_name processor-worker "lazymind.processor.service.worker"
        ;;
      lazyllm-algo)
        stop_by_name lazyllm-algo "lazymind.parsing.app"
        ;;
      doc-server)
        stop_by_name doc-server "backend/core/doc/doc_server.py"
        ;;
      chat-router)
        stop_by_name chat-router "lazymind.router.app"
        ;;
    esac
  done
}

tail_log() {
  local service="$1"
  local log_file
  log_file="$(log_file_for "$service")"
  echo "Recent log for $service:"
  if [ -f "$log_file" ]; then
    tail -n 40 "$log_file" || true
  else
    echo "  (log file not found)"
  fi
}

wait_for_http_health() {
  local name="$1"
  local pid_file="$2"
  local url="$3"
  local timeout="${4:-120}"
  local start
  local pid
  local elapsed=0

  pid="$(cat "$pid_file")"
  start="$(date +%s)"

  while :; do
    if ! is_pid_running "$pid"; then
      tail_log "$name"
      fail "$name exited before healthy"
      return 1
    fi

    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi

    elapsed=$(( $(date +%s) - start ))
    if [ "$elapsed" -ge "$timeout" ]; then
      tail_log "$name"
      fail "$name did not become healthy within ${timeout}s: $url"
      return 1
    fi
    sleep 2
  done
}

start_service() {
  local name="$1"
  local pattern="$2"
  local url="$3"
  local pid_file log_file
  local -a cmd

  shift 3
  cmd=("$@")
  pid_file="$(pid_file_for "$name")"
  log_file="$(log_file_for "$name")"

  if is_pid_file_for_service "$pid_file" "$pattern"; then
    log "$name is already running; restarting it"
    stop_by_name "$name" "$pattern"
  fi
  rm -f "$pid_file"

  log "starting $name"

  if use_systemd_user; then
    local unit pid
    unit="$(unit_for "$name")"
    systemctl --user stop "$unit" >/dev/null 2>&1 || true
    systemd-run --quiet --user \
      --unit="${unit%.service}" \
      --collect \
      --same-dir \
      "$SCRIPT_DIR/run-algo-service.sh" "$name" >/dev/null

    for _ in 1 2 3 4 5 6 7 8 9 10; do
      pid="$(systemctl --user show "$unit" --property=MainPID --value 2>/dev/null || true)"
      if [ -n "$pid" ] && [ "$pid" != "0" ]; then
        printf '%s\n' "$pid" > "$pid_file"
        break
      fi
      sleep 0.2
    done
  else
    setsid -f sh -c '
      pid_file="$1"
      shift
      printf "%s\n" "$$" > "$pid_file"
      exec "$@"
    ' local-algo-daemon "$pid_file" "${cmd[@]}" </dev/null >> "$log_file" 2>&1

    for _ in 1 2 3 4 5 6 7 8 9 10; do
      if [ -s "$pid_file" ]; then
        break
      fi
      sleep 0.2
    done
  fi

  if [ ! -s "$pid_file" ]; then
    tail_log "$name"
    fail "$name did not write a pid file"
    rollback_started
    return 1
  fi

  STARTED_SERVICES+=("$name")

  if ! wait_for_http_health "$name" "$pid_file" "$url" 180; then
    rollback_started
    return 1
  fi

  local listen_pid
  listen_pid="$(pid_listening_on_port "$(port_for "$name")")"
  if [ -n "$listen_pid" ]; then
    printf '%s\n' "$listen_pid" > "$pid_file"
  fi

  log "$name is healthy"
}

main() {
  ensure_dirs

  cd "$REPO_ROOT"

  start_service \
    processor-server \
    "lazymind.processor.service.server" \
    "http://127.0.0.1:${LAZYMIND_LOCAL_PROCESSOR_SERVER_PORT}/health" \
    "$LAZYMIND_LOCAL_ALGO_PYTHON_PATH" -m lazymind.processor.service.server

  start_service \
    processor-worker \
    "lazymind.processor.service.worker" \
    "http://127.0.0.1:${LAZYMIND_LOCAL_PROCESSOR_WORKER_PORT}/health" \
    "$LAZYMIND_LOCAL_ALGO_PYTHON_PATH" -m lazymind.processor.service.worker

  start_service \
    lazyllm-algo \
    "lazymind.parsing.app" \
    "http://127.0.0.1:${LAZYMIND_LOCAL_ALGO_PORT}/docs" \
    "$LAZYMIND_LOCAL_ALGO_PYTHON_PATH" -m lazymind.parsing.app

  start_service \
    doc-server \
    "backend/core/doc/doc_server.py" \
    "http://127.0.0.1:${LAZYMIND_LOCAL_DOC_SERVER_PORT}/v1/health" \
    "$LAZYMIND_LOCAL_ALGO_PYTHON_PATH" backend/core/doc/doc_server.py --port "$LAZYMIND_LOCAL_DOC_SERVER_PORT"

  start_service \
    chat-router \
    "lazymind.router.app" \
    "http://127.0.0.1:${LAZYMIND_LOCAL_CHAT_ROUTER_PORT}/health" \
    env LAZYMIND_DOCUMENT_SERVER_URL="$LAZYMIND_CHAT_DOCUMENT_SERVER_URL" \
    "$LAZYMIND_LOCAL_ALGO_PYTHON_PATH" -m lazymind.router.app \
      --host "$LAZYMIND_LOCAL_ALGO_HOST" \
      --port "$LAZYMIND_LOCAL_CHAT_ROUTER_PORT"

  log "all algorithm services are running"
  for name in processor-server processor-worker lazyllm-algo doc-server chat-router; do
    echo " - $name pid $(cat "$(pid_file_for "$name")")"
  done
}

main "$@"
