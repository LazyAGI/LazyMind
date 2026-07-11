#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_ROOT="${ROOT}/desktop/build/darwin-arm64"
RUNTIME_ROOT="${BUILD_ROOT}/runtime"
DIST_ROOT="${ROOT}/desktop/dist"
APP_ICON="${ROOT}/desktop/electron/assets/LazyMind.icns"

GO_BIN="${GO:-go}"
PNPM_BIN="${PNPM:-pnpm}"
UV_BIN="${UV:-uv}"
GO_BUILD_FLAGS=(-trimpath -buildvcs=false -ldflags="-s -w")
GO_INSTALL_FLAGS=(-trimpath -ldflags="-s -w")

: "${ELECTRON_CACHE:=${HOME}/Library/Caches/electron}"
: "${ELECTRON_BUILDER_CACHE:=${HOME}/Library/Caches/electron-builder}"
export ELECTRON_CACHE
export ELECTRON_BUILDER_CACHE
export PYTHONDONTWRITEBYTECODE=1

remove_generated_path() {
  local target="$1"
  if [[ -e "${target}" ]]; then
    chflags -R nouchg,noschg,nohidden "${target}" 2>/dev/null || true
    xattr -cr "${target}" 2>/dev/null || true
    find "${target}" -type d -exec chmod u+rwx {} + 2>/dev/null || true
    find "${target}" -type f -exec chmod u+rw {} + 2>/dev/null || true
    find "${target}" -name ".DS_Store" -exec rm -f {} + 2>/dev/null || true
    chmod -R u+w "${target}" 2>/dev/null || true
    rm -rf "${target}"
  fi
}

make_internal_symlinks_relative() {
  local root="$1"
  find "${root}" -type l -print | while IFS= read -r link; do
    local target
    target="$(readlink "${link}")"
    case "${target}" in
      "${root}/"*)
        local relative_target
        relative_target="$(
          node -e 'const path = require("path"); const [link, target] = process.argv.slice(-2); console.log(path.relative(path.dirname(link), target) || ".")' \
            "${link}" \
            "${target}"
        )"
        ln -snf "${relative_target}" "${link}"
        ;;
    esac
  done
}

prune_python_runtime() {
  local root="$1"
  find "${root}" -type d -name "__pycache__" -prune -exec rm -rf {} +
  find "${root}" -type f \( -name "*.pyc" -o -name "*.pyo" \) -delete
  find "${root}" -type d \( -name "test" -o -name "tests" \) -prune -exec rm -rf {} +
}

assert_desktop_runtime_app() {
  local app_root="$1"
  local frontend_dist="${app_root}/frontend/dist/index.html"
  local lazyllm_source="${app_root}/algorithm/lazyllm/lazyllm"
  if [[ ! -f "${frontend_dist}" ]]; then
    echo "desktop frontend dist is required: ${frontend_dist}" >&2
    exit 1
  fi
  if [[ ! -d "${lazyllm_source}" ]]; then
    echo "bundled LazyLLM source is required: ${lazyllm_source}" >&2
    exit 1
  fi
}

prune_runtime_app() {
  local app_root="$1"
  if [[ -d "${app_root}/frontend" ]]; then
    find "${app_root}/frontend" -mindepth 1 -maxdepth 1 ! -name "dist" -exec rm -rf {} +
  fi
  remove_generated_path "${app_root}/algorithm/lazyllm/docs"
  remove_generated_path "${app_root}/backend/core/core"
}

mkdir -p \
  "${RUNTIME_ROOT}/bin" \
  "${RUNTIME_ROOT}/app" \
  "${RUNTIME_ROOT}/runtimes/python" \
  "${RUNTIME_ROOT}/runtimes/node" \
  "${RUNTIME_ROOT}/deps/python" \
  "${RUNTIME_ROOT}/deps/node" \
  "${ELECTRON_CACHE}" \
  "${ELECTRON_BUILDER_CACHE}"

