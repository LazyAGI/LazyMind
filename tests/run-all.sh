#!/usr/bin/env bash
# Run all unit tests.
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
echo "=== channel-gateway ==="
if command -v python3 &>/dev/null; then
  PYTHONPATH=backend/channel-gateway \
    python3 -m unittest discover \
      -s backend/channel-gateway/tests -p 'test_*.py' -v 2>&1 \
    || FAILED=1
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
echo "=== local/local-runtime-manager ==="
if command -v go &>/dev/null; then
  (cd local/local-runtime-manager && GOCACHE=/tmp/local-runtime-manager-gocache go test ./... -v 2>&1) || FAILED=1
else
  echo "Skip (go not found)"
fi

echo ""
echo "=== desktop shell ==="
if command -v node &>/dev/null; then
  node --test desktop/scripts/*.test.mjs 2>&1 || FAILED=1
else
  echo "Skip (node not found)"
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
