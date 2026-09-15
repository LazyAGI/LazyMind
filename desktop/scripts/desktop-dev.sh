#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
STATE_DIR="${REPO_ROOT}/local/build/desktop-dev"
FRONTEND_DIR="${REPO_ROOT}/frontend"
ELECTRON_DIR="${REPO_ROOT}/desktop/electron"
VITE_PID_FILE="${STATE_DIR}/vite.pid"
ELECTRON_PID_FILE="${STATE_DIR}/electron.pid"
VITE_LOG="${STATE_DIR}/vite.log"
ELECTRON_LOG="${STATE_DIR}/electron.log"
DEV_PORT="${LAZYMIND_DESKTOP_DEV_PORT:-5173}"
RUNTIME_URL="${LAZYMIND_DESKTOP_EXTERNAL_RUNTIME_URL:-http://127.0.0.1:8090}"
DEV_URL="http://127.0.0.1:${DEV_PORT}"

pid_is_running() {
  local pid="${1:-}"
  [[ "${pid}" =~ ^[0-9]+$ ]] && kill -0 "${pid}" 2>/dev/null
}

read_pid() {
  local file="$1"
  if [[ -f "${file}" ]]; then
    tr -d '[:space:]' < "${file}"
  fi
}

stop_pid_file() {
  local file="$1"
  local expected="$2"
  local pid
  pid="$(read_pid "${file}")"
  if ! pid_is_running "${pid}"; then
    rm -f "${file}"
    return
  fi
  local command
  command="$(ps -p "${pid}" -o args= 2>/dev/null || true)"
  if [[ "${command}" != *"${expected}"* ]]; then
    echo "Refusing to stop pid ${pid}: command does not contain ${expected}" >&2
    return 1
  fi
  kill "${pid}"
  for _ in $(seq 1 30); do
    if ! pid_is_running "${pid}"; then
      rm -f "${file}"
      return
    fi
    sleep 0.1
  done
  kill -KILL "${pid}" 2>/dev/null || true
  rm -f "${file}"
}

stop_dev() {
  stop_pid_file "${ELECTRON_PID_FILE}" "desktop/electron/scripts/dev-runner.js" || true
  stop_pid_file "${VITE_PID_FILE}" "vite/bin/vite.js" || true
  echo "Desktop development processes stopped; Local Runtime was left running."
}

ensure_dependencies() {
  if ! command -v node >/dev/null 2>&1; then
    echo "Node.js is required for Desktop development mode." >&2
    exit 1
  fi
  if ! command -v pnpm >/dev/null 2>&1; then
    echo "pnpm is required for Desktop development mode." >&2
    exit 1
  fi
  if [[ ! -f "${FRONTEND_DIR}/node_modules/vite/bin/vite.js" ]]; then
    echo "Installing frontend dependencies (one-time)..."
    pnpm --dir "${FRONTEND_DIR}" install --frozen-lockfile
  fi
  if [[ ! -f "${ELECTRON_DIR}/node_modules/electron/index.js" ]]; then
    echo "Installing Electron development dependencies (one-time)..."
    pnpm --dir "${ELECTRON_DIR}" install --frozen-lockfile
  fi
}

wait_for_url() {
  local url="$1"
  local label="$2"
  local pid="${3:-}"
  for _ in $(seq 1 120); do
    if [[ -n "${pid}" ]] && ! pid_is_running "${pid}"; then
      echo "${label} exited before becoming ready." >&2
      return 1
    fi
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return
    fi
    sleep 0.25
  done
  echo "Timed out waiting for ${label}: ${url}" >&2
  return 1
}

start_dev() {
  mkdir -p "${STATE_DIR}"
  local existing_vite existing_electron
  existing_vite="$(read_pid "${VITE_PID_FILE}")"
  existing_electron="$(read_pid "${ELECTRON_PID_FILE}")"
  if pid_is_running "${existing_vite}" || pid_is_running "${existing_electron}"; then
    echo "Desktop development mode is already running. Use 'make desktop-dev-down' first." >&2
    exit 1
  fi
  rm -f "${VITE_PID_FILE}" "${ELECTRON_PID_FILE}"

  if ! curl -fsS "${RUNTIME_URL}/_local/healthz" >/dev/null 2>&1; then
    echo "Local Runtime is not ready at ${RUNTIME_URL}. Run 'make local-up' first." >&2
    exit 1
  fi
  ensure_dependencies
  pnpm --dir "${FRONTEND_DIR}" run predev

  : > "${VITE_LOG}"
  VITE_LAZYMIND_MODE=desktop \
  VITE_PROXY_TARGET="${RUNTIME_URL}" \
  nohup node "${FRONTEND_DIR}/node_modules/vite/bin/vite.js" \
    "${FRONTEND_DIR}" --config "${FRONTEND_DIR}/vite.config.ts" \
    --host 127.0.0.1 --port "${DEV_PORT}" --strictPort \
    > "${VITE_LOG}" 2>&1 &
  local vite_pid=$!
  printf '%s\n' "${vite_pid}" > "${VITE_PID_FILE}"
  if ! wait_for_url "${DEV_URL}/agent/chat/home" "Desktop Vite server" "${vite_pid}"; then
    tail -n 80 "${VITE_LOG}" >&2 || true
    stop_dev
    exit 1
  fi

  : > "${ELECTRON_LOG}"
  LAZYMIND_DESKTOP_DEV_URL="${DEV_URL}" \
  LAZYMIND_DESKTOP_EXTERNAL_RUNTIME_URL="${RUNTIME_URL}" \
  ELECTRON_DISABLE_SECURITY_WARNINGS=true \
  nohup node "${ELECTRON_DIR}/scripts/dev-runner.js" \
    > "${ELECTRON_LOG}" 2>&1 &
  local electron_pid=$!
  printf '%s\n' "${electron_pid}" > "${ELECTRON_PID_FILE}"
  sleep 2
  if ! pid_is_running "${electron_pid}"; then
    echo "Electron development runner exited during startup." >&2
    tail -n 120 "${ELECTRON_LOG}" >&2 || true
    stop_dev
    exit 1
  fi

  echo "Desktop development mode ready."
  echo "  renderer: ${DEV_URL}/agent/chat/home"
  echo "  runtime:  ${RUNTIME_URL}"
  echo "  Vite log: ${VITE_LOG}"
  echo "  Electron: ${ELECTRON_LOG}"
  echo "Frontend changes use Vite HMR; Electron src/*.js changes restart Electron automatically."
}

case "${1:-start}" in
  start) start_dev ;;
  stop) stop_dev ;;
  *) echo "usage: $0 [start|stop]" >&2; exit 2 ;;
esac
