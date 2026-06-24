#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_PROXY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT="$(cd "$LOCAL_PROXY_DIR/../.." && pwd)"

GO_BIN="${GO:-go}"
BASE_ROOT="${LAZYMIND_LOCAL_PROXY_BASE_ROOT:-./data/local-proxy}"
case "$BASE_ROOT" in
  /*) BASE_ROOT_ABS="$BASE_ROOT" ;;
  *) BASE_ROOT_ABS="$ROOT/$BASE_ROOT" ;;
esac

BIN_PATH="${LAZYMIND_LOCAL_PROXY_BIN:-$BASE_ROOT_ABS/bin/local-proxy}"

mkdir -p "$(dirname "$BIN_PATH")" "$BASE_ROOT_ABS/run" "$BASE_ROOT_ABS/logs"

echo "Rebuilding local-proxy..."
rm -f "$BIN_PATH"
(cd "$LOCAL_PROXY_DIR" && "$GO_BIN" build -o "$BIN_PATH" ./cmd/local-proxy)
echo "local-proxy built: $BIN_PATH"
