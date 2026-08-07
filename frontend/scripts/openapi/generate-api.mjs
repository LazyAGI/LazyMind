/**
 * Generate API clients from OpenAPI specs.
 * Local specs live in scripts/openapi/specs.
 * Output: src/api/generated/<name>-client
 */
import { execSync } from "child_process";
import path from "path";
import fs from "fs";
import { fileURLToPath } from "url";
import {
  getOpenApiApis,
  getOpenApiCacheFilePath,
  hashFile,
} from "./openapi-manifest.mjs";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const cwdPath = process.cwd();
const apis = getOpenApiApis(cwdPath);

const args = process.argv.slice(2);
const flags = new Set(args.filter((arg) => arg.startsWith("--")));
const positional = args.filter((arg) => !arg.startsWith("--"));
const skipCache = flags.has("--skip-cache");
const target = positional[0];

const selectedApis = target ? apis.filter((api) => api.name === target) : apis;

if (target && selectedApis.length === 0) {
  console.error(
    `❌ API "${target}" not found. Available: ${apis.map((a) => a.name).join(", ")}`,
  );
  process.exit(1);
}

const cacheFilePath = getOpenApiCacheFilePath(cwdPath);
let cache = {};
if (!skipCache && fs.existsSync(cacheFilePath)) {
  cache = JSON.parse(fs.readFileSync(cacheFilePath, "utf-8"));
}

function resolveOpenApiGeneratorCommand() {
  const binaryName =
    process.platform === "win32"
      ? "openapi-generator-cli.cmd"
      : "openapi-generator-cli";
  const localBinary = path.resolve(cwdPath, "node_modules", ".bin", binaryName);

  if (fs.existsSync(localBinary)) {
    return `"${localBinary}"`;
  }

  if (commandExists("pnpm")) {
    return "pnpm exec openapi-generator-cli";
  }

  if (commandExists("npm")) {
    return "npm exec -- openapi-generator-cli";
  }

  if (commandExists("npx")) {
    return "npx --no-install openapi-generator-cli";
  }

  console.error(
    "❌ openapi-generator-cli not found. Run `npm install` in frontend first, or install pnpm/npm/npx on PATH.",
  );
  process.exit(1);
}

function commandExists(command) {
  try {
    execSync(`${process.platform === "win32" ? "where" : "command -v"} ${command}`, {
      stdio: "ignore",
      cwd: cwdPath,
    });
    return true;
  } catch {
    return false;
  }
}

/**
 * Patch generated base.ts to use VITE_API_BASE_URL env variable instead of
 * the hardcoded "http://localhost" that the OpenAPI generator emits by default.
 */
function patchBasePath(outputDir) {
  const baseTsPath = path.resolve(outputDir, 'base.ts');
  if (!fs.existsSync(baseTsPath)) return;

  const original = fs.readFileSync(baseTsPath, 'utf-8');
  const patched = original.replace(
    /export const BASE_PATH\s*=\s*"[^"]*"\.replace\(.*?\);/,
    'export const BASE_PATH = (typeof import.meta !== "undefined" && import.meta.env?.VITE_API_BASE_URL) ? import.meta.env.VITE_API_BASE_URL.replace(/\\/+$/, "") : "http://localhost";',
  );

  if (patched !== original) {
    fs.writeFileSync(baseTsPath, patched, 'utf-8');
    console.log(`🔧 Patched BASE_PATH in ${path.relative(cwdPath, baseTsPath)}`);
  }
}

function patchNullableRecursiveMemoryValue(outputDir) {
  const apiTsPath = path.resolve(outputDir, "api.ts");
  if (!fs.existsSync(apiTsPath)) return;

  const original = fs.readFileSync(apiTsPath, "utf-8");
  const patched = original.replace(
    /export type CurrentMemoryDocumentValue = (?!null \| )/,
    "export type CurrentMemoryDocumentValue = null | ",
  );
  if (patched !== original) {
    fs.writeFileSync(apiTsPath, patched, "utf-8");
    console.log(
      `🔧 Preserved nullable CurrentMemoryDocumentValue in ${path.relative(cwdPath, apiTsPath)}`,
    );
  }
}

function removeUnusedGeneratedFiles(outputDir) {
  const unusedFiles = ["git_push.sh"];

  for (const filename of unusedFiles) {
    const filePath = path.resolve(outputDir, filename);
    if (fs.existsSync(filePath)) {
      fs.rmSync(filePath);
      console.log(`🧹 Removed unused generated file ${path.relative(cwdPath, filePath)}`);
    }
  }
}

function normalizeGeneratedTypeScript(outputDir) {
  for (const filename of [
    "api.ts",
    "base.ts",
    "common.ts",
    "configuration.ts",
    "index.ts",
  ]) {
    const filePath = path.resolve(outputDir, filename);
    if (!fs.existsSync(filePath)) continue;

    const original = fs.readFileSync(filePath, "utf-8");
    const normalized = `${original
      .replace(/[ \t]+$/gm, "")
      .replace(/[\r\n]+$/u, "")}\n`;
    if (normalized !== original) {
      fs.writeFileSync(filePath, normalized, "utf-8");
    }
  }
}

let updated = false;
const openApiGeneratorCommand = resolveOpenApiGeneratorCommand();
for (const api of selectedApis) {
  if (!fs.existsSync(api.input)) {
    console.warn(
      `⚠️ ${api.name}: Input not found at ${api.input}, skipping. Run from workspace or copy specs to api/specs/`,
    );
    continue;
  }
  const currentHash = hashFile(api.input);
  const prevHash = cache[api.name];

  if (!skipCache && currentHash === prevHash) {
    console.log(`✅ ${api.name}: No changes detected.`);
    continue;
  }

  console.log(`🔁 ${api.name}: Regenerating...`);
  fs.mkdirSync(api.output, { recursive: true });

  try {
    execSync(
      `${openApiGeneratorCommand} generate --skip-validate-spec -c scripts/openapi/openapi-generator-config.json -i "${api.input}" -o "${api.output}"`,
      { stdio: "inherit", cwd: cwdPath },
    );
    patchBasePath(api.output);
    patchNullableRecursiveMemoryValue(api.output);
    removeUnusedGeneratedFiles(api.output);
    normalizeGeneratedTypeScript(api.output);
    cache[api.name] = currentHash;
    updated = true;
  } catch (error) {
    console.error(`❌ Failed to generate API "${api.name}":`, error);
    process.exit(1);
  }
}

if (!skipCache && updated) {
  fs.writeFileSync(cacheFilePath, JSON.stringify(cache, null, 2));
  console.log("💾 Cache updated");
}
