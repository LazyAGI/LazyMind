import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { prepareUpdateBuildConfig } from "./prepare-update-build-config.mjs";

function options() {
  const key = generateKeyPairSync("ed25519").publicKey.export({ type: "spki", format: "der" }).subarray(-32).toString("base64");
  return { packageVersion: "1.0.0", platform: "darwin", arch: "arm64", signingMode: "developer-id", allowUnsignedTest: false,
    configuration: { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official", releaseId: "fixture-1.0.0", signingIdentity: "FIXTURE_TEAM", updaterProtocol: 1,
      policyURL: "https://updates.example.invalid/policy.json", allowedOrigins: ["https://updates.example.invalid"], publicKeys: { key } } };
}

test("an unconfigured build stays disabled without inventing trust keys or a source", () => {
  assert.equal(prepareUpdateBuildConfig({ ...options(), configuration: undefined }), "");
});

test("public update config is bound to the actual package version and target", () => {
  const input = options();
  const result = JSON.parse(Buffer.from(prepareUpdateBuildConfig(input), "base64").toString("utf8"));
  assert.equal(result.version, "1.0.0"); assert.equal(result.platform, "darwin"); assert.equal(result.arch, "arm64");
  assert.deepEqual(result.publicKeys, input.configuration.publicKeys);
  for (const mismatch of [{ version: "2.0.0" }, { platform: "windows" }, { arch: "amd64" }]) {
    assert.throws(() => prepareUpdateBuildConfig({ ...input, configuration: { ...input.configuration, ...mismatch } }), /mismatch/);
  }
});

test("official configuration cannot be embedded in unsigned packages or stable prereleases", () => {
  const input = options();
  for (const signingMode of ["adhoc", "none", ""]) assert.throws(() => prepareUpdateBuildConfig({ ...input, signingMode }), /sign/);
  assert.throws(() => prepareUpdateBuildConfig({ ...input, packageVersion: "1.1.0-alpha.1" }), /channel/);
  assert.throws(() => prepareUpdateBuildConfig({ ...input, platform: "windows", arch: "amd64", signingMode: "none" }), /sign/);
});

test("unsigned personal test requires explicit enablement and an isolated audience/channel", () => {
  const input = options();
  const configuration = { ...input.configuration, mode: "unsigned-test", audience: "personal-fixture", channel: "personal-test", signingIdentity: "adhoc", policyURL: "http://127.0.0.1:8000/policy.json", allowedOrigins: ["http://127.0.0.1:8000"] };
  assert.throws(() => prepareUpdateBuildConfig({ ...input, configuration, signingMode: "adhoc" }), /explicit/);
  assert.doesNotThrow(() => prepareUpdateBuildConfig({ ...input, configuration, signingMode: "adhoc", allowUnsignedTest: true }));
  assert.throws(() => prepareUpdateBuildConfig({ ...input, configuration: { ...configuration, audience: "official" }, signingMode: "adhoc", allowUnsignedTest: true }), /audience/);
  assert.throws(() => prepareUpdateBuildConfig({ ...input, configuration: { ...configuration, channel: "stable" }, signingMode: "adhoc", allowUnsignedTest: true }), /channel/);
});

test("unknown build fields cannot accidentally embed a private key", () => {
  const input = options();
  assert.throws(() => prepareUpdateBuildConfig({ ...input, configuration: { ...input.configuration, privateKey: "DO-NOT-EMBED" } }), /unknown/i);
});

test("the build CLI emits only the public configuration consumed by both platform builds", (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lm-update-config-")); t.after(() => rmSync(root, { recursive: true, force: true }));
  const configPath = path.join(root, "public.json"), packagePath = path.join(root, "package.json");
  const input = options(); writeFileSync(configPath, JSON.stringify(input.configuration)); writeFileSync(packagePath, JSON.stringify({ version: input.packageVersion }));
  const output = execFileSync(process.execPath, [new URL("./prepare-update-build-config.mjs", import.meta.url).pathname,
    "--platform", "darwin", "--arch", "arm64", "--signing-mode", "developer-id", "--package", packagePath],
    { encoding: "utf8", env: { ...process.env, LAZYMIND_DESKTOP_UPDATE_CONFIG: configPath } }).trim();
  const value = JSON.parse(Buffer.from(output, "base64").toString("utf8"));
  assert.equal(value.releaseId, "fixture-1.0.0"); assert.equal(value.version, "1.0.0");
  for (const file of ["build-darwin-arm64.sh", "build-windows-x64.ps1"]) {
    const source = readFileSync(new URL(`./${file}`, import.meta.url), "utf8");
    assert.ok(source.includes("prepare-update-build-config.mjs"), `${file} must prepare fixed build config`);
    assert.ok(source.includes("main.embeddedConfig="), `${file} must embed the validated configuration`);
    assert.match(source, /cmd[\\/]update-helper/);
  }
});
