#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_ROOT="${ROOT}/.lazymind-desktop/build/darwin-arm64"
RUNTIME_ROOT="${BUILD_ROOT}/runtime"
DIST_ROOT="${ROOT}/desktop/dist"
APP_RUNTIME_ROOT="${DIST_ROOT}/runtime"

GO_BIN="${GO:-go}"
PNPM_BIN="${PNPM:-pnpm}"
UV_BIN="${UV:-uv}"

mkdir -p "${RUNTIME_ROOT}/bin" "${RUNTIME_ROOT}/app" "${RUNTIME_ROOT}/python"

echo "==> Building Go desktop runtime binaries"
(cd "${ROOT}/local/local-runtime-manager" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/local-runtime-manager" .)
(cd "${ROOT}/local/local-proxy" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/local-proxy" ./cmd/local-proxy)
(cd "${ROOT}/backend/core" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/core" .)
(cd "${ROOT}/backend/scan-control-plane" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/scan-control-plane" ./cmd/scan-control-plane)
(cd "${ROOT}/backend/file-watcher" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/file-watcher" ./cmd/main.go)
GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install github.com/f1bonacc1/process-compose@v1.116.0
GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install github.com/caddyserver/caddy/v2/cmd/caddy@v2.10.2

echo "==> Building frontend desktop dist"
(cd "${ROOT}/frontend" && VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" install --frozen-lockfile)
(cd "${ROOT}/frontend" && VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" build)

echo "==> Preparing Python runtime and venvs"
export UV_PYTHON_INSTALL_DIR="${RUNTIME_ROOT}/python/runtime"
"${UV_BIN}" python install 3.11.15
PYTHON="$("${UV_BIN}" python find --managed-python --no-python-downloads --resolve-links 3.11.15)"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/python/auth-service"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/auth-service/bin/python" --link-mode copy --strict -r "${ROOT}/backend/auth-service/requirements.txt"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/python/algorithm"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/algorithm/bin/python" --link-mode copy --strict 'setuptools<81' lazyllm
"${RUNTIME_ROOT}/python/algorithm/bin/lazyllm" install rag
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/algorithm/bin/python" --link-mode copy --strict -r "${ROOT}/algorithm/requirements.txt"

echo "==> Staging runtime app files"
rsync -a --delete \
  --exclude ".git" \
  --exclude ".lazymind-local" \
  --exclude ".lazymind-desktop" \
  --exclude "node_modules" \
  --exclude "__pycache__" \
  --exclude ".pytest_cache" \
  --exclude ".ruff_cache" \
  --exclude "desktop/dist" \
  "${ROOT}/" "${RUNTIME_ROOT}/app/"

node "${ROOT}/desktop/scripts/write-runtime-manifest.mjs" "${RUNTIME_ROOT}"

echo "==> Copying runtime into Electron resources staging"
rm -rf "${APP_RUNTIME_ROOT}"
mkdir -p "${DIST_ROOT}"
rsync -a --delete "${RUNTIME_ROOT}/" "${APP_RUNTIME_ROOT}/"

echo "==> Packaging Electron app"
(cd "${ROOT}/desktop/electron" && CI=true "${PNPM_BIN}" install --frozen-lockfile=false)
(cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" rebuild electron)
(cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" run pack:mac:arm64)

APP_PATH="${DIST_ROOT}/mac-arm64/LazyMind.app"
ZIP_PATH="${DIST_ROOT}/LazyMind-darwin-arm64.zip"
if [[ ! -d "${APP_PATH}" ]]; then
  if [[ -d "${DIST_ROOT}/mac-arm64" ]]; then
    APP_PATH="$(find "${DIST_ROOT}/mac-arm64" -maxdepth 3 -type d -name "LazyMind.app" -print -quit)"
  fi
fi
if [[ -d "${APP_PATH}" ]]; then
  ditto -c -k --keepParent "${APP_PATH}" "${ZIP_PATH}"
  echo "LazyMind.app: ${APP_PATH}"
  echo "Zip: ${ZIP_PATH}"
else
  echo "Expected app not found: ${APP_PATH}" >&2
  exit 1
fi
