#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
package_manager="$(node -p 'require(process.argv[1]).packageManager' "$project_root/frontend/package.json")"
case "$package_manager" in
  pnpm@*) ;;
  *) echo "Expected frontend/package.json to declare pnpm@<version>" >&2; exit 1 ;;
esac

exec npx --yes "$package_manager" "$@"
