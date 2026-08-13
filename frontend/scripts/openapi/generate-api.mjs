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
  normalizeOpenApiCacheEntry,
} from "./openapi-manifest.mjs";
import {
  inspectGeneratedClientOutput,
  postProcessGeneratedClient,
} from "./generated-client-utils.mjs";

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
if (fs.existsSync(cacheFilePath)) {
  try {
    cache = JSON.parse(fs.readFileSync(cacheFilePath, "utf-8"));
  } catch {
    cache = {};
  }
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
  const cached = normalizeOpenApiCacheEntry(cache[api.name]);
  const currentOutput = inspectGeneratedClientOutput(api.output);
  const outputMatches =
    !cached.strictOutput ||
    (currentOutput.missingFiles.length === 0 &&
      currentOutput.hash === cached.outputHash);

  if (!skipCache && currentHash === cached.specHash && outputMatches) {
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
    postProcessGeneratedClient(api.output, { cwdPath });
    const generatedOutput = inspectGeneratedClientOutput(api.output);
    if (generatedOutput.missingFiles.length > 0) {
      throw new Error(
        `Generated output is incomplete: ${generatedOutput.missingFiles.join(", ")}`,
      );
    }
    cache[api.name] = {
      specHash: currentHash,
      outputHash: generatedOutput.hash,
    };
    updated = true;
  } catch (error) {
    console.error(`❌ Failed to generate API "${api.name}":`, error);
    process.exit(1);
  }
}

if (updated) {
  fs.writeFileSync(cacheFilePath, JSON.stringify(cache, null, 2));
  console.log("💾 Cache updated");
}
