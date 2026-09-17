import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const desktopRoot = path.resolve(scriptsDir, "..");
const manifestScript = path.join(scriptsDir, "write-runtime-manifest.mjs");
const darwinBuildScript = path.join(scriptsDir, "build-darwin-arm64.sh");
const windowsBuildScript = path.join(scriptsDir, "build-windows-x64.ps1");
const electronMain = path.join(desktopRoot, "electron", "src", "main.js");
const releaseConfigModule = path.join(desktopRoot, "electron", "src", "cloud-release-config.js");
const require = createRequire(import.meta.url);

function writeManifestFixtures(root) {
  mkdirSync(path.join(root, "builtin-skills"), { recursive: true });
  writeFileSync(path.join(root, "builtin-skills", "catalog.json"), '{"schema_version":1,"skills":[]}\n');
  mkdirSync(path.join(root, "featured-skills", "assets"), { recursive: true });
  writeFileSync(path.join(root, "featured-skills", "catalog.json"), '{"schema_version":1,"cases":[]}\n');
  writeFileSync(path.join(root, "history-injection.zip"), "fixture");
}

function manifestArgs(root, cloudBaseURL, extra = []) {
  return [
    manifestScript,
    root,
    "--platform", "darwin",
    "--arch", "arm64",
    ...(cloudBaseURL ? ["--cloud-base-url", cloudBaseURL] : []),
    ...extra,
  ];
}

