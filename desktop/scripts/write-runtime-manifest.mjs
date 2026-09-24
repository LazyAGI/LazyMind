#!/usr/bin/env node
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { validateConfig } from "./stage-history-injection-package.mjs";

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
  console.error("usage: write-runtime-manifest.mjs <runtime-root> --platform darwin|windows --arch arm64|amd64 [--trusted-local-mode true|false] [--build-audience production|internal] [--cloud-base-url https://cloud.example.com] [--cloud-oauth-callback-mode direct|localhost-relay] [--cloud-oauth-callback-port 8443]");
  process.exit(2);
}

const trustedLocalModeOption = options["trusted-local-mode"] ?? "false";
if (!new Set(["true", "false"]).has(trustedLocalModeOption)) {
  console.error("--trusted-local-mode must be true or false");
  process.exit(2);
}

const buildAudience = options["build-audience"] ?? "production";
if (!new Set(["production", "internal"]).has(buildAudience)) {
  console.error("--build-audience must be production or internal");
  process.exit(2);
}

const cloudOAuthCallbackMode = options["cloud-oauth-callback-mode"] ?? "direct";
if (!new Set(["direct", "localhost-relay"]).has(cloudOAuthCallbackMode)) {
  console.error("--cloud-oauth-callback-mode must be direct or localhost-relay");
  process.exit(2);
}
let cloudOAuthCallbackPort = 0;
if (cloudOAuthCallbackMode === "localhost-relay") {
  cloudOAuthCallbackPort = Number(options["cloud-oauth-callback-port"]);
  if (buildAudience !== "internal" || !Number.isInteger(cloudOAuthCallbackPort) || cloudOAuthCallbackPort < 1024 || cloudOAuthCallbackPort > 65535) {
    console.error("localhost OAuth callback relay requires an internal build and an unprivileged port");
    process.exit(2);
  }
} else if (options["cloud-oauth-callback-port"] !== undefined) {
  console.error("--cloud-oauth-callback-port is only valid with localhost-relay");
  process.exit(2);
}

let cloudBaseURL = "";
if (options["cloud-base-url"]) {
  const candidate = options["cloud-base-url"];
  let parsed;
  try {
    parsed = new URL(candidate);
  } catch {
    console.error("--cloud-base-url must be an HTTPS origin");
    process.exit(2);
  }
  if (
    candidate.trim() !== candidate ||
    parsed.protocol !== "https:" ||
    parsed.username ||
    parsed.password ||
    parsed.pathname !== "/" ||
    parsed.search ||
    parsed.hash
  ) {
    console.error("--cloud-base-url must be an HTTPS origin");
    process.exit(2);
  }
  cloudBaseURL = parsed.origin;
}
if (!cloudBaseURL && cloudOAuthCallbackMode !== "direct") {
  console.error("localhost OAuth callback relay requires --cloud-base-url");
  process.exit(2);
}

const supportedTargets = new Set(["darwin/arm64", "darwin/amd64", "windows/amd64"]);
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
const builtinSkills = JSON.parse(readFileSync(builtinSkillCatalog, "utf8")).skills;
for (const skill of builtinSkills) {
  if (!skill.source_url?.startsWith("builtin://") && existsSync(path.join(runtimeRoot, "builtin-skills", skill.package_file))) {
    console.error(`remote builtin Skill package must not be bundled: ${skill.uid}`);
    process.exit(1);
  }
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
// Deferred builds must never silently retain full-size showcase assets.
const featuredDownloadsPath = path.join(runtimeRoot, "featured-skills", "downloads.json");
if (existsSync(featuredDownloadsPath)) {
  const downloads = JSON.parse(readFileSync(featuredDownloadsPath, "utf8"));
  if (downloads.schemaVersion !== 1 || !downloads.local || !downloads.bundles) {
    throw new Error("Invalid deferred featured asset catalog");
  }
  for (const filename of Object.keys(walk(featuredSkillAssets, featuredSkillAssets))) {
    if (!Object.hasOwn(downloads.local, filename.replaceAll("\\", "/"))) {
      throw new Error(`Full-size featured asset must not be bundled: ${filename}`);
    }
  }
}
const historyInjectionArchive = path.join(runtimeRoot, "history-injection.zip");
const historyInjectionDescriptor = path.join(runtimeRoot, "history-injection-package.json");
let historyInjectionDownload;
if (existsSync(historyInjectionDescriptor)) {
  if (existsSync(historyInjectionArchive)) {
    throw new Error("history examples cannot be bundled and deferred in the same runtime");
  }
  const config = JSON.parse(readFileSync(historyInjectionDescriptor, "utf8"));
  validateConfig(config, historyInjectionDescriptor);
  historyInjectionDownload = { url: config.url, size: config.size, sha256: config.sha256 };
}
if (!historyInjectionDownload && !existsSync(historyInjectionArchive)) {
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
  ...(buildAudience === "internal" ? { buildAudience } : {}),
  ...(cloudBaseURL ? {
    cloud: {
      baseURL: cloudBaseURL,
      ...(cloudOAuthCallbackMode === "localhost-relay" ? {
        oauthCallbackMode: cloudOAuthCallbackMode,
        oauthCallbackPort: cloudOAuthCallbackPort,
      } : {}),
    },
  } : {}),
  features: {
    trustedLocalMode: trustedLocalModeOption === "true",
    offlineBuiltinSkills: false,
    offlineFeaturedSkills: false
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
    ...(historyInjectionDownload ? {} : { historyInjectionArchive: "history-injection.zip" })
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
  ...(historyInjectionDownload ? { historyInjectionDownload } : {}),
  checksums: {
    ...walk(path.join(runtimeRoot, "bin"), runtimeRoot),
    ...walk(path.join(runtimeRoot, "builtin-skills"), runtimeRoot),
    ...walk(path.join(runtimeRoot, "featured-skills"), runtimeRoot),
    ...(historyInjectionDownload
      ? { "history-injection-package.json": sha256(historyInjectionDescriptor) }
      : { "history-injection.zip": sha256(historyInjectionArchive) })
  }
};

writeFileSync(path.join(runtimeRoot, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
