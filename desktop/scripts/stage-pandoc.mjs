#!/usr/bin/env node

import { execFile as execFileCallback } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import {
  chmod,
  copyFile,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  stat,
} from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import { promisify } from "node:util";
import { fileURLToPath, pathToFileURL } from "node:url";

const execFile = promisify(execFileCallback);
const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const defaultConfigPath = path.join(scriptsDir, "..", "dependencies", "pandoc.json");
const defaultCacheRoot = path.join(scriptsDir, "..", "cache", "dependencies", "pandoc");
const mirrorURLOverrideEnv = "LAZYMIND_PANDOC_MIRROR_URL";

function safeRelativePath(value, label) {
  if (typeof value !== "string" || !value || path.isAbsolute(value)) {
    throw new Error(`Pandoc ${label} must be a non-empty relative path`);
  }
  const normalized = path.normalize(value);
  if (normalized === ".." || normalized.startsWith(`..${path.sep}`)) {
    throw new Error(`Pandoc ${label} escapes its root: ${value}`);
  }
  return normalized;
}

function validateURLs(values, label) {
  if (!Array.isArray(values)) {
    throw new Error(`Pandoc ${label} must be an array`);
  }
  return values.map((value) => {
    const parsed = new URL(value);
    if (!new Set(["https:", "http:"]).has(parsed.protocol)) {
      throw new Error(`Pandoc download URL must use HTTP(S): ${value}`);
    }
    return parsed.toString();
  });
}

export function resolvePandocTarget(config, target, environment = process.env) {
  if (config?.schemaVersion !== 1 || config?.name !== "pandoc") {
    throw new Error("unsupported Pandoc dependency configuration");
  }
  if (!/^\d+(?:\.\d+)+$/.test(config.version || "")) {
    throw new Error("Pandoc dependency version is invalid");
  }
  const selected = config.targets?.[target];
  if (!selected) {
    throw new Error(`unsupported Pandoc desktop target: ${target}`);
  }
  if (!/^[a-f0-9]{64}$/.test(selected.sha256 || "")) {
    throw new Error(`Pandoc SHA-256 is invalid for ${target}`);
  }
  if (!selected.fileName || path.basename(selected.fileName) !== selected.fileName) {
    throw new Error(`Pandoc fileName is invalid for ${target}`);
  }
  const override = String(environment[mirrorURLOverrideEnv] || "").trim();
  const urls = [
    ...(override ? validateURLs([override], mirrorURLOverrideEnv) : []),
    ...validateURLs(selected.domesticUrls || [], `${target}.domesticUrls`),
    ...validateURLs(selected.upstreamUrls || [], `${target}.upstreamUrls`),
  ];
  if (urls.length === 0) {
    throw new Error(`Pandoc has no download URL for ${target}`);
  }
  return {
    ...selected,
    archivePath: safeRelativePath(selected.archivePath, "archivePath"),
    runtimePath: safeRelativePath(selected.runtimePath, "runtimePath"),
    target,
    urls: [...new Set(urls)],
    version: config.version,
  };
}

export async function sha256File(filePath) {
  const hash = createHash("sha256");
  await pipeline(createReadStream(filePath), new Transform({
    transform(chunk, _encoding, callback) {
      hash.update(chunk);
      callback();
    },
  }));
  return hash.digest("hex");
}

async function fileMatches(filePath, expectedSHA256) {
  try {
    const info = await stat(filePath);
    return info.isFile() && await sha256File(filePath) === expectedSHA256;
  } catch (error) {
    if (error?.code === "ENOENT") return false;
    throw error;
  }
}

async function downloadOnce(url, destination) {
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok || !response.body) {
    throw new Error(`HTTP ${response.status}`);
  }
  await pipeline(Readable.fromWeb(response.body), createWriteStream(destination, { flags: "wx" }));
}

async function downloadWithFallback(urls, destination, expectedSHA256, attempts = 2) {
  const failures = [];
  for (const url of urls) {
    for (let attempt = 1; attempt <= attempts; attempt += 1) {
      await rm(destination, { force: true });
      try {
        await downloadOnce(url, destination);
        const actual = await sha256File(destination);
        if (actual !== expectedSHA256) {
          throw new Error(`SHA-256 mismatch: got ${actual}, want ${expectedSHA256}`);
        }
        return url;
      } catch (error) {
        failures.push(`${url} attempt ${attempt}: ${error.message}`);
        await rm(destination, { force: true });
        if (attempt < attempts) {
          await new Promise((resolve) => setTimeout(resolve, attempt * 1000));
        }
      }
    }
  }
  throw new Error(`Pandoc download failed:\n${failures.join("\n")}`);
}