echo "==> Building Go desktop runtime binaries"
(cd "${ROOT}/local/local-runtime-manager" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/local-runtime-manager" .)
(cd "${ROOT}/local/local-proxy" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/local-proxy" ./cmd/local-proxy)
(cd "${ROOT}/backend/core" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/core" .)
(cd "${ROOT}/backend/scan-control-plane" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/scan-control-plane" ./cmd/scan-control-plane)
(cd "${ROOT}/backend/file-watcher" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/file-watcher" ./cmd/main.go)
GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install "${GO_INSTALL_FLAGS[@]}" github.com/f1bonacc1/process-compose@v1.116.0
GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install "${GO_INSTALL_FLAGS[@]}" github.com/caddyserver/caddy/v2/cmd/caddy@v2.10.2

echo "==> Building frontend desktop dist"
(cd "${ROOT}/frontend" && CI=true VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" install --frozen-lockfile --prefer-offline)
(cd "${ROOT}/frontend" && VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" build)

echo "==> Ensuring LazyLLM submodule source"
if [[ ! -d "${ROOT}/algorithm/lazyllm/lazyllm" ]]; then
  git -C "${ROOT}" submodule update --init algorithm/lazyllm
fi
if [[ ! -d "${ROOT}/algorithm/lazyllm/lazyllm" ]]; then
  echo "algorithm/lazyllm submodule is required for desktop packaging" >&2
  exit 1
fi

echo "==> Preparing Python runtime and venvs"
export UV_PYTHON_INSTALL_DIR="${RUNTIME_ROOT}/runtimes/python"
"${UV_BIN}" python install 3.11.15
PYTHON="$("${UV_BIN}" python find --managed-python --no-python-downloads --resolve-links 3.11.15)"
rm -rf "${RUNTIME_ROOT}/deps/python/auth-service"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/deps/python/auth-service"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/auth-service/bin/python" --link-mode copy --strict -r "${ROOT}/backend/auth-service/requirements.txt"
rm -rf "${RUNTIME_ROOT}/deps/python/algorithm"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/deps/python/algorithm"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/algorithm/bin/python" --link-mode copy --strict 'setuptools<81' lazyllm
"${RUNTIME_ROOT}/deps/python/algorithm/bin/lazyllm" install rag
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/algorithm/bin/python" --link-mode copy --strict -r "${ROOT}/algorithm/requirements.txt"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/algorithm/bin/python" --link-mode copy --strict -r "${ROOT}/algorithm/requirements-local.txt"
make_internal_symlinks_relative "${RUNTIME_ROOT}"
echo "==> Pruning Python runtime bytecode and test packages"
prune_python_runtime "${RUNTIME_ROOT}/runtimes/python"
prune_python_runtime "${RUNTIME_ROOT}/deps/python"

echo "==> Staging runtime app files"
rsync -a --delete \
  --exclude ".git" \
  --exclude "local/build" \
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
  --exclude "/frontend/src" \
  --exclude "/frontend/public" \
  --exclude "/frontend/scripts" \
  --exclude "/algorithm/lazyllm/docs" \
  --exclude "/backend/core/core" \
  "${ROOT}/" "${RUNTIME_ROOT}/app/"

prune_runtime_app "${RUNTIME_ROOT}/app"
assert_desktop_runtime_app "${RUNTIME_ROOT}/app"
node "${ROOT}/desktop/scripts/write-runtime-manifest.mjs" \
  "${RUNTIME_ROOT}" --platform darwin --arch arm64

echo "==> Packaging Electron app"
if [[ ! -f "${APP_ICON}" ]]; then
  echo "App icon not found: ${APP_ICON}" >&2
  exit 1
fi
(cd "${ROOT}/desktop/electron" && CI=true "${PNPM_BIN}" install --frozen-lockfile=false --prefer-offline)
if ! (cd "${ROOT}/desktop/electron" && node -e 'require("electron")' >/dev/null 2>&1); then
  (cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" rebuild electron)
fi
remove_generated_path "${DIST_ROOT}/mac-arm64/LazyMind.app"
export LAZYMIND_DESKTOP_RUNTIME_STAGE="${RUNTIME_ROOT}"
export LAZYMIND_DESKTOP_OUTPUT_DIR="${DIST_ROOT}"
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
