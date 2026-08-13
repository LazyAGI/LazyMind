import { createHash } from "crypto";
import fs from "fs";
import path from "path";

export const GENERATED_TYPESCRIPT_FILES = Object.freeze([
  "api.ts",
  "base.ts",
  "common.ts",
  "configuration.ts",
  "index.ts",
]);

export function hashFile(filePath) {
  if (!fs.existsSync(filePath)) return "";
  return createHash("sha256").update(fs.readFileSync(filePath)).digest("hex");
}

export function inspectGeneratedClientOutput(outputDir) {
  const missingFiles = GENERATED_TYPESCRIPT_FILES.filter(
    (filename) => !fs.existsSync(path.resolve(outputDir, filename)),
  );

  if (missingFiles.length > 0) {
    return { hash: "", missingFiles };
  }

  const hash = createHash("sha256");
  for (const filename of GENERATED_TYPESCRIPT_FILES) {
    hash.update(filename);
    hash.update("\0");
    hash.update(fs.readFileSync(path.resolve(outputDir, filename)));
    hash.update("\0");
  }

  return { hash: hash.digest("hex"), missingFiles: [] };
}

function patchBasePath(outputDir, cwdPath, logger) {
  const baseTsPath = path.resolve(outputDir, "base.ts");
  if (!fs.existsSync(baseTsPath)) return;

  const original = fs.readFileSync(baseTsPath, "utf-8");
  const patched = original.replace(
    /export const BASE_PATH\s*=\s*"[^"]*"\.replace\(.*?\);/,
    'export const BASE_PATH = (typeof import.meta !== "undefined" && import.meta.env?.VITE_API_BASE_URL) ? import.meta.env.VITE_API_BASE_URL.replace(/\\/+$/, "") : "http://localhost";',
  );

  if (patched !== original) {
    fs.writeFileSync(baseTsPath, patched, "utf-8");
    logger.log(`🔧 Patched BASE_PATH in ${path.relative(cwdPath, baseTsPath)}`);
  }
}

function patchNullableRecursiveMemoryValue(outputDir, cwdPath, logger) {
  const apiTsPath = path.resolve(outputDir, "api.ts");
  if (!fs.existsSync(apiTsPath)) return;

  const original = fs.readFileSync(apiTsPath, "utf-8");
  const patched = original.replace(
    /export type CurrentMemoryDocumentValue = (?!null \| )/,
    "export type CurrentMemoryDocumentValue = null | ",
  );
  if (patched !== original) {
    fs.writeFileSync(apiTsPath, patched, "utf-8");
    logger.log(
      `🔧 Preserved nullable CurrentMemoryDocumentValue in ${path.relative(cwdPath, apiTsPath)}`,
    );
  }
}

function removeUnusedGeneratedFiles(outputDir, cwdPath, logger) {
  for (const filename of ["git_push.sh"]) {
    const filePath = path.resolve(outputDir, filename);
    if (fs.existsSync(filePath)) {
      fs.rmSync(filePath);
      logger.log(`🧹 Removed unused generated file ${path.relative(cwdPath, filePath)}`);
    }
  }
}

function normalizeGeneratedTypeScript(outputDir) {
  for (const filename of GENERATED_TYPESCRIPT_FILES) {
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

export function postProcessGeneratedClient(
  outputDir,
  { cwdPath = process.cwd(), logger = console } = {},
) {
  patchBasePath(outputDir, cwdPath, logger);
  patchNullableRecursiveMemoryValue(outputDir, cwdPath, logger);
  removeUnusedGeneratedFiles(outputDir, cwdPath, logger);
  normalizeGeneratedTypeScript(outputDir);
}