test("writes the build-time Cloud HTTPS origin into the desktop runtime manifest", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-cloud-release-"));
  try {
    writeManifestFixtures(root);
    execFileSync(process.execPath, manifestArgs(root, "https://10.210.0.49:5027"));
    const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
    assert.deepEqual(manifest.cloud, { baseURL: "https://10.210.0.49:5027" });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("keeps Cloud optional for packages built without a release origin", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-cloud-optional-"));
  try {
    writeManifestFixtures(root);
    execFileSync(process.execPath, manifestArgs(root));
    const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
    assert.equal(Object.hasOwn(manifest, "cloud"), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("rejects unsafe or non-origin Cloud release URLs", () => {
  for (const value of [
    "http://cloud.example.com",
    "https://user@cloud.example.com",
    "https://cloud.example.com/path",
    "https://cloud.example.com?tenant=a",
    "https://cloud.example.com/#fragment",
  ]) {
    const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-cloud-invalid-"));
    try {
      writeManifestFixtures(root);
      const result = spawnSync(process.execPath, manifestArgs(root, value), { encoding: "utf8" });
      assert.notEqual(result.status, 0, `unsafe Cloud release URL was accepted: ${value}`);
      assert.equal(existsSync(path.join(root, "manifest.json")), false);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  }
});

test("both platform builds forward the release Cloud origin to the runtime manifest", () => {
  const darwin = readFileSync(darwinBuildScript, "utf8");
  const windows = readFileSync(windowsBuildScript, "utf8");
  for (const source of [darwin, windows]) {
    assert.match(source, /LAZYMIND_CLOUD_BASE_URL/);
    assert.match(source, /--cloud-base-url/);
    assert.match(source, /LAZYMIND_DESKTOP_BUILD_AUDIENCE/);
    assert.match(source, /LAZYMIND_CLOUD_OAUTH_CALLBACK_MODE/);
    assert.match(source, /LAZYMIND_CLOUD_OAUTH_CALLBACK_PORT/);
  }
});

test("writes localhost OAuth relay settings only for an internal desktop build", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-cloud-relay-"));
  try {
    writeManifestFixtures(root);
    execFileSync(process.execPath, manifestArgs(root, "https://10.210.0.49:5027", [
      "--build-audience", "internal",
      "--cloud-oauth-callback-mode", "localhost-relay",
      "--cloud-oauth-callback-port", "8443",
    ]));
    const manifest = JSON.parse(readFileSync(path.join(root, "manifest.json"), "utf8"));
    assert.equal(manifest.buildAudience, "internal");
    assert.deepEqual(manifest.cloud, {
      baseURL: "https://10.210.0.49:5027",
      oauthCallbackMode: "localhost-relay",
      oauthCallbackPort: 8443,
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("production desktop builds reject the localhost OAuth relay", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-cloud-production-relay-"));
  try {
    writeManifestFixtures(root);
    const result = spawnSync(process.execPath, manifestArgs(root, "https://cloud.example.com", [
      "--build-audience", "production",
      "--cloud-oauth-callback-mode", "localhost-relay",
      "--cloud-oauth-callback-port", "8443",
    ]), { encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.equal(existsSync(path.join(root, "manifest.json")), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("packaged Desktop trusts the manifest origin over the process environment", () => {
  assert.equal(existsSync(releaseConfigModule), true, "Cloud release configuration reader is missing");
  const { resolveDesktopCloudBaseURL } = require(releaseConfigModule);
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-cloud-config-reader-"));
  try {
    writeFileSync(path.join(root, "manifest.json"), JSON.stringify({
      version: 1,
      cloud: { baseURL: "https://10.210.0.49:5027" },
    }));
    assert.equal(resolveDesktopCloudBaseURL({
      isPackaged: true,
      runtimeResourcesRoot: root,
      environment: { LAZYMIND_CLOUD_BASE_URL: "https://attacker.example" },
    }), "https://10.210.0.49:5027");
    assert.equal(resolveDesktopCloudBaseURL({
      isPackaged: false,
      runtimeResourcesRoot: root,
      environment: { LAZYMIND_CLOUD_BASE_URL: "https://development.example" },
    }), "https://development.example");
    assert.throws(() => resolveDesktopCloudBaseURL({
      isPackaged: true,
      runtimeResourcesRoot: root,
      environment: {},
      manifest: { version: 1, cloud: { baseURL: "http://cloud.example" } },
    }), /Cloud release configuration is invalid/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("packaged Desktop resolves the reviewed internal OAuth relay configuration", () => {
  const { resolveDesktopCloudConfiguration } = require(releaseConfigModule);
  assert.deepEqual(resolveDesktopCloudConfiguration({
    isPackaged: true,
    runtimeResourcesRoot: "/unused",
    environment: {},
    manifest: {
      version: 1,
      buildAudience: "internal",
      cloud: {
        baseURL: "https://10.210.0.49:5027",
        oauthCallbackMode: "localhost-relay",
        oauthCallbackPort: 8443,
      },
    },
  }), {
    baseURL: "https://10.210.0.49:5027",
    oauthCallbackMode: "localhost-relay",
    oauthCallbackPort: 8443,
  });
  assert.throws(() => resolveDesktopCloudConfiguration({
    isPackaged: true,
    runtimeResourcesRoot: "/unused",
    environment: {},
    manifest: {
      version: 1,
      buildAudience: "production",
      cloud: {
        baseURL: "https://cloud.example.com",
        oauthCallbackMode: "localhost-relay",
        oauthCallbackPort: 8443,
      },
    },
  }), /Cloud release configuration is invalid/);
});

test("Electron passes the resolved release Cloud origin to the managed runtime", () => {
  const source = readFileSync(electronMain, "utf8");
  assert.match(source, /loadDesktopCloudConfiguration/);
  assert.match(source, /LAZYMIND_CLOUD_BASE_URL:\s*cloudBaseURL/);
  assert.match(source, /startCloudOAuthCallbackRelay/);
  assert.doesNotMatch(source, /const cloudBaseURL = String\(process\.env\.LAZYMIND_CLOUD_BASE_URL/);
  assert.doesNotMatch(source, /appendStartupLog\([^\n]*cloudBaseURL/);
});

test("invalid optional Cloud settings disable Cloud without trusting an environment fallback", () => {
  const { loadDesktopCloudConfiguration } = require(releaseConfigModule);
  for (const cloud of [
    { baseURL: "http://untrusted.example" },
    { baseURL: "https://cloud.example.com", oauthCallbackMode: "localhost-relay", oauthCallbackPort: 8443 },
    { baseURL: "https://cloud.example.com/path" },
  ]) {
    const config = loadDesktopCloudConfiguration({
      isPackaged: true, runtimeResourcesRoot: "/unused",
      manifest: { buildAudience: "production", cloud },
      environment: { LAZYMIND_CLOUD_BASE_URL: "https://fallback.example" },
    });
    assert.deepEqual(config, { baseURL: "", oauthCallbackMode: "direct", oauthCallbackPort: 0, errorCode: "CLOUD_CONFIG_INVALID" });
  }
  assert.deepEqual(loadDesktopCloudConfiguration({
    isPackaged: true, manifest: { version: 1 }, environment: {},
  }), { baseURL: "", oauthCallbackMode: "direct", oauthCallbackPort: 0 });
  assert.equal(loadDesktopCloudConfiguration({
    isPackaged: false, environment: { LAZYMIND_CLOUD_BASE_URL: "invalid" },
  }).errorCode, "CLOUD_CONFIG_INVALID");
});