async function defaultExtractZip(archivePath, destination, platform = process.platform) {
  if (platform === "darwin") {
    await execFile("/usr/bin/ditto", ["-x", "-k", archivePath, destination]);
    return;
  }
  if (platform === "win32") {
    const command = "param($archive,$destination) Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force";
    await execFile("powershell.exe", [
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      command,
      archivePath,
      destination,
    ]);
    return;
  }
  throw new Error(`Pandoc ZIP extraction is unsupported on ${platform}`);
}

function expectedPlatform(target) {
  if (target.startsWith("darwin-")) return "darwin";
  if (target.startsWith("windows-")) return "win32";
  return "";
}

export async function stagePandoc(runtimeRoot, target, options = {}) {
  if (!runtimeRoot) throw new Error("runtime root is required");
  if (!target) throw new Error("Pandoc desktop target is required");
  const platform = options.platform || process.platform;
  if (!options.allowCrossPlatform && expectedPlatform(target) !== platform) {
    throw new Error(`Pandoc target ${target} cannot be staged on ${platform}`);
  }

  const configPath = options.configPath || defaultConfigPath;
  const cacheRoot = options.cacheRoot || defaultCacheRoot;
  const config = JSON.parse(await readFile(configPath, "utf8"));
  const selected = resolvePandocTarget(config, target, options.environment || process.env);
  await mkdir(cacheRoot, { recursive: true });
  await mkdir(runtimeRoot, { recursive: true });

  const cachePath = path.join(cacheRoot, `${selected.sha256}-${selected.fileName}`);
  if (!(await fileMatches(cachePath, selected.sha256))) {
    await rm(cachePath, { force: true });
    const temporaryDownload = `${cachePath}.${process.pid}.tmp`;
    await downloadWithFallback(
      selected.urls,
      temporaryDownload,
      selected.sha256,
      options.attempts || 2,
    );
    await rename(temporaryDownload, cachePath);
  }

  const extractionRoot = await mkdtemp(path.join(path.dirname(runtimeRoot), ".pandoc-extract-"));
  try {
    await (options.extractZip || defaultExtractZip)(cachePath, extractionRoot, platform);
    const extractedPath = path.join(extractionRoot, selected.archivePath);
    const extractedInfo = await stat(extractedPath);
    if (!extractedInfo.isFile()) {
      throw new Error(`Pandoc archive executable is missing: ${selected.archivePath}`);
    }
    const runtimePath = path.join(runtimeRoot, selected.runtimePath);
    await mkdir(path.dirname(runtimePath), { recursive: true });
    const temporaryRuntimePath = `${runtimePath}.${process.pid}.tmp`;
    await rm(temporaryRuntimePath, { force: true });
    await copyFile(extractedPath, temporaryRuntimePath);
    if (platform !== "win32") await chmod(temporaryRuntimePath, 0o755);
    const runVersion = options.runVersion || (async (executable) => {
      const { stdout } = await execFile(executable, ["--version"]);
      return stdout;
    });
    const firstLine = String(await runVersion(temporaryRuntimePath)).split(/\r?\n/, 1)[0].trim();
    if (firstLine !== `pandoc ${selected.version}`) {
      throw new Error(`Pandoc version mismatch: got ${JSON.stringify(firstLine)}, want "pandoc ${selected.version}"`);
    }
    await rm(runtimePath, { force: true });
    await rename(temporaryRuntimePath, runtimePath);
    console.log(`Pandoc ${selected.version} staged: ${runtimePath}`);
    return { cachePath, runtimePath, selected };
  } finally {
    await rm(extractionRoot, { recursive: true, force: true });
  }
}

function parseArgs(argv) {
  const runtimeRoot = argv.shift();
  const options = {};
  while (argv.length > 0) {
    const key = argv.shift();
    const value = argv.shift();
    if (!key?.startsWith("--") || !value) throw new Error("invalid Pandoc staging arguments");
    options[key.slice(2)] = value;
  }
  return { runtimeRoot, target: options.target };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const { runtimeRoot, target } = parseArgs(process.argv.slice(2));
  stagePandoc(runtimeRoot, target).catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
