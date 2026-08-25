#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
base_revision=${1:-}

if [[ -z "$base_revision" && -n "${GITHUB_BASE_REF:-}" ]]; then
  base_revision="origin/${GITHUB_BASE_REF}"
fi
if [[ -z "$base_revision" ]]; then
  for candidate in origin/main origin/master main master; do
    if git -C "$repo_root" rev-parse --verify --quiet "$candidate" >/dev/null; then
      base_revision=$candidate
      break
    fi
  done
fi
if [[ -z "$base_revision" ]] || ! git -C "$repo_root" rev-parse --verify --quiet "$base_revision" >/dev/null; then
  echo "migration upgrade test: target revision is unavailable" >&2
  exit 1
fi

target_root=""
cleanup() {
  if [[ -n "$target_root" && -d "$target_root" ]]; then
    rm -rf "$target_root"
  fi
}
trap cleanup EXIT

migration_paths=(
  backend/core/migrations
  backend/core/migrate
)
if ! git -C "$repo_root" diff --quiet "$base_revision" -- "${migration_paths[@]}"; then
  target_root=$(mktemp -d "${TMPDIR:-/tmp}/lazymind-target-migrations.XXXXXX")
  git -C "$repo_root" archive "$base_revision" backend/core/migrations |
    tar -x -C "$target_root"
  export TARGET_MIGRATIONS_DIR="$target_root/backend/core/migrations"
  echo "migration upgrade test: comparing branch against $base_revision"
else
  unset TARGET_MIGRATIONS_DIR || true
  echo "migration upgrade test: no migration diff against $base_revision; target paths skipped"
fi

cd "$repo_root/backend/core"
go test ./migrate -run '^TestRepositoryPostgresMigrationPaths$' -count=1 -v
