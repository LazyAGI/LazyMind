#!/usr/bin/env bash
set -euo pipefail
export LAZYMIND_DESKTOP_MAC_ARCH=x64
exec bash "$(dirname "${BASH_SOURCE[0]}")/build-darwin-arm64.sh" "$@"
