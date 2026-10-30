#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
modules=(
  backend/core
  backend/scan-control-plane
  backend/file-watcher
  local/local-proxy
  local/local-runtime-manager
  local/lazymind-cli
  tests/backend/core
)

# The exec wrapper receives the test binary path but never starts it, including
# init and TestMain. Go's result cache includes the wrapper and -run arguments;
# these compilation-only commands cannot substitute for CI's real test runs.
for module in "${modules[@]}"; do
  echo "Warming Go compilation cache: $module"
  (
    cd "$repo_root/$module"
    # Match the integration job's module preparation before compiling its tests.
    if [[ "$module" == tests/backend/core ]]; then
      go mod tidy
    fi
    go test -exec=true -run '^$' ./...
  )
done

# Core's SQLite and PostgreSQL jobs both use coverage instrumentation.
(cd "$repo_root/backend/core" && go test -exec=true -run '^$' -cover ./...)
