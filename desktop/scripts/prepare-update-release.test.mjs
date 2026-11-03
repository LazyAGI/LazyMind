import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import test from "node:test";
import { prepareRelease, fingerprint } from "./prepare-update-release.mjs";

const signatureFixture = () => ({ status: 0, stderr: "TeamIdentifier=fixture\nAuthority=Developer ID Application: Fixture\n" });
const prepare = options => prepareRelease(options, signatureFixture);

function fixture(t, origin = "https://fixture.invalid") {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lm-release-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const resources = path.join(root, "LazyMind.app/Contents/Resources"), runtime = path.join(resources, "runtime");
  fs.mkdirSync(path.join(runtime, "bin"), { recursive: true }); fs.mkdirSync(path.join(runtime, "config"));
  const config = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official", releaseId: "r1", version: "1.0.0",
    platform: process.platform === "darwin" ? "darwin" : "windows", arch: "arm64", signingIdentity: "fixture", updaterProtocol: 1, allowedOrigins: [origin] };
  const helper = path.join(runtime, "bin", "lazymind-update-helper");
  fs.writeFileSync(helper, `#!${process.execPath}\nconsole.log(${JSON.stringify(JSON.stringify({ status: "enabled", config }))});\n`, { mode: 0o755 });
  fs.writeFileSync(path.join(resources, "app-update.yml"), JSON.stringify({ lazymind: { protocol: 1, releaseId: "r1", version: "1.0.0", mode: "official", platform: config.platform, arch: "arm64", signingIdentity: "fixture" } }));
  fs.writeFileSync(path.join(runtime, "manifest.json"), '{"version":1}');
  fs.writeFileSync(path.join(runtime, "config/python-components.json"), JSON.stringify({ schemaVersion: 1, platform: config.platform, arch: "arm64", components: {} }));
  const packagePath = path.join(root, config.platform === "darwin" ? "app.zip" : "app.exe");
  const repack = () => config.platform === "darwin" ? execFileSync("/usr/bin/zip", ["-qr", packagePath, "LazyMind.app"], { cwd: root }) : fs.writeFileSync(packagePath, "fixture installer");
  repack();
  return { resources, runtime, packagePath, repack, baseURL: "https://fixture.invalid/updates/", minimumSystemVersion: "12.0.0", output: path.join(root, "out") };
}

test("release metadata hashes the staged package and exact built component catalog", { skip: process.platform === "win32" }, async t => {
  const f = fixture(t), record = await prepare(f);
  const staged = path.join(f.output, "r1");
  assert.equal(record.release.package.sha256, (await fingerprint(f.packagePath)).sha256);
  assert.equal(record.release.components.sha256, (await fingerprint(path.join(f.runtime, "config/python-components.json"))).sha256);
  assert.equal(record.release.package.url, `https://fixture.invalid/updates/releases/r1/package${path.extname(f.packagePath)}`);
  assert.equal(JSON.parse(fs.readFileSync(path.join(staged, "feed.json"))).sha512, (await fingerprint(f.packagePath)).sha512);
  assert.equal(record.manifestSHA256, (await fingerprint(path.join(f.runtime, "manifest.json"))).sha256);
  await assert.rejects(prepare(f), /already exists/);
});

test("wrong native identity, package format and unpinned sources cannot be staged", { skip: process.platform === "win32" }, async t => {
  const f = fixture(t);
  await assert.rejects(prepare({ ...f, baseURL: "https://other.invalid/" }), /pinned/);
  await assert.rejects(prepare({ ...f, packagePath: "wrong.dmg" }), /package must/);
  fs.writeFileSync(path.join(f.resources, "app-update.yml"), '{"lazymind":{}}');
  await assert.rejects(prepare(f), /does not match/);
  assert.equal(fs.existsSync(f.output), false);
});

test("Mac ZIP must contain the same metadata as the packaged app", { skip: process.platform !== "darwin" }, async t => {
  const f = fixture(t);
  fs.writeFileSync(path.join(f.runtime, "manifest.json"), '{"version":1,"different":true}');
  await assert.rejects(prepare(f), /ZIP metadata/);
  assert.equal(fs.existsSync(f.output), false);
});

test("client-supported trailing slash origins can generate release artifacts", { skip: process.platform === "win32" }, async t => {
  const f = fixture(t, "https://fixture.invalid/");
  await assert.rejects(prepare({ ...f, baseURL: "https://other.invalid/" }), /pinned/);
  const record = await prepare(f);
  assert.equal(record.release.package.url.startsWith("https://fixture.invalid/"), true);
});

test("Mac native signature identity must match the embedded release identity", { skip: process.platform !== "darwin" }, async t => {
  const f = fixture(t);
  await assert.rejects(prepareRelease(f, () => ({ status: 0, stderr: "TeamIdentifier=OTHER\nAuthority=Developer ID Application: Other\n" })), /signature does not match/);
  assert.equal(fs.existsSync(f.output), false);
});
