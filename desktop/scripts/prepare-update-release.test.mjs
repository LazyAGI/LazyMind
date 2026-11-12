import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import test from "node:test";
import { prepareRelease, fingerprint } from "./prepare-update-release.mjs";

const signatureFixture = () => ({ status: 0, stderr: "TeamIdentifier=fixture\nAuthority=Developer ID Application: Fixture\n" });
const prepare = options => prepareRelease(options, signatureFixture);

function fixture(t, origin = "https://fixture.invalid", updaterProtocol = 1) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "lm-release-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const resources = path.join(root, "LazyMind.app/Contents/Resources"), runtime = path.join(resources, "runtime");
  fs.mkdirSync(path.join(runtime, "bin"), { recursive: true }); fs.mkdirSync(path.join(runtime, "config"));
  const config = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official", releaseId: "r1", version: "1.0.0",
    platform: process.platform === "darwin" ? "darwin" : "windows", arch: "arm64", signingIdentity: "fixture", updaterProtocol, allowedOrigins: [origin] };
  const helper = path.join(runtime, "bin", "lazymind-update-helper");
  fs.writeFileSync(helper, `#!${process.execPath}\nconsole.log(${JSON.stringify(JSON.stringify({ status: "enabled", config }))});\n`, { mode: 0o755 });
  fs.writeFileSync(path.join(resources, "app-update.yml"), JSON.stringify({ lazymind: { protocol: updaterProtocol, releaseId: "r1", version: "1.0.0", mode: "official", platform: config.platform, arch: "arm64", signingIdentity: "fixture" } }));
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

test("v2 publication binds the supported LazyLLM lifecycle shapes into its format", { skip: process.platform === "win32" }, async t => {
  const f = fixture(t, "https://fixture.invalid", 2);
  const catalogFile = path.join(f.runtime, "config/python-components.json");
  const catalog = JSON.parse(fs.readFileSync(catalogFile));
  catalog.components.rag = { revision: "a".repeat(64) };
  fs.writeFileSync(catalogFile, JSON.stringify(catalog));
  const recipeFile = path.join(f.runtime, "config/update-format-v2.json");
  const recipe = JSON.parse(fs.readFileSync(new URL("../../local/local-runtime-manager/update-format-v2.json", import.meta.url)));
  fs.writeFileSync(recipeFile, JSON.stringify(recipe));
  f.repack();
  const supported = await prepare(f);
  recipe.lazyllmVariants = [];
  fs.writeFileSync(recipeFile, JSON.stringify(recipe));
  f.repack();
  const restricted = await prepare({ ...f, output: f.output + "-restricted" });
  assert.notEqual(supported.formatProfile, restricted.formatProfile,
    "published format must include exactly the lifecycle shapes recognized by the runtime");
});

test("v2 publication projects real catalog build metadata into the protocol descriptor", { skip: process.platform === "win32" }, async t => {
  const f = fixture(t, "https://fixture.invalid", 2);
  const descriptor = { id: "rag", revision: "a".repeat(64), sha256: "b".repeat(64), tag: "c".repeat(64),
    baseFingerprint: "d".repeat(64), pythonAbi: "cp311", packages: { "owner-fixture": "1" },
    platform: process.platform === "darwin" ? "darwin" : "windows", arch: "arm64", filename: "rag.zip",
    url: "https://fixture.invalid/rag.zip", catalogUrl: "https://fixture.invalid/python-components.json",
    sizeBytes: 1, unpackedBytes: 1 };
  const catalogFile = path.join(f.runtime, "config/python-components.json");
  const catalog = JSON.parse(fs.readFileSync(catalogFile));
  catalog.components.rag = { ...descriptor, schemaVersion: 1, signingMode: "developer-id" };
  fs.writeFileSync(catalogFile, JSON.stringify(catalog));
  fs.copyFileSync(new URL("../../local/local-runtime-manager/update-format-v2.json", import.meta.url),
    path.join(f.runtime, "config/update-format-v2.json"));
  f.repack();
  const record = await prepare(f);
  assert.deepEqual(record.componentDescriptor, descriptor);
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(f.output, "r1/components.json"))), catalog,
    "the exact signed catalog retains its native build metadata");
});
