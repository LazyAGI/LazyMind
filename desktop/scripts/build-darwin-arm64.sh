#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_ROOT="${ROOT}/desktop/build/darwin-arm64"
RUNTIME_ROOT="${BUILD_ROOT}/runtime"
DIST_ROOT="${ROOT}/desktop/dist"
APP_ICON="${ROOT}/desktop/electron/assets/LazyMind.icns"
PACKAGE_KIND="${LAZYMIND_DESKTOP_PACKAGE_KIND:-zip}"
SIGNING_MODE="${LAZYMIND_DESKTOP_SIGNING_MODE:-adhoc}"
LAZYLLM_VERSION="${LAZYMIND_LAZYLLM_VERSION:-1.2.2}"
RELEASE_BUILD="${LAZYMIND_RELEASE_BUILD:-false}"

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

case "${PACKAGE_KIND}" in
  zip|dmg) ;;
  *)
    echo "LAZYMIND_DESKTOP_PACKAGE_KIND must be zip or dmg, got: ${PACKAGE_KIND}" >&2
    exit 2
    ;;
esac
case "${SIGNING_MODE}" in
  adhoc|developer-id|none) ;;
  *)
    echo "LAZYMIND_DESKTOP_SIGNING_MODE must be adhoc, developer-id, or none, got: ${SIGNING_MODE}" >&2
    exit 2
    ;;
esac
if [[ "${PACKAGE_KIND}" == "dmg" && "${SIGNING_MODE}" == "none" ]]; then
  echo "Refusing to create an unsigned distribution DMG" >&2
  exit 2
fi

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
  if [[ "${RELEASE_BUILD}" != "true" && ! -d "${lazyllm_source}" ]]; then
    echo "bundled LazyLLM source is required for local builds: ${lazyllm_source}" >&2
    exit 1
  fi
}

verify_runtime_code_signatures() {
  local runtime_root="$1"
  local checked=0
  local failed=0

  while IFS= read -r -d '' candidate; do
    if ! file -b "${candidate}" | grep -q "Mach-O"; then
      continue
    fi
    checked=$((checked + 1))
    if ! codesign --verify --strict "${candidate}"; then
      echo "Invalid embedded runtime signature: ${candidate}" >&2
      failed=$((failed + 1))
    fi
  done < <(
    find "${runtime_root}" -type f \
      \( -name "*.so" -o -name "*.dylib" -o -perm -111 \) -print0
  )

  echo "Verified ${checked} embedded runtime Mach-O signatures"
  if (( failed > 0 )); then
    echo "${failed} embedded runtime signatures failed verification" >&2
    return 1
  fi
}

