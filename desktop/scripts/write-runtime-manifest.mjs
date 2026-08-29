#!/usr/bin/env node
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const args = process.argv.slice(2);
const runtimeRoot = args.shift();
const options = {};
while (args.length > 0) {
  const key = args.shift();
  const value = args.shift();
  if (!key?.startsWith("--") || !value) {
    console.error("invalid runtime manifest arguments");
    process.exit(2);
  }
  options[key.slice(2)] = value;
}

if (!runtimeRoot || !options.platform || !options.arch) {
  console.error("usage: write-runtime-manifest.mjs <runtime-root> --platform darwin|windows --arch arm64|amd64 [--trusted-local-mode true|false]");
  process.exit(2);
}

const trustedLocalModeOption = options["trusted-local-mode"] ?? "false";
if (!new Set(["true", "false"]).has(trustedLocalModeOption)) {
  console.error("--trusted-local-mode must be true or false");
  process.exit(2);
}

const supportedTargets = new Set(["darwin/arm64", "windows/amd64"]);
const target = `${options.platform}/${options.arch}`;
if (!supportedTargets.has(target)) {
  console.error(`unsupported desktop runtime target: ${target}`);
  process.exit(2);
}

const executableSuffix = options.platform === "windows" ? ".exe" : "";
const executable = (name) => `bin/${name}${executableSuffix}`;
const builtinSkillCatalog = path.join(runtimeRoot, "builtin-skills", "catalog.json");
if (!existsSync(builtinSkillCatalog)) {
  console.error(`builtin Skill catalog is missing: ${builtinSkillCatalog}`);
  process.exit(1);
}
const featuredSkillCatalog = path.join(runtimeRoot, "featured-skills", "catalog.json");
if (!existsSync(featuredSkillCatalog)) {
  console.error(`featured Skill catalog is missing: ${featuredSkillCatalog}`);
  process.exit(1);
}
const featuredSkillAssets = path.join(runtimeRoot, "featured-skills", "assets");
if (!existsSync(featuredSkillAssets)) {
  console.error(`featured Skill assets are missing: ${featuredSkillAssets}`);
  process.exit(1);
}
const historyInjectionArchive = path.join(runtimeRoot, "history-injection.zip");
if (!existsSync(historyInjectionArchive)) {
  console.error(`history injection package is missing: ${historyInjectionArchive}`);
  process.exit(1);
}

function sha256(file) {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

function walk(dir, base = dir, out = {}) {
  if (!existsSync(dir)) {
    return out;
  }
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    const rel = path.relative(base, full).split(path.sep).join("/");
    const stat = statSync(full);
    if (stat.isDirectory()) {
      walk(full, base, out);
    } else if (stat.isFile()) {
      out[rel] = sha256(full);
    }
  }
  return out;
}

const manifest = {
  version: 1,
  profile: "desktop",
  platform: options.platform,
  arch: options.arch,
  features: {
    trustedLocalMode: trustedLocalModeOption === "true",
    offlineBuiltinSkills: true,
    offlineFeaturedSkills: true
  },
  binaries: {
    "process-supervisor": executable("process-compose"),
    "agent-connector": executable("lazymind"),
    "local-proxy": executable("local-proxy"),
    "core": executable("core"),
    "scan-control-plane": executable("scan-control-plane"),
    "file-watcher": executable("file-watcher"),
    "caddy": executable("caddy")
  },
  paths: {
    appRoot: "app",
    frontendDist: "app/frontend/dist",
    pythonRuntime: "runtimes/python",
    authServiceVenv: "deps/python/auth-service",
    channelGatewayVenv: "deps/python/channel-gateway",
    algorithmVenv: "deps/python/algorithm",
    localProxyConfig: "app/local/local-proxy/configs/cloud-replace-kong.yaml",
    historyInjectionArchive: "history-injection.zip"
  },
  services: {
    "local-proxy": { healthPath: "/_local/healthz" },
    "auth-service": { healthPath: "/api/authservice/auth/health" },
    "channel-gateway": { healthPath: "/readyz" },
    "core": { healthPath: "/health" },
    "scan-control-plane": { healthPath: "/healthz" },
    "file-watcher": { healthPath: "/healthz" },
    "lazyllm-doc-server": { healthPath: "/v1/health" },
    "lazyllm-parse-server": { healthPath: "/health" },
    "lazyllm-algo": { healthPath: "/docs" },
    "chat": { healthPath: "/health" }
  },
  checksums: {
    ...walk(path.join(runtimeRoot, "bin"), runtimeRoot),
    ...walk(path.join(runtimeRoot, "builtin-skills"), runtimeRoot),
    ...walk(path.join(runtimeRoot, "featured-skills"), runtimeRoot),
    "history-injection.zip": sha256(historyInjectionArchive)
  }
};

writeFileSync(path.join(runtimeRoot, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
