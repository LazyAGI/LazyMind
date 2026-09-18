#!/usr/bin/env bash
# Local macOS acceptance build only; used via the existing GO override.
set -euo pipefail

go_bin="${LAZYMIND_ACCEPTANCE_REAL_GO:?Set this to the absolute path of go}"
export GOTOOLCHAIN=auto
if [[ "${1:-}" == run && "${2:-}" == ./cmd/builtin-skill-bundle ]]; then
  export GOTOOLCHAIN=go1.25.0
  for arg in "$@"; do
    if [[ "$arg" == --frozen-lockfile* ]]; then
      exec "$go_bin" "$@"
    fi
  done
  exec "$go_bin" "$@" --frozen-lockfile
fi
exec "$go_bin" "$@"
