#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VENV_DIR="${LAZYMIND_TEST_VENV:-$ROOT/.venv-test}"
PYTHON_BIN="$VENV_DIR/bin/python"
LOCK_STAMP="$VENV_DIR/.lazymind-test-hermetic.sha256"
REQUIRED_PYTHON="3.11"
REQUIRED_NODE_MAJOR="20"
REQUIRED_GO="1.24.0"

fail() {
  echo "Error: $*" >&2
  return 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

test_hermetic_hash_inputs() {
  if [ ! -f "$ROOT/algorithm/lazyllm/pyproject.toml" ]; then
    echo "Initializing git submodules..." >&2
    git -C "$ROOT" submodule update --init || fail "failed to initialize submodules"
  fi

  local files=(
    "$ROOT/requirements/test-hermetic.txt"
    "$ROOT/backend/auth-service/requirements.txt"
    "$ROOT/tests/backend/auth-service/requirements-test.txt"
    "$ROOT/algorithm/requirements.txt"
    "$ROOT/algorithm/lazyllm/pyproject.toml"
    "$ROOT/scripts/test-hermetic-env.sh"
  )

  local file
  for file in "${files[@]}"; do
    [ -f "$file" ] || fail "required dependency input missing: $file"
    hash_file "$file"
  done | if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

test_hermetic_check_uv() {
  require_cmd uv || fail "uv not found: install uv before running make test-hermetic"
  uv python find "$REQUIRED_PYTHON" >/dev/null 2>&1 \
    || fail "Python $REQUIRED_PYTHON is not available through uv"
}

test_hermetic_load_nvm() {
  if command -v nvm >/dev/null 2>&1; then
    return 0
  fi

  local nvm_sh="${NVM_DIR:-$HOME/.nvm}/nvm.sh"
  if [ -s "$nvm_sh" ]; then
    # shellcheck disable=SC1090
    . "$nvm_sh"
  fi

  command -v nvm >/dev/null 2>&1
}

test_hermetic_select_node() {
  if command -v fnm >/dev/null 2>&1; then
    eval "$(fnm env --shell bash)"
    fnm use "$REQUIRED_NODE_MAJOR" >/dev/null \
      || fail "Node $REQUIRED_NODE_MAJOR is not installed for fnm"
  elif test_hermetic_load_nvm; then
    nvm use "$REQUIRED_NODE_MAJOR" >/dev/null \
      || fail "Node $REQUIRED_NODE_MAJOR is not installed for nvm"
  else
    fail "neither fnm nor nvm was found: install one Node manager before running make test-hermetic"
  fi

  require_cmd node || fail "node not found after selecting Node $REQUIRED_NODE_MAJOR"
  require_cmd npm || fail "npm not found after selecting Node $REQUIRED_NODE_MAJOR"

  local major
  major="$(node -p 'process.versions.node.split(".")[0]')"
  [ "$major" = "$REQUIRED_NODE_MAJOR" ] \
    || fail "Node $(node --version) selected, but Node $REQUIRED_NODE_MAJOR is required"
}

test_hermetic_check_go() {
  require_cmd go || fail "go not found: install Go $REQUIRED_GO before running make test-hermetic"

  local version
  version="$(go version | awk '{print $3}' | sed 's/^go//')"
  if [[ ! "$version" =~ ^1\.24(\.[0-9]+)?$ ]]; then
    fail "Go $version found, but Go 1.24.x is required"
  fi
}

test_hermetic_create_or_sync_python() {
  mkdir -p "$VENV_DIR"

  if [ ! -x "$PYTHON_BIN" ]; then
    rm -rf "$VENV_DIR"
    uv venv "$VENV_DIR" --python "$REQUIRED_PYTHON"
  fi

  local version
  version="$("$PYTHON_BIN" -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
  if [ "$version" != "$REQUIRED_PYTHON" ]; then
    rm -rf "$VENV_DIR"
    uv venv "$VENV_DIR" --python "$REQUIRED_PYTHON"
  fi

  local expected actual
  expected="$(test_hermetic_hash_inputs)"
  actual=""
  [ -f "$LOCK_STAMP" ] && actual="$(cat "$LOCK_STAMP")"

  if [ "$expected" != "$actual" ]; then
    uv pip install --python "$PYTHON_BIN" -r "$ROOT/requirements/test-hermetic.txt"
    CMAKE_POLICY_VERSION_MINIMUM=3.5 \
      uv pip install --python "$PYTHON_BIN" --no-cache-dir "$ROOT/algorithm/lazyllm"
    "$PYTHON_BIN" - <<'PY'
from pathlib import Path
import shutil
import lazyllm

source = Path("algorithm/lazyllm/pyproject.toml").resolve()
target = Path(lazyllm.__path__[0]) / "pyproject.toml"
if source.exists():
    shutil.copy2(source, target)
PY
  fi

  uv pip check --python "$PYTHON_BIN" >/dev/null
  echo "$expected" > "$LOCK_STAMP"
}

test_hermetic_prepare() {
  cd "$ROOT"
  test_hermetic_check_uv
  test_hermetic_select_node
  test_hermetic_check_go
  test_hermetic_create_or_sync_python

  export LAZYMIND_TEST_PYTHON="$PYTHON_BIN"
  export PYTHONPATH="$ROOT:$ROOT/algorithm:$ROOT/backend/auth-service${PYTHONPATH:+:$PYTHONPATH}"
}

test_hermetic_check() {
  cd "$ROOT"
  test_hermetic_check_uv
  test_hermetic_select_node
  test_hermetic_check_go
  [ -x "$PYTHON_BIN" ] || fail "Python test venv missing: run make test-hermetic-setup"
  uv pip check --python "$PYTHON_BIN" >/dev/null
  echo "Hermetic host test environment OK."
}

case "${1:-}" in
  setup)
    test_hermetic_prepare
    echo "Hermetic host test environment prepared at $VENV_DIR."
    ;;
  check)
    test_hermetic_check
    ;;
  "")
    ;;
  *)
    fail "unknown command: $1"
    ;;
esac
