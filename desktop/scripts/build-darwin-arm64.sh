#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_ROOT="${ROOT}/desktop/build/darwin-arm64"
RUNTIME_ROOT="${BUILD_ROOT}/runtime"
DIST_ROOT="${ROOT}/desktop/dist"
APP_RUNTIME_ROOT="${DIST_ROOT}/runtime"
DESKTOP_CACHE_ROOT="${LAZYMIND_DESKTOP_CACHE_ROOT:-${ROOT}/desktop/cache}"

GO_BIN="${GO:-go}"
PNPM_BIN="${PNPM:-pnpm}"
UV_BIN="${UV:-uv}"

GO_CACHE_DIR="${DESKTOP_CACHE_ROOT}/go/build"
GO_MOD_CACHE_DIR="${DESKTOP_CACHE_ROOT}/go/mod"
UV_CACHE_DIR="${DESKTOP_CACHE_ROOT}/uv"
PIP_CACHE_DIR="${DESKTOP_CACHE_ROOT}/pip"
PNPM_STORE_DIR="${DESKTOP_CACHE_ROOT}/pnpm-store"
ELECTRON_CACHE="${DESKTOP_CACHE_ROOT}/electron"
ELECTRON_BUILDER_CACHE="${DESKTOP_CACHE_ROOT}/electron-builder"
MARKER_DIR="${DESKTOP_CACHE_ROOT}/markers"

export GOCACHE="${GO_CACHE_DIR}"
export GOMODCACHE="${GO_MOD_CACHE_DIR}"
export UV_CACHE_DIR
export PIP_CACHE_DIR
export ELECTRON_CACHE
export ELECTRON_BUILDER_CACHE

mkdir -p \
  "${RUNTIME_ROOT}/bin" \
  "${RUNTIME_ROOT}/app" \
  "${RUNTIME_ROOT}/python" \
  "${GO_CACHE_DIR}" \
  "${GO_MOD_CACHE_DIR}" \
  "${UV_CACHE_DIR}" \
  "${PIP_CACHE_DIR}" \
  "${PNPM_STORE_DIR}" \
  "${ELECTRON_CACHE}" \
  "${ELECTRON_BUILDER_CACHE}" \
  "${MARKER_DIR}"

install_go_tool() {
  local name="$1"
  local package="$2"
  local version="$3"
  local output="$4"
  local marker="${MARKER_DIR}/go-${name}-${version}.done"
  if [[ -x "${output}" && -f "${marker}" ]]; then
    return
  fi
  GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install "${package}@${version}"
  "${GO_BIN}" version >"${marker}"
}

echo "==> Building Go desktop runtime binaries"
(cd "${ROOT}/local/local-runtime-manager" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/local-runtime-manager" .)
(cd "${ROOT}/local/local-proxy" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/local-proxy" ./cmd/local-proxy)
(cd "${ROOT}/backend/core" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/core" .)
(cd "${ROOT}/backend/scan-control-plane" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/scan-control-plane" ./cmd/scan-control-plane)
(cd "${ROOT}/backend/file-watcher" && "${GO_BIN}" build -buildvcs=false -o "${RUNTIME_ROOT}/bin/file-watcher" ./cmd/main.go)
install_go_tool "process-compose" "github.com/f1bonacc1/process-compose" "v1.116.0" "${RUNTIME_ROOT}/bin/process-compose"
install_go_tool "caddy" "github.com/caddyserver/caddy/v2/cmd/caddy" "v2.10.2" "${RUNTIME_ROOT}/bin/caddy"

echo "==> Building frontend desktop dist"
(cd "${ROOT}/frontend" && VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" install --frozen-lockfile --prefer-offline --store-dir "${PNPM_STORE_DIR}")
(cd "${ROOT}/frontend" && VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" build)

echo "==> Preparing Python runtime and venvs"
export UV_PYTHON_INSTALL_DIR="${RUNTIME_ROOT}/python/runtime"
"${UV_BIN}" python install 3.11.15
PYTHON="$("${UV_BIN}" python find --managed-python --no-python-downloads --resolve-links 3.11.15)"
AUTH_REQ_HASH="$(shasum -a 256 "${ROOT}/backend/auth-service/requirements.txt" | awk '{print $1}')"
AUTH_MARKER="${MARKER_DIR}/auth-service-python-3.11.15-${AUTH_REQ_HASH}.done"
if [[ ! -x "${RUNTIME_ROOT}/python/auth-service/bin/python" || ! -f "${AUTH_MARKER}" ]]; then
  rm -rf "${RUNTIME_ROOT}/python/auth-service"
  "${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/python/auth-service"
  "${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/auth-service/bin/python" --link-mode copy --strict -r "${ROOT}/backend/auth-service/requirements.txt"
  rm -f "${MARKER_DIR}"/auth-service-python-*.done
  touch "${AUTH_MARKER}"
fi
ALGO_REQ_HASH="$(shasum -a 256 "${ROOT}/algorithm/requirements.txt" | awk '{print $1}')"
ALGO_MARKER="${MARKER_DIR}/algorithm-python-3.11.15-${ALGO_REQ_HASH}.done"
if [[ ! -x "${RUNTIME_ROOT}/python/algorithm/bin/python" || ! -f "${ALGO_MARKER}" ]]; then
  rm -rf "${RUNTIME_ROOT}/python/algorithm"
  "${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/python/algorithm"
  "${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/algorithm/bin/python" --link-mode copy --strict 'setuptools<81' lazyllm
  "${RUNTIME_ROOT}/python/algorithm/bin/lazyllm" install rag
  "${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/algorithm/bin/python" --link-mode copy --strict -r "${ROOT}/algorithm/requirements.txt"
  rm -f "${MARKER_DIR}"/algorithm-python-*.done
  touch "${ALGO_MARKER}"
fi

echo "==> Staging runtime app files"
rsync -a --delete \
  --exclude ".git" \
  --exclude "local/runtime" \
  --exclude "desktop/build" \
  --exclude "desktop/cache" \
  --exclude "node_modules" \
  --exclude "__pycache__" \
  --exclude ".pytest_cache" \
  --exclude ".ruff_cache" \
  --exclude ".codex-gocache" \
  --exclude ".codex-gomodcache" \
  --exclude ".pnpm-store" \
  --exclude ".cache" \
  --exclude "desktop/dist" \
  "${ROOT}/" "${RUNTIME_ROOT}/app/"

node "${ROOT}/desktop/scripts/write-runtime-manifest.mjs" "${RUNTIME_ROOT}"

echo "==> Copying runtime into Electron resources staging"
rm -rf "${APP_RUNTIME_ROOT}"
mkdir -p "${DIST_ROOT}"
rsync -a --delete "${RUNTIME_ROOT}/" "${APP_RUNTIME_ROOT}/"

echo "==> Packaging Electron app"
(cd "${ROOT}/desktop/electron" && CI=true "${PNPM_BIN}" install --frozen-lockfile=false --prefer-offline --store-dir "${PNPM_STORE_DIR}")
if ! (cd "${ROOT}/desktop/electron" && node -e 'require("electron")' >/dev/null 2>&1); then
  (cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" rebuild electron)
fi
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
