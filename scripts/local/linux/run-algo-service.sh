#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/algo-env.sh"

service="${1:-}"
if [ -z "$service" ]; then
  echo "Usage: $0 <processor-server|processor-worker|lazyllm-algo|doc-server|chat-router>" >&2
  exit 2
fi

mkdir -p "$LAZYMIND_LOCAL_ALGO_LOG_DIR" "$LAZYMIND_LOCAL_ALGO_RUN_DIR"
log_file="$LAZYMIND_LOCAL_ALGO_LOG_DIR/$service.log"
exec >> "$log_file" 2>&1

cd "$REPO_ROOT"
python_path="$LAZYMIND_LOCAL_ALGO_PYTHON"
if [ ! -x "$python_path" ]; then
  python_path="$("$SCRIPT_DIR/ensure-algo-python.sh")"
fi

case "$service" in
  processor-server)
    exec "$python_path" -m lazymind.processor.service.server
    ;;
  processor-worker)
    exec "$python_path" -m lazymind.processor.service.worker
    ;;
  lazyllm-algo)
    exec "$python_path" -m lazymind.parsing.app
    ;;
  doc-server)
    exec "$python_path" backend/core/doc/doc_server.py --port "$LAZYMIND_LOCAL_DOC_SERVER_PORT"
    ;;
  chat-router)
    export LAZYMIND_DOCUMENT_SERVER_URL="$LAZYMIND_CHAT_DOCUMENT_SERVER_URL"
    exec "$python_path" -m lazymind.router.app \
      --host "$LAZYMIND_LOCAL_ALGO_HOST" \
      --port "$LAZYMIND_LOCAL_CHAT_ROUTER_PORT"
    ;;
  *)
    echo "Unknown local algorithm service: $service" >&2
    exit 2
    ;;
esac
