#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_ROOT="${ROOT}/desktop/build/darwin-arm64"
RUNTIME_ROOT="${BUILD_ROOT}/runtime"
DIST_ROOT="${ROOT}/desktop/dist"
APP_RUNTIME_ROOT="${DIST_ROOT}/runtime"
APP_ICON="${ROOT}/desktop/electron/assets/LazyMind.icns"
SHA256SUMS_PATH="${DIST_ROOT}/SHA256SUMS.txt"
BUILD_MANIFEST_PATH="${DIST_ROOT}/LazyMind-darwin-arm64.build.json"

GO_BIN="${GO:-go}"
PNPM_BIN="${PNPM:-pnpm}"
UV_BIN="${UV:-uv}"
GO_BUILD_FLAGS=(-trimpath -buildvcs=false -ldflags="-s -w")
GO_INSTALL_FLAGS=(-trimpath -ldflags="-s -w")
SIGNING_MODE="${LAZYMIND_DESKTOP_SIGNING_MODE:-adhoc}"

: "${ELECTRON_CACHE:=${HOME}/Library/Caches/electron}"
: "${ELECTRON_BUILDER_CACHE:=${HOME}/Library/Caches/electron-builder}"
export ELECTRON_CACHE
export ELECTRON_BUILDER_CACHE
export PYTHONDONTWRITEBYTECODE=1

case "${SIGNING_MODE}" in
  adhoc|none) ;;
  *)
    echo "LAZYMIND_DESKTOP_SIGNING_MODE must be adhoc or none, got: ${SIGNING_MODE}" >&2
    exit 2
    ;;
esac

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

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sign_app() {
  local app_path="$1"
  case "${SIGNING_MODE}" in
    adhoc)
      echo "==> Applying ad-hoc code signature"
      xattr -cr "${app_path}" 2>/dev/null || true
      codesign --force --deep --sign - "${app_path}"
      codesign --verify --deep --strict --verbose=2 "${app_path}"
      ;;
    none)
      echo "==> Skipping code signing (LAZYMIND_DESKTOP_SIGNING_MODE=none)"
      ;;
  esac
}

verify_zipped_app() {
  local app_path="$1"
  local zip_path="$2"
  local temp_dir
  local extracted_app

  if [[ "${SIGNING_MODE}" == "none" ]]; then
    echo "==> Skipping archived app signature verification (LAZYMIND_DESKTOP_SIGNING_MODE=none)"
    return 0
  fi
  echo "==> Verifying archived app signature"
  temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/lazymind-archive-verify.XXXXXX")"
  if ! ditto -x -k "${zip_path}" "${temp_dir}"; then
    remove_generated_path "${temp_dir}"
    return 1
  fi
  extracted_app="${temp_dir}/$(basename "${app_path}")"
  if ! codesign --verify --deep --strict --verbose=2 "${extracted_app}"; then
    remove_generated_path "${temp_dir}"
    return 1
  fi
  remove_generated_path "${temp_dir}"
}

write_release_metadata() {
  local app_path="$1"
  local zip_path="$2"
  local zip_hash
  local git_commit
  local git_dirty

  zip_hash="$(sha256_file "${zip_path}")"
  git_commit="$(git -C "${ROOT}" rev-parse HEAD 2>/dev/null || printf 'unknown')"
  if [[ -n "$(git -C "${ROOT}" status --short 2>/dev/null)" ]]; then
    git_dirty="true"
  else
    git_dirty="false"
  fi

  {
    printf '%s  %s\n' "${zip_hash}" "$(basename "${zip_path}")"
  } > "${SHA256SUMS_PATH}"

  BUILD_MANIFEST_PATH="${BUILD_MANIFEST_PATH}" \
  ROOT="${ROOT}" \
  APP_PATH="${app_path}" \
  ZIP_PATH="${zip_path}" \
  ZIP_HASH="${zip_hash}" \
  GIT_COMMIT="${git_commit}" \
  GIT_DIRTY="${git_dirty}" \
  SIGNING_MODE="${SIGNING_MODE}" \
  node -e '
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");

function hashFileTree(root) {
  const digest = crypto.createHash("sha256");

  function walk(dir) {
    const entries = fs.readdirSync(dir).sort();
    for (const entry of entries) {
      const full = path.join(dir, entry);
      const rel = path.relative(root, full);
      const stat = fs.lstatSync(full);
      digest.update(rel);
      digest.update("\0");
      if (stat.isDirectory()) {
        digest.update("dir\0");
        walk(full);
      } else if (stat.isSymbolicLink()) {
        digest.update("symlink\0");
        digest.update(fs.readlinkSync(full));
        digest.update("\0");
      } else if (stat.isFile()) {
        digest.update("file\0");
        const fd = fs.openSync(full, "r");
        const buffer = Buffer.alloc(65536);
        try {
          let bytesRead;
          while ((bytesRead = fs.readSync(fd, buffer, 0, buffer.length, null)) > 0) {
            digest.update(buffer.subarray(0, bytesRead));
          }
        } finally {
          fs.closeSync(fd);
        }
        digest.update("\0");
      }
    }
  }

  walk(root);
  return digest.digest("hex");
}

const manifest = {
  version: 1,
  product: "LazyMind",
  platform: "darwin",
  arch: "arm64",
  builtAt: new Date().toISOString(),
  git: {
    commit: process.env.GIT_COMMIT,
    dirty: process.env.GIT_DIRTY === "true"
  },
  signing: {
    mode: process.env.SIGNING_MODE,
    note: process.env.SIGNING_MODE === "adhoc"
      ? "Ad-hoc signed for internal testing. This is not Developer ID signing or notarization."
      : "Unsigned internal testing build."
  },
  artifacts: {
    app: {
      path: path.relative(process.env.ROOT, process.env.APP_PATH),
      treeSha256: hashFileTree(process.env.APP_PATH)
    },
    zip: {
      path: path.relative(process.env.ROOT, process.env.ZIP_PATH),
      sha256: process.env.ZIP_HASH
    },
    checksums: {
      path: path.relative(process.env.ROOT, path.join(path.dirname(process.env.ZIP_PATH), "SHA256SUMS.txt"))
    }
  }
};

fs.writeFileSync(process.env.BUILD_MANIFEST_PATH, `${JSON.stringify(manifest, null, 2)}\n`);
'
}