prune_runtime_app() {
  local app_root="$1"
  if [[ -d "${app_root}/frontend" ]]; then
    find "${app_root}/frontend" -mindepth 1 -maxdepth 1 ! -name "dist" -exec rm -rf {} +
  fi
  # Developer-local virtualenvs must not ship inside the app bundle; absolute
  # interpreter symlinks break macOS sealed-resource verification.
  find "${app_root}" -type d \( -name ".venv" -o -name ".venv-test" \) -prune -exec rm -rf {} +
  if [[ "${RELEASE_BUILD}" == "true" ]]; then
    remove_generated_path "${app_root}/algorithm/lazyllm"
  else
    remove_generated_path "${app_root}/algorithm/lazyllm/docs"
  fi
  remove_generated_path "${app_root}/skills/.runtime"
  remove_generated_path "${app_root}/skills/research"
  remove_generated_path "${app_root}/skills/review"
  remove_generated_path "${app_root}/skills/search"
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
(cd "${ROOT}/local/lazymind-cli" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/lazymind" ./cmd/lazymind)
(cd "${ROOT}/local/local-proxy" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/local-proxy" ./cmd/local-proxy)
(cd "${ROOT}/backend/core" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/core" .)
(cd "${ROOT}/backend/scan-control-plane" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/scan-control-plane" ./cmd/scan-control-plane)
(cd "${ROOT}/backend/file-watcher" && "${GO_BIN}" build "${GO_BUILD_FLAGS[@]}" -o "${RUNTIME_ROOT}/bin/file-watcher" ./cmd/main.go)
GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install "${GO_INSTALL_FLAGS[@]}" github.com/f1bonacc1/process-compose@v1.116.0
GOBIN="${RUNTIME_ROOT}/bin" "${GO_BIN}" install "${GO_INSTALL_FLAGS[@]}" github.com/caddyserver/caddy/v2/cmd/caddy@v2.10.2

echo "==> Building frontend desktop dist"
(cd "${ROOT}/frontend" && CI=true VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" install --frozen-lockfile --prefer-offline)
(cd "${ROOT}/frontend" && VITE_LAZYMIND_MODE=desktop "${PNPM_BIN}" build)

if [[ "${RELEASE_BUILD}" != "true" && ! -d "${ROOT}/algorithm/lazyllm/lazyllm" ]]; then
  echo "==> Ensuring LazyLLM submodule source"
  git -C "${ROOT}" submodule update --init algorithm/lazyllm
fi

echo "==> Preparing Python runtime and venvs"
export UV_PYTHON_INSTALL_DIR="${RUNTIME_ROOT}/runtimes/python"
"${UV_BIN}" python install 3.11.15
PYTHON="$("${UV_BIN}" python find --managed-python --no-python-downloads --resolve-links 3.11.15)"
rm -rf "${RUNTIME_ROOT}/deps/python/auth-service"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/deps/python/auth-service"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/auth-service/bin/python" --link-mode copy --strict -r "${ROOT}/backend/auth-service/requirements.txt"
rm -rf "${RUNTIME_ROOT}/deps/python/channel-gateway"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/deps/python/channel-gateway"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/channel-gateway/bin/python" --link-mode copy --strict -r "${ROOT}/backend/channel-gateway/requirements.txt"
rm -rf "${RUNTIME_ROOT}/deps/python/algorithm"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/deps/python/algorithm"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/deps/python/algorithm/bin/python" --link-mode copy --strict 'setuptools<81' "lazyllm==${LAZYLLM_VERSION}"
"${RUNTIME_ROOT}/deps/python/algorithm/bin/python" -c "import importlib.metadata as m; assert m.version('lazyllm') == '${LAZYLLM_VERSION}'"
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
  --exclude "/.env" \
  --exclude "/.lazymind-local" \
  --exclude ".venv" \
  --exclude ".venv-test" \
  --exclude "/.conda" \
  --exclude "/.codex" \
  --exclude "/.claude" \
  --exclude "/.cursor" \
  --exclude "/.vscode" \
  --exclude "/data" \
  --exclude "/volumes" \
  --exclude "/local/config.env" \
  --exclude "local/build" \
  --exclude "local/runtime" \
  --exclude "desktop/build" \
  --exclude "desktop/cache" \
  --exclude "skills/.runtime" \
  --exclude "skills/research" \
  --exclude "skills/review" \
  --exclude "skills/search" \
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
  --exclude "/backend/core/core" \
  "${ROOT}/" "${RUNTIME_ROOT}/app/"

prune_runtime_app "${RUNTIME_ROOT}/app"
assert_desktop_runtime_app "${RUNTIME_ROOT}/app"

echo "==> Materializing offline Skill packages and featured catalog"
BUILTIN_SKILL_BUNDLE_ARGS=(
  run ./cmd/builtin-skill-bundle
  --sources "${ROOT}/skills/builtin-sources.yaml"
  --lock "${ROOT}/skills/builtin-skills.lock.json"
  --cache "${ROOT}/desktop/cache/builtin-skills"
  --output "${RUNTIME_ROOT}/builtin-skills"
  --featured-sources "${ROOT}/skills/featured"
  --featured-output "${RUNTIME_ROOT}/featured-skills"
)
if [[ "${RELEASE_BUILD}" == "true" ]]; then
  BUILTIN_SKILL_BUNDLE_ARGS+=(--frozen-lockfile)
fi
(cd "${ROOT}/backend/core" && "${GO_BIN}" "${BUILTIN_SKILL_BUNDLE_ARGS[@]}")

TRUSTED_LOCAL_MODE=false
if [[ "${LAZYMIND_TRUSTED_LOCAL_MODE:-}" == "true" ]]; then
  TRUSTED_LOCAL_MODE=true
  echo "==> Trusted local mode enabled for this desktop package"
fi
node "${ROOT}/desktop/scripts/write-runtime-manifest.mjs" \
  "${RUNTIME_ROOT}" --platform darwin --arch arm64 \
  --trusted-local-mode "${TRUSTED_LOCAL_MODE}"
node "${ROOT}/desktop/scripts/write-editable-ppt-dependency-config.mjs" "${RUNTIME_ROOT}"

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
export LAZYMIND_DESKTOP_PACKAGE_KIND
export LAZYMIND_DESKTOP_SIGNING_MODE
if [[ "${PACKAGE_KIND}" == "dmg" ]]; then
  (cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" run dist:mac:arm64)
else
  (cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" run pack:mac:arm64)
fi

APP_PATH="${DIST_ROOT}/mac-arm64/LazyMind.app"
ZIP_PATH="${DIST_ROOT}/LazyMind-darwin-arm64.zip"
DMG_PATH="${DIST_ROOT}/LazyMind-macos-arm64.dmg"
if [[ ! -d "${APP_PATH}" ]]; then
  if [[ -d "${DIST_ROOT}/mac-arm64" ]]; then
    APP_PATH="$(find "${DIST_ROOT}/mac-arm64" -maxdepth 3 -type d -name "LazyMind.app" -print -quit)"
  fi
fi
if [[ -d "${APP_PATH}" ]]; then
  if [[ "${SIGNING_MODE}" == "adhoc" ]]; then
    echo "==> Applying local ad-hoc signature"
    codesign --force --deep --sign - "${APP_PATH}"
  fi
  if [[ "${SIGNING_MODE}" != "none" ]]; then
    codesign --verify --deep --strict --verbose=2 "${APP_PATH}"
  fi
  if [[ "${SIGNING_MODE}" == "developer-id" ]]; then
    signature_info="$(codesign -dv --verbose=4 "${APP_PATH}" 2>&1)"
    if [[ "${signature_info}" != *"Authority=Developer ID Application:"* ]]; then
      echo "Expected a Developer ID Application signature: ${APP_PATH}" >&2
      exit 1
    fi
    verify_runtime_code_signatures "${APP_PATH}/Contents/Resources/runtime"
  fi
  if [[ "${PACKAGE_KIND}" == "zip" ]]; then
    remove_generated_path "${ZIP_PATH}"
    ditto -c -k --keepParent "${APP_PATH}" "${ZIP_PATH}"
  else
    if [[ ! -f "${DMG_PATH}" ]]; then
      echo "Expected DMG not found: ${DMG_PATH}" >&2
      exit 1
    fi
    codesign --verify --strict --verbose=2 "${DMG_PATH}"
  fi
  echo "LazyMind.app: ${APP_PATH}"
  if [[ "${PACKAGE_KIND}" == "dmg" ]]; then
    echo "DMG: ${DMG_PATH}"
  else
    echo "Zip: ${ZIP_PATH}"
  fi
else
  echo "Expected app not found: ${APP_PATH}" >&2
  exit 1
fi
