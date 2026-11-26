import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { prepareUpdateBuildConfig } from "./prepare-update-build-config.mjs";

const helper = fileURLToPath(new URL("./windows-test-channel.mjs", import.meta.url));
const repo = "fixture/lazymind";
const root = `https://modelscope.cn/datasets/${repo}/resolve/master/updates/windows-test/trial-1/`;
const key = () => generateKeyPairSync("ed25519").publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64");
const template = { mode: "official", audience: "official", channel: "canary", trustDomain: "windows-test-fixture",
  signingIdentity: "Fixture Code Signing", updaterProtocol: 2, policyURL: root + "policy.json",
  allowedOrigins: ["https://modelscope.cn"], publicKeys: { test: key() } };
const official = { ...template, trustDomain: "official", channel: "stable", signingIdentity: "Official Publisher",
  policyURL: root.replace("windows-test/trial-1/", "stable.json"), publicKeys: { release: key() } };

function run(args, config = template, production = official, options = {}) {
  return spawnSync(process.execPath, [helper, ...args], { encoding: "utf8", timeout: 5000, cwd: options.cwd,
    env: { ...process.env, LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON: JSON.stringify(config),
      LAZYMIND_WINDOWS_UPDATE_CONFIG_JSON: JSON.stringify(production), UPDATE_REPO: repo, ...options.env } });
}

test("two test builds stamp the selected versions and embed the same isolated update config and component base", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "windows-build-version-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const pkg = path.join(dir, "desktop/electron/package.json");
  fs.mkdirSync(path.dirname(pkg), { recursive: true });
  fs.writeFileSync(pkg, JSON.stringify({ name: "lazymind-desktop", version: "0.5.1-alpha.3" }));
  for (const version of ["0.5.301", "0.5.302"]) {
    const env = { TEST_VERSION: version, GITHUB_ENV: path.join(dir, version + ".env"), GITHUB_OUTPUT: path.join(dir, version + ".out") };
    const result = run(["configure"], template, official, { cwd: dir, env });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(fs.readFileSync(pkg)), { name: "lazymind-desktop", version });
    const lines = fs.readFileSync(env.GITHUB_ENV, "utf8").trim().split(/\r?\n/);
    assert.ok(lines.includes("LAZYMIND_UPDATE_BASE_URL=" + root));
    assert.ok(lines.includes("LAZYMIND_PYTHON_COMPONENT_BASE_URL=" + root + "RAG/"));
    assert.deepEqual(JSON.parse(lines.find(l => l.startsWith("LAZYMIND_UPDATE_BUILD_CONFIG_JSON=")).split("=").slice(1).join("=")), template);
    assert.ok(fs.readFileSync(env.GITHUB_OUTPUT, "utf8").split(/\r?\n/).includes("package_version=" + version));
  }
  const invalid = run(["configure"], template, official, { cwd: dir, env: { TEST_VERSION: "not-a-version" } });
  assert.notEqual(invalid.status, 0);
  assert.equal(JSON.parse(fs.readFileSync(pkg)).version, "0.5.302");
});

test("signed Windows test rounds bind their package, feed, catalog and policy to the configured test channel", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "windows-update-round-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const check = (...args) => run(["check-release", dir, ...args]);
  for (const version of ["0.5.301", "0.5.302"]) {
    const config = JSON.parse(Buffer.from(prepareUpdateBuildConfig({ configuration: template, platform: "windows",
      arch: "amd64", signingMode: "authenticode", packageVersion: version }), "base64"));
    const release = { releaseId: config.releaseId, version, platform: "windows", arch: "amd64", signingIdentity: config.signingIdentity, updaterProtocol: 2 };
    const recordDir = path.join(dir, release.releaseId);
    fs.mkdirSync(recordDir);
    for (const [kind, file] of [["package", "package.exe"], ["feed", "feed.json"], ["components", "components.json"]])
      release[kind] = { url: root + "releases/" + release.releaseId + "/" + file };
    const write = (c = config, r = release) => fs.writeFileSync(path.join(recordDir, "release.json"), JSON.stringify({ config: c, release: r }));
    write();
    const valid = check(); assert.equal(valid.status, 0, valid.stderr);
    for (const change of [{ publicKeys: official.publicKeys }, { policyURL: official.policyURL }, { signingIdentity: official.signingIdentity }]) {
      write({ ...config, ...change });
      assert.notEqual(check().status, 0, "foreign release configuration accepted");
    }
    for (const field of ["package", "feed", "components"]) {
      write(config, { ...release, [field]: { ...release[field], url: release[field].url.replace("windows-test/trial-1/", "") } });
      assert.notEqual(check().status, 0, "release writes outside test prefix: " + field);
    }
    write();
    const hidden = path.join(recordDir, "extra");
    fs.mkdirSync(hidden);
    fs.writeFileSync(path.join(hidden, "release.json"), JSON.stringify({ config: official, release }));
    assert.notEqual(check().status, 0, "the planner recursively reads additional records");
    fs.rmSync(recordDir, { recursive: true });
  }
});

test("Windows signed test configuration rejects other datasets, production identities and ambiguous URL paths", () => {
  const base = run(["base-url"]); assert.equal(base.status, 0, base.stderr); assert.equal(base.stdout.trim(), root);
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  const raw = official.publicKeys.release;
  const alternate = raw.slice(0, -2) + alphabet[alphabet.indexOf(raw.at(-2)) ^ 1] + "=";
  assert.notEqual(alternate, raw);
  assert.deepEqual(Buffer.from(alternate, "base64"), Buffer.from(raw, "base64"));
  for (const override of [
    { policyURL: official.policyURL }, { policyURL: root.replace(repo, "foreign/repo") + "policy.json" },
    { policyURL: root + "../policy.json" }, { policyURL: root + "%2e%2e/policy.json" },
    { channel: "stable" }, { updaterProtocol: 1 }, { mode: "unsigned-test" },
    { trustDomain: official.trustDomain }, { signingIdentity: official.signingIdentity },
    { publicKeys: { renamed: official.publicKeys.release } }, { publicKeys: { alias: alternate } },
  ]) assert.notEqual(run(["base-url"], { ...template, ...override }).status, 0, JSON.stringify(override));
});

test("a disposable Windows runner trusts only the configured public code-signing certificate", {
  skip: process.platform !== "win32" || process.env.RUNNER_ENVIRONMENT !== "github-hosted",
}, () => {
  const script = fileURLToPath(new URL("./fixtures/windows-test-certificate.ps1", import.meta.url));
  const result = spawnSync("pwsh", ["-NoProfile", "-NonInteractive", "-File", script], { encoding: "utf8", timeout: 90000 });
  assert.equal(result.status, 0, result.stderr || result.error?.message);
  assert.match(result.stdout, /pinned signer verified/);
});
