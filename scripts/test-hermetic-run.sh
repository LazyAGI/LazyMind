#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck disable=SC1091
. "$ROOT/scripts/test-hermetic-env.sh"

test_hermetic_prepare

FAILED=0

run_section() {
  local name="$1"
  shift

  echo "=== $name ==="
  if "$@"; then
    echo "=== $name: PASS ==="
  else
    echo "=== $name: FAIL ==="
    FAILED=1
  fi
  echo ""
}

run_frontend() {
  cd "$ROOT/tests/frontend"
  if [ ! -d "node_modules" ] || [ "package-lock.json" -nt "node_modules" ]; then
    npm ci --silent
  fi
  npm test
}

run_auth_service() {
  cd "$ROOT"
  "$LAZYMIND_TEST_PYTHON" -m pytest tests/backend/auth-service/ -v --tb=short
}

run_backend_core() {
  cd "$ROOT/tests/backend/core"
  go test ./... -v
}

run_algorithm() {
  cd "$ROOT"
  "$LAZYMIND_TEST_PYTHON" -m pytest tests/algorithm/ -v --tb=short
}

run_section "Frontend" run_frontend
run_section "auth-service" run_auth_service
run_section "backend/core" run_backend_core
run_section "algorithm" run_algorithm

if [ "$FAILED" -eq 1 ]; then
  echo "Some hermetic test segments failed."
  exit 1
fi

echo "All hermetic test segments passed."