mkdir -p \
  "${RUNTIME_ROOT}/bin" \
  "${RUNTIME_ROOT}/app" \
  "${RUNTIME_ROOT}/python" \
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
export UV_PYTHON_INSTALL_DIR="${RUNTIME_ROOT}/python/runtime"
"${UV_BIN}" python install 3.11.15
PYTHON="$("${UV_BIN}" python find --managed-python --no-python-downloads --resolve-links 3.11.15)"
rm -rf "${RUNTIME_ROOT}/python/auth-service"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/python/auth-service"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/auth-service/bin/python" --link-mode copy --strict -r "${ROOT}/backend/auth-service/requirements.txt"
rm -rf "${RUNTIME_ROOT}/python/algorithm"
"${UV_BIN}" venv --managed-python --no-python-downloads --relocatable --seed --link-mode copy --python "${PYTHON}" "${RUNTIME_ROOT}/python/algorithm"
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/algorithm/bin/python" --link-mode copy --strict 'setuptools<81' lazyllm
"${RUNTIME_ROOT}/python/algorithm/bin/lazyllm" install rag
"${UV_BIN}" pip install --python "${RUNTIME_ROOT}/python/algorithm/bin/python" --link-mode copy --strict -r "${ROOT}/algorithm/requirements.txt"
make_internal_symlinks_relative "${RUNTIME_ROOT}"
echo "==> Pruning Python runtime bytecode and test packages"
prune_python_runtime "${RUNTIME_ROOT}/python"

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
  --exclude "/frontend/src" \
  --exclude "/frontend/public" \
  --exclude "/frontend/scripts" \
  --exclude "/algorithm/lazyllm/docs" \
  --exclude "/backend/core/core" \
  "${ROOT}/" "${RUNTIME_ROOT}/app/"

prune_runtime_app "${RUNTIME_ROOT}/app"
assert_desktop_runtime_app "${RUNTIME_ROOT}/app"
node "${ROOT}/desktop/scripts/write-runtime-manifest.mjs" "${RUNTIME_ROOT}"

echo "==> Copying runtime into Electron resources staging"
remove_generated_path "${APP_RUNTIME_ROOT}"
mkdir -p "${DIST_ROOT}"
rsync -a --delete "${RUNTIME_ROOT}/" "${APP_RUNTIME_ROOT}/"

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
(cd "${ROOT}/desktop/electron" && "${PNPM_BIN}" run pack:mac:arm64)

APP_PATH="${DIST_ROOT}/mac-arm64/LazyMind.app"
ZIP_PATH="${DIST_ROOT}/LazyMind-darwin-arm64.zip"
if [[ ! -d "${APP_PATH}" ]]; then
  if [[ -d "${DIST_ROOT}/mac-arm64" ]]; then
    APP_PATH="$(find "${DIST_ROOT}/mac-arm64" -maxdepth 3 -type d -name "LazyMind.app" -print -quit)"
  fi
fi
if [[ -d "${APP_PATH}" ]]; then
  sign_app "${APP_PATH}"
  remove_generated_path "${ZIP_PATH}"
  ditto -c -k --keepParent "${APP_PATH}" "${ZIP_PATH}"
  verify_zipped_app "${APP_PATH}" "${ZIP_PATH}"
  write_release_metadata "${APP_PATH}" "${ZIP_PATH}"
  echo "LazyMind.app: ${APP_PATH}"
  echo "Zip: ${ZIP_PATH}"
  echo "Checksums: ${SHA256SUMS_PATH}"
  echo "Build manifest: ${BUILD_MANIFEST_PATH}"
  echo "Tester guide: ${ROOT}/desktop/RUN_ON_MAC.md"
else
  echo "Expected app not found: ${APP_PATH}" >&2
  exit 1
fi
