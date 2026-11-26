#!/usr/bin/env bash
# Build the in-place macOS update test package in a private checkout.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
if [[ $# != 2 || "$(uname -s)" != Darwin || ! "$1" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ || "$2" != /* ]]; then
  echo "Usage: build-unsigned-update-test.sh MAJOR.MINOR.PATCH /absolute/new-output-directory (native macOS only)" >&2
  exit 2
fi
version="$1"
out_parent="$(cd "$(dirname "$2")" && pwd -P)"
output="${out_parent}/$(basename "$2")"
if [[ -e "${output}" || -L "${output}" || "${output}" == "${root}" || "${output}" == "${root}/"* ]]; then
  echo "The test output must be a fresh directory outside the source checkout" >&2
  exit 2
fi
: "${LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON:?Configure the public unsigned-test update configuration}"
: "${LAZYMIND_UPDATE_BASE_URL:?Configure the isolated update artifact base URL}"
: "${LAZYMIND_UPDATE_MINIMUM_SYSTEM_VERSION:?Configure the minimum macOS system version}"
if [[ ! -d "${root}/algorithm/lazyllm/lazyllm" ]]; then
  echo "Initialize the LazyLLM submodule before building the in-place test app" >&2
  exit 2
fi
if [[ -e "${root}/desktop/scripts/.unsigned-update-test.local.json" ]]; then
  echo "Run this wrapper from an ordinary checkout" >&2
  exit 2
fi

umask 077
stage="$(mktemp -d "${out_parent}/.lazymind-unsigned-v2.XXXXXXXX")"
trap 'rm -rf -- "${stage}"' EXIT
# Copy only checkout files, including initialized submodules. Omit Git
# credentials, local caches, ignored files and anything from a previous build.
(cd "${root}" && git ls-files -z --recurse-submodules | tar --null -T - -cf -) | tar -xf - -C "${stage}"
component_base="$(node - "${stage}" "${version}" <<'NODE'
const fs = require("node:fs");
const path = require("node:path");
const [root, version] = process.argv.slice(2);
const build = require(path.join(root, "desktop/scripts/unsigned-update-build.cjs"));
const config = build.validateUnsignedTestConfig(JSON.parse(process.env.LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON));
for (const field of ["version", "releaseId", "platform", "arch"]) {
  if (Object.hasOwn(config, field)) throw new Error(`Test build derives ${field}; omit it from the public test configuration`);
}
const base = build.inPlaceArtifactBase(process.env.LAZYMIND_UPDATE_BASE_URL, config);
const expectedComponents = new URL("RAG/", base).href;
if (process.env.LAZYMIND_PYTHON_COMPONENT_BASE_URL && process.env.LAZYMIND_PYTHON_COMPONENT_BASE_URL !== expectedComponents) {
  throw new Error("Test components must use the RAG directory under the isolated update artifact prefix");
}
const manifest = path.join(root, "desktop/electron/package.json");
const packageInfo = JSON.parse(fs.readFileSync(manifest, "utf8"));
packageInfo.version = version;
fs.writeFileSync(manifest, JSON.stringify(packageInfo, null, 2) + "\n");
fs.writeFileSync(build.filename, JSON.stringify(config) + "\n", { flag: "wx" });
process.stdout.write(expectedComponents);
NODE
)"

# The build never needs the publication key or any upload credentials.
unset LAZYMIND_UPDATE_POLICY_PRIVATE_KEY UPDATE_PRIVATE_KEY MODELSCOPE_UPDATE_TOKEN MODELSCOPE_API_TOKEN
unset LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON LAZYMIND_DESKTOP_UPDATE_CONFIG
unset LAZYMIND_CLOUD_BASE_URL LAZYMIND_CLOUD_OAUTH_CALLBACK_MODE LAZYMIND_TRUSTED_LOCAL_MODE
export LAZYMIND_PYTHON_COMPONENT_BASE_URL="${component_base}"
export LAZYMIND_DESKTOP_DEFER_PYTHON=true LAZYMIND_DESKTOP_DEFER_CLUSTER=true
export LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS=true LAZYMIND_RELEASE_BUILD=false
export LAZYMIND_DESKTOP_SIGNING_MODE=adhoc LAZYMIND_DESKTOP_PACKAGE_KIND=zip
bash "${stage}/desktop/scripts/build-darwin-arm64.sh"

target="${LAZYMIND_DESKTOP_MAC_ARCH:-arm64}"
if [[ "${target}" == x64 ]]; then
  app_directory=mac
  component_arch=amd64
else
  app_directory=mac-arm64
  component_arch=arm64
fi
dist="${stage}/desktop/dist"
node "${stage}/desktop/scripts/prepare-update-release.mjs" \
  --resources "${dist}/${app_directory}/LazyMind.app/Contents/Resources" \
  --package "${dist}/LazyMind-darwin-${target}.zip" \
  --base-url "${LAZYMIND_UPDATE_BASE_URL}" --minimum-system-version "${LAZYMIND_UPDATE_MINIMUM_SYSTEM_VERSION}" \
  --output "${stage}/publication"
cp -R "${dist}/python-components/darwin-${component_arch}" "${stage}/publication/python-components"
if [[ -e "${output}" || -L "${output}" ]]; then
  echo "The test output appeared during the build" >&2
  exit 2
fi
mv "${stage}/publication" "${output}"
