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
  console.error("usage: write-runtime-manifest.mjs <runtime-root> --platform darwin|windows --arch arm64|amd64");
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
  binaries: {
    "process-supervisor": executable("process-compose"),
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
    algorithmVenv: "deps/python/algorithm",
    localProxyConfig: "app/local/local-proxy/configs/cloud-replace-kong.yaml"
  },
  services: {
    "local-proxy": { healthPath: "/_local/healthz" },
    "auth-service": { healthPath: "/api/authservice/auth/health" },
    "core": { healthPath: "/health" },
    "scan-control-plane": { healthPath: "/healthz" },
    "file-watcher": { healthPath: "/healthz" },
    "lazyllm-doc-server": { healthPath: "/v1/health" },
    "lazyllm-parse-server": { healthPath: "/health" },
    "lazyllm-algo": { healthPath: "/docs" },
    "chat": { healthPath: "/health" }
  },
  checksums: walk(path.join(runtimeRoot, "bin"), runtimeRoot)
};

writeFileSync(path.join(runtimeRoot, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
