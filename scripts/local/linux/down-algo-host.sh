#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/algo-env.sh"

pid_file_for() {
  echo "$LAZYMIND_LOCAL_ALGO_RUN_DIR/$1.pid"
}

unit_for() {
  echo "lazymind-local-algo-$1.service"
}

use_systemd_user() {
  [ "${LAZYMIND_LOCAL_ALGO_SUPERVISOR:-systemd}" = "systemd" ] || return 1
  command -v systemctl >/dev/null 2>&1 || return 1
  systemctl --user is-system-running >/dev/null 2>&1
}

is_pid_running() {
  local pid="$1"
  kill -0 "$pid" >/dev/null 2>&1
}

port_for() {
  case "$1" in
    processor-server) echo "$LAZYMIND_LOCAL_PROCESSOR_SERVER_PORT" ;;
    processor-worker) echo "$LAZYMIND_LOCAL_PROCESSOR_WORKER_PORT" ;;
    lazyllm-algo) echo "$LAZYMIND_LOCAL_ALGO_PORT" ;;
    doc-server) echo "$LAZYMIND_LOCAL_DOC_SERVER_PORT" ;;
    chat-router) echo "$LAZYMIND_LOCAL_CHAT_ROUTER_PORT" ;;
    chat-algorithm) echo "$LAZYMIND_ROUTER_PORT_POOL_START" ;;
    *) return 1 ;;
  esac
}

pid_listening_on_port() {
  local port="$1"
  ss -ltnp "sport = :$port" 2>/dev/null \
    | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' \
    | head -n 1
}

is_process_matching() {
  local pid="$1"
  local pattern="$2"
  ps -ww -p "$pid" -o args= | grep -Fq "$pattern"
}

stop_service() {
  local name="$1"
  local pattern="$2"
  local pid_file pid
  local unit

  pid_file="$(pid_file_for "$name")"
  unit="$(unit_for "$name")"

  if use_systemd_user && systemctl --user --quiet is-active "$unit"; then
    echo "[down-algo-host] stopping $name systemd unit ($unit)"
    systemctl --user stop "$unit" >/dev/null 2>&1 || true
  fi

  if [ -f "$pid_file" ]; then
    pid="$(cat "$pid_file")"
  else
    pid="$(pid_listening_on_port "$(port_for "$name")")"
  fi

  if [ -z "${pid:-}" ] || ! is_pid_running "$pid"; then
    pid="$(pid_listening_on_port "$(port_for "$name")")"
    if [ -z "${pid:-}" ] || ! is_pid_running "$pid"; then
      rm -f "$pid_file"
      echo "[down-algo-host] $name: removed stale pid file"
      return 0
    fi
  fi

  if [ -n "$pattern" ] && ! is_process_matching "$pid" "$pattern" && [ "$pid" != "$(pid_listening_on_port "$(port_for "$name")")" ]; then
    rm -f "$pid_file"
    echo "[down-algo-host] $name: stale pid file points to unrelated process $pid"
    return 0
  fi

  echo "[down-algo-host] stopping $name (pid $pid)"
  kill "$pid" >/dev/null 2>&1 || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if ! is_pid_running "$pid"; then
      rm -f "$pid_file"
      echo "[down-algo-host] stopped $name"
      return 0
    fi
    sleep 1
  done

  kill -9 "$pid" >/dev/null 2>&1 || true
  rm -f "$pid_file"
  echo "[down-algo-host] force-killed $name (pid $pid)"
}

main() {
  stop_service chat-algorithm ""
  stop_service chat-router "lazymind.router.app"
  stop_service doc-server "backend/core/doc/doc_server.py"
  stop_service lazyllm-algo "lazymind.parsing.app"
  stop_service processor-worker "lazymind.processor.service.worker"
  stop_service processor-server "lazymind.processor.service.server"
}

main "$@"
