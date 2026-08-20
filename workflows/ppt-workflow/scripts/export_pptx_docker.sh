#!/usr/bin/env bash
# One-shot editable HTML→PPTX export via the official Playwright image.
#
# Usage:
#   ./workflows/ppt-workflow/scripts/export_pptx_docker.sh /abs/path/to/deck_dir
#
# Env overrides:
#   PPT_PLAYWRIGHT_IMAGE   default mcr.microsoft.com/playwright:v1.58.2-noble
#   PPT_EXPORT_SRC         default <repo>/workflows/ppt-workflow/runtime/scripts/export_pptx
#
# After compose is up you can instead:
#   curl -sS -X POST http://localhost:8099/export -H 'Content-Type: application/json' \
#     -d "{\"deck_dir\":\"/data/subagent/<user>/ppt_sessions/<conversation>/ppt_decks/<deck_id>\"}"
set -euo pipefail

DECK_DIR="${1:-}"
if [[ -z "${DECK_DIR}" ]]; then
  echo "usage: $0 <deck_dir>" >&2
  exit 2
fi
DECK_DIR="$(cd "${DECK_DIR}" && pwd)"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
EXPORT_SRC="${PPT_EXPORT_SRC:-${REPO_ROOT}/workflows/ppt-workflow/runtime/scripts/export_pptx}"
IMAGE="${PPT_PLAYWRIGHT_IMAGE:-mcr.microsoft.com/playwright:v1.58.2-noble}"

if [[ ! -d "${EXPORT_SRC}" ]]; then
  echo "export_pptx missing: ${EXPORT_SRC}" >&2
  exit 1
fi
if [[ ! -f "${DECK_DIR}/task_pack.json" ]]; then
  echo "not a deck dir (missing task_pack.json): ${DECK_DIR}" >&2
  exit 1
fi

echo "[ppt-export] image=${IMAGE}" >&2
echo "[ppt-export] deck=${DECK_DIR}" >&2
echo "[ppt-export] export_src=${EXPORT_SRC}" >&2

docker run --rm \
  -v "${EXPORT_SRC}:/export:rw" \
  -v ppt-export-node-modules:/export/node_modules \
  -v "${DECK_DIR}:/deck:rw" \
  -w /export \
  "${IMAGE}" \
  bash -lc 'set -euo pipefail
    if [[ ! -d node_modules/pptxgenjs || ! -d node_modules/playwright ]]; then
      npm install --omit=dev
    fi
    node html_to_pptx.mjs --deck-dir /deck --force
  '
