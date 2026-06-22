#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/algo-env.sh"

REQUIRED_PYTHON=3.11
HASH_FILE="$LAZYMIND_LOCAL_ALGO_VENV/.algo-python.sha256"
export LAZYMIND_LOCAL_ALGO_PYTHON="$LAZYMIND_LOCAL_ALGO_VENV/bin/python"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Error: required command not found: $1" >&2
    return 1
  fi
}

hash_inputs() {
  if [ ! -f "$REPO_ROOT/algorithm/requirements.txt" ]; then
    echo "Error: missing algorithm requirements file: $REPO_ROOT/algorithm/requirements.txt" >&2
    exit 1
  fi
  if [ ! -f "$REPO_ROOT/algorithm/lazyllm/pyproject.toml" ]; then
    echo "Error: missing LazyLLM submodule. Run git submodule update --init from the repository root." >&2
    exit 1
  fi

  {
    sha256sum "$REPO_ROOT/algorithm/requirements.txt"
    sha256sum "$REPO_ROOT/algorithm/lazyllm/pyproject.toml"
  } | sha256sum | awk '{print $1}'
}

ensure_python() {
  mkdir -p "$LAZYMIND_LOCAL_ALGO_ROOT"

  require_cmd uv
  uv python find "$REQUIRED_PYTHON" >/dev/null 2>&1 \
    || { echo "Error: uv python ${REQUIRED_PYTHON} is not available." >&2; exit 1; }

  if [ ! -x "$LAZYMIND_LOCAL_ALGO_PYTHON" ]; then
    uv venv "$LAZYMIND_LOCAL_ALGO_VENV" --python "$REQUIRED_PYTHON"
  fi

  local current_version
  current_version="$("$LAZYMIND_LOCAL_ALGO_PYTHON" -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"

  if [ "$current_version" != "$REQUIRED_PYTHON" ]; then
    rm -rf "$LAZYMIND_LOCAL_ALGO_VENV"
    uv venv "$LAZYMIND_LOCAL_ALGO_VENV" --python "$REQUIRED_PYTHON"
  fi

  local expected actual
  expected="$(hash_inputs)"
  actual=""
  [ -f "$HASH_FILE" ] && actual="$(cat "$HASH_FILE")"

  if [ "$expected" != "$actual" ]; then
    uv pip install --python "$LAZYMIND_LOCAL_ALGO_PYTHON" 'lazyllm[rag]'
    CMAKE_POLICY_VERSION_MINIMUM=3.5 \
      uv pip install --python "$LAZYMIND_LOCAL_ALGO_PYTHON" --no-cache-dir "$REPO_ROOT/algorithm/lazyllm"
    uv pip install --python "$LAZYMIND_LOCAL_ALGO_PYTHON" -r "$REPO_ROOT/algorithm/requirements.txt"
    printf '%s\n' "$expected" > "$HASH_FILE"
  fi
}

ensure_python
printf '%s\n' "$LAZYMIND_LOCAL_ALGO_PYTHON"
