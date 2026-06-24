#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_PROXY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_CONFIG="$LOCAL_PROXY_DIR/configs/local.yaml"
CONFIG_PATH="${1:-$DEFAULT_CONFIG}"
PROXY_PORT="${LAZYMIND_LOCAL_PROXY_PORT:-5024}"
PROXY_ADDR="http://127.0.0.1:$PROXY_PORT"

if [ ! -f "$CONFIG_PATH" ]; then
  echo "Local Proxy config not found: $CONFIG_PATH" >&2
  exit 1
fi

echo "Starting Local Proxy in Local Mode from source."
echo "Using config: $CONFIG_PATH"
echo "Local Proxy URL: $PROXY_ADDR"
echo "Frontend hint: export VITE_PROXY_TARGET=$PROXY_ADDR"
echo "Note: this script only starts Local Proxy and does not start auth/core/chat services."
echo

cd "$LOCAL_PROXY_DIR"
exec go run ./cmd/local-proxy --config "$CONFIG_PATH"
