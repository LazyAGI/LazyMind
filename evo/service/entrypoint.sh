#!/bin/sh
set -e

if [ "${LAZYMIND_EVO_CODE_DATA_DIR:-}" = "/var/lib/lazymind/evo/opencode" ]; then
  LAZYMIND_EVO_CODE_DATA_DIR="/var/lib/lazymind/evo/work/opencode"
fi
OC_DATA_DIR="${LAZYMIND_EVO_CODE_DATA_DIR:-/var/lib/lazymind/evo/work/opencode}"
mkdir -p "$OC_DATA_DIR"
chmod 700 "$OC_DATA_DIR"

LINK_TARGET="${HOME:-/root}/.local/share/opencode"
mkdir -p "$(dirname "$LINK_TARGET")"
if [ ! -L "$LINK_TARGET" ] && [ ! -e "$LINK_TARGET" ]; then
  ln -s "$OC_DATA_DIR" "$LINK_TARGET"
elif [ -L "$LINK_TARGET" ] && [ "$(readlink "$LINK_TARGET")" != "$OC_DATA_DIR" ]; then
  rm -f "$LINK_TARGET"
  ln -s "$OC_DATA_DIR" "$LINK_TARGET"
fi

if [ ! -f "$OC_DATA_DIR/auth.json" ]; then
  if [ -n "$LAZYMIND_EVO_OPENCODE_AUTH_JSON" ]; then
    printf '%s' "$LAZYMIND_EVO_OPENCODE_AUTH_JSON" > "$OC_DATA_DIR/auth.json"
  elif [ -n "$LAZYMIND_EVO_OPENCODE_ANTHROPIC_KEY" ]; then
    printf '{"anthropic":{"type":"api","key":"%s"}}' \
      "$LAZYMIND_EVO_OPENCODE_ANTHROPIC_KEY" > "$OC_DATA_DIR/auth.json"
  fi
fi

exec uvicorn evo.service.api:get_app --factory --host 0.0.0.0 --port "${LAZYMIND_EVO_API_PORT:-8047}"
