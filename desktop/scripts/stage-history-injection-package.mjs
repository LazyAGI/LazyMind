#!/usr/bin/env node

import { createHash } from "node:crypto";
import {
  copyFile,
  mkdir,
  readFile,
  rename,
  rm,
  stat,
} from "node:fs/promises";
import { createReadStream, createWriteStream } from "node:fs";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath, pathToFileURL } from "node:url";

const scriptPath = fileURLToPath(import.meta.url);
const scriptsDir = path.dirname(scriptPath);
const defaultConfigPath = path.join(scriptsDir, "..", "history-injection-package.json");
const defaultCacheRoot = path.join(scriptsDir, "..", "cache", "history-injection");

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

function validateConfig(config, configPath) {
  if (config?.version !== 1) {
    throw new Error(`unsupported history injection package config version in ${configPath}`);
  }
  const parsedURL = new URL(config.url);
  if (!new Set(["https:", "http:"]).has(parsedURL.protocol)) {
    throw new Error(`history injection package URL must use HTTP(S): ${config.url}`);
  }
  if (!/^[a-f0-9]{64}$/.test(config.sha256 || "")) {
    throw new Error(`history injection package SHA-256 is invalid in ${configPath}`);
  }
  if (!Number.isSafeInteger(config.size) || config.size <= 0) {
    throw new Error(`history injection package size is invalid in ${configPath}`);
  }
  for (const [key, value] of [
    ["fileName", config.fileName],
    ["runtimeFileName", config.runtimeFileName],
  ]) {
    if (!value || path.basename(value) !== value || value.includes("..")) {
      throw new Error(`history injection package ${key} is unsafe in ${configPath}`);
    }
  }
}

async function fileMatches(filePath, config) {
  try {
    const info = await stat(filePath);
    if (!info.isFile() || info.size !== config.size) {
      return false;
    }
    return (await sha256File(filePath)) === config.sha256;
  } catch (error) {
    if (error?.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

async function downloadOnce(url, destination) {
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok || !response.body) {
    throw new Error(`history injection download failed: HTTP ${response.status}`);
  }
  const hash = createHash("sha256");
  const meter = new Transform({
    transform(chunk, _encoding, callback) {
      hash.update(chunk);
      callback(null, chunk);
    },
  });
  await pipeline(
    Readable.fromWeb(response.body),
    meter,
    createWriteStream(destination, { flags: "wx" }),
  );
  return hash.digest("hex");
}

async function downloadWithRetry(config, destination, attempts = 3) {
  let lastError;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    await rm(destination, { force: true });
    try {
      const digest = await downloadOnce(config.url, destination);
      const info = await stat(destination);
      if (info.size !== config.size) {
        throw new Error(`history injection package size mismatch: got ${info.size}, want ${config.size}`);
      }
      if (digest !== config.sha256) {
        throw new Error(`history injection package SHA-256 mismatch: got ${digest}, want ${config.sha256}`);
      }
      return;
    } catch (error) {
      lastError = error;
      await rm(destination, { force: true });
      if (attempt < attempts) {
        await new Promise((resolve) => setTimeout(resolve, attempt * 1000));
      }
    }
  }
  throw lastError;
}

export async function stageHistoryInjectionPackage(runtimeRoot, options = {}) {
  if (!runtimeRoot) {
    throw new Error("runtime root is required");
  }
  const configPath = options.configPath || defaultConfigPath;
  const cacheRoot = options.cacheRoot || defaultCacheRoot;
  const config = JSON.parse(await readFile(configPath, "utf8"));
  validateConfig(config, configPath);

  await mkdir(cacheRoot, { recursive: true });
  await mkdir(runtimeRoot, { recursive: true });
  const cachePath = path.join(cacheRoot, `${config.sha256}-${config.fileName}`);
  const runtimePath = path.join(runtimeRoot, config.runtimeFileName);

  if (!(await fileMatches(cachePath, config))) {
    await rm(cachePath, { force: true });
    const temporaryPath = `${cachePath}.${process.pid}.tmp`;
    await downloadWithRetry(config, temporaryPath, options.attempts || 3);
    await rename(temporaryPath, cachePath);
  }

  const temporaryRuntimePath = `${runtimePath}.${process.pid}.tmp`;
  await rm(temporaryRuntimePath, { force: true });
  await copyFile(cachePath, temporaryRuntimePath);
  if (!(await fileMatches(temporaryRuntimePath, config))) {
    await rm(temporaryRuntimePath, { force: true });
    throw new Error("staged history injection package failed verification");
  }
  await rm(runtimePath, { force: true });
  await rename(temporaryRuntimePath, runtimePath);
  console.log(`History injection package staged: ${runtimePath}`);
  return { config, cachePath, runtimePath };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  stageHistoryInjectionPackage(process.argv[2]).catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
