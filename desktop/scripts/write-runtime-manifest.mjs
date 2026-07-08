#!/usr/bin/env node
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const runtimeRoot = process.argv[2];
if (!runtimeRoot) {
  console.error("usage: write-runtime-manifest.mjs <runtime-root>");
  process.exit(2);
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
    const rel = path.relative(base, full);
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
  platform: "darwin",
  arch: "arm64",
  binaries: {
    "process-supervisor": "bin/process-compose",
    "local-proxy": "bin/local-proxy",
    "core": "bin/core",
    "scan-control-plane": "bin/scan-control-plane",
    "file-watcher": "bin/file-watcher",
    "caddy": "bin/caddy"
  },
  paths: {
    appRoot: "app",
    frontendDist: "app/frontend/dist",
    pythonRuntime: "python/runtime",
    authServiceVenv: "python/auth-service",
    algorithmVenv: "python/algorithm",
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
