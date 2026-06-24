#!/usr/bin/env bash
# Run all unit tests (frontend, auth-service, backend/core, algorithm).
# Execute from project root: ./tests/run-all.sh
set -e

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FAILED=0

echo "=== Frontend ==="
if command -v npm &>/dev/null; then
  (cd tests/frontend && npm install --silent; npm test 2>&1) || FAILED=1
else
  echo "Skip (npm not found)"
fi

echo ""
echo "=== auth-service ==="
if command -v python3 &>/dev/null; then
  python3 -m pytest tests/backend/auth-service/ -v --tb=short 2>&1 || FAILED=1
else
  echo "Skip (python3 not found)"
fi

echo ""
echo "=== backend/core ==="
if command -v go &>/dev/null; then
  (cd tests/backend/core && go test ./... -v 2>&1) || FAILED=1
else
  echo "Skip (go not found)"
fi

echo ""
echo "=== local/local-proxy ==="
if command -v go &>/dev/null; then
  (cd local/local-proxy && GOCACHE=/tmp/local-proxy-gocache go test ./... -v 2>&1) || FAILED=1
else
  echo "Skip (go not found)"
fi

echo ""
echo "=== algorithm ==="
if command -v python3 &>/dev/null; then
  python3 -m pytest tests/algorithm/ -v --tb=short 2>&1 || FAILED=1
else
  echo "Skip (python3 not found)"
fi

if [ $FAILED -eq 1 ]; then
  echo ""
  echo "Some tests failed."
  exit 1
fi
echo ""
echo "All tests passed."
