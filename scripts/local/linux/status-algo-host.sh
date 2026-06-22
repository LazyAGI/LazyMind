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
    *) return 1 ;;
  esac
}

pid_listening_on_port() {
  local port="$1"
  ss -ltnp "sport = :$port" 2>/dev/null \
    | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' \
    | head -n 1
}

report_process() {
  local name="$1"
  local pattern="$2"
  local pid_file pid

  pid_file="$(pid_file_for "$name")"
  if use_systemd_user && systemctl --user --quiet is-active "$(unit_for "$name")"; then
    local unit_pid
    unit_pid="$(systemctl --user show "$(unit_for "$name")" --property=MainPID --value 2>/dev/null || true)"
    echo "$name: running via systemd (pid ${unit_pid:-unknown})"
    return
  fi

  if [ ! -f "$pid_file" ]; then
    local listen_pid
    listen_pid="$(pid_listening_on_port "$(port_for "$name")")"
    if [ -n "$listen_pid" ]; then
      printf '%s\n' "$listen_pid" > "$pid_file"
      echo "$name: running (pid $listen_pid, recovered from listening port)"
      return
    fi
    echo "$name: stopped (no pid file)"
    return
  fi

  pid="$(cat "$pid_file")"
  if [ -z "$pid" ] || ! is_pid_running "$pid"; then
    local listen_pid
    listen_pid="$(pid_listening_on_port "$(port_for "$name")")"
    if [ -n "$listen_pid" ]; then
      printf '%s\n' "$listen_pid" > "$pid_file"
      echo "$name: running (pid $listen_pid, recovered from listening port)"
      return
    fi
    echo "$name: stale pid file -> remove with down-algo-host"
    return
  fi

  if ! ps -ww -p "$pid" -o args= | grep -Fq "$pattern"; then
    if [ -n "$(pid_listening_on_port "$(port_for "$name")")" ]; then
      echo "$name: running (pid $pid, listening on managed port)"
      return
    fi
    echo "$name: pid $pid does not match managed command (stale)"
    return
  fi

  echo "$name: running (pid $pid)"
}

check_health() {
  local name="$1"
  local url="$2"

  if curl -fsS "$url" >/dev/null 2>&1; then
    echo "  health: ok ($url)"
  else
    echo "  health: bad ($url)"
  fi
}

warn_containers() {
  if ! command -v docker >/dev/null 2>&1; then
    return
  fi

  local containers=(
    "lazyllm-parse-server"
    "lazyllm-parse-worker"
    "lazyllm-doc-server"
    "lazyllm-algo"
    "chat"
  )

  local found=0
  local names=()
  local container
  for container in "${containers[@]}"; do
    while IFS= read -r line; do
      if [ -n "$line" ]; then
        found=1
        names+=("$line")
      fi
    done < <(docker ps --filter "name=$container" --format '{{.Names}}')
  done

  if [ "$found" -eq 1 ]; then
    echo
    echo "Warning: algorithm containers are running and may conflict with host processes:"
    printf '  - %s\n' "${names[@]}"
  fi
}

main() {
  echo "LazyMind local algo host status:"
  report_process "processor-server" "lazymind.processor.service.server"
  check_health "processor-server" "http://127.0.0.1:${LAZYMIND_LOCAL_PROCESSOR_SERVER_PORT}/health"
  report_process "processor-worker" "lazymind.processor.service.worker"
  check_health "processor-worker" "http://127.0.0.1:${LAZYMIND_LOCAL_PROCESSOR_WORKER_PORT}/health"
  report_process "lazyllm-algo" "lazymind.parsing.app"
  check_health "lazyllm-algo" "http://127.0.0.1:${LAZYMIND_LOCAL_ALGO_PORT}/docs"
  report_process "doc-server" "backend/core/doc/doc_server.py"
  check_health "doc-server" "http://127.0.0.1:${LAZYMIND_LOCAL_DOC_SERVER_PORT}/v1/health"
  report_process "chat-router" "lazymind.router.app"
  check_health "chat-router" "http://127.0.0.1:${LAZYMIND_LOCAL_CHAT_ROUTER_PORT}/health"

  warn_containers
}

main "$@"
