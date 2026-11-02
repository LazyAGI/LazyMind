import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { createRequire } from "node:module";
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import test from "node:test";
import vm from "node:vm";
const require = createRequire(import.meta.url);
const { testPaths, assertPaths, initializeTestProfile, activateTestProfile } = require("../electron/src/unsigned-test-profile");
const { createUnsignedTestInstaller } = require("../electron/src/unsigned-test-installer");
const { unsignedTestBuild } = require("./unsigned-test-build.cjs");

function fixture(t) {
  const home = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-unsigned-")));
  t.after(() => fs.rmSync(home, { recursive: true, force: true }));
  const account = { username: "lazymind-test", homedir: home }, profile = testPaths({ account, platform: "darwin" });
  const config = { mode: "unsigned-test", audience: "fixture", channel: "personal-test", trustDomain: "fixture", releaseId: "source",
    version: "1.0.0", platform: "darwin", arch: "arm64", signingIdentity: "adhoc", updaterProtocol: 1,
    policyURL: "http://127.0.0.1:18080/policy.json", allowedOrigins: ["http://127.0.0.1:18080"], publicKeys: { test: Buffer.alloc(32).toString("base64") } };
  return { home, account, profile, config };
}

test("unsigned build requires explicit opt-in and a private loopback identity", t => {
  const f = fixture(t), configPath = path.join(f.home, "build.json");
  const env = { LAZYMIND_DESKTOP_UPDATE_CONFIG: configPath, LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST: "true" };
  fs.writeFileSync(configPath, JSON.stringify(f.config)); assert.equal(unsignedTestBuild(env), true);
  assert.throws(() => unsignedTestBuild({ ...env, LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST: "false" }));
  for (const change of [{ audience: "official" }, { platform: "windows" }, { signingIdentity: "Official Team" }, { allowedOrigins: ["http://example.com"] }]) {
    fs.writeFileSync(configPath, JSON.stringify({ ...f.config, ...change })); assert.throws(() => unsignedTestBuild(env));
  }
  fs.writeFileSync(configPath, JSON.stringify({ mode: "official" })); assert.equal(unsignedTestBuild(env), false);
  assert.equal(unsignedTestBuild({ LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST: "true" }), false);
});

test("current-account test profile permits existing app data but refuses redirected storage", t => {
  const f = fixture(t), writes = [];
  const app = { setName: name => writes.push(name), setPath: (...args) => writes.push(args), setAppLogsPath: value => writes.push(value) };
  const profile = testPaths({ platform: "darwin", account: { ...f.account, username: "developer" } });
  assert.deepEqual(profile, f.profile);
  for (const username of ["", "root"]) assert.throws(() => testPaths({ platform: "darwin", account: { ...f.account, username } }));
  assert.throws(() => testPaths({ platform: "windows", account: f.account }));
  assert.throws(() => initializeTestProfile({ app, platform: "darwin", account: f.account, executable: "/Applications/LazyMind.app/Contents/MacOS/LazyMindApp" }));
  assert.deepEqual(writes, []); assert.equal(fs.existsSync(f.profile.profile), false);
  for (const name of ["LazyMind", "lazymind-desktop"]) {
    const existing = path.join(f.home, "Library", "Application Support", name); fs.mkdirSync(existing, { recursive: true });
    fs.writeFileSync(path.join(existing, "sentinel"), "existing test data");
  }
  assert.doesNotThrow(() => assertPaths(profile));
  activateTestProfile(app, profile);
  assert.ok(fs.existsSync(profile.profile));
  for (const name of ["LazyMind", "lazymind-desktop"]) {
    const existing = path.join(f.home, "Library", "Application Support", name);
    assert.deepEqual(fs.readdirSync(existing), ["sentinel"]);
    assert.equal(fs.readFileSync(path.join(existing, "sentinel"), "utf8"), "existing test data");
  }
  fs.symlinkSync(f.home, f.profile.updater); assert.throws(() => assertPaths(f.profile));
});

test("only the fixed installed test bundle activates isolated profile paths", t => {
  const f = fixture(t), before = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.startsWith("LAZYMIND_")));
  t.after(() => { for (const key of Object.keys(process.env)) if (key.startsWith("LAZYMIND_")) delete process.env[key]; Object.assign(process.env, before); });
  const values = {}, app = { setName: name => { values.name = name; }, setPath: (key, value) => { values[key] = value; }, setAppLogsPath: value => { values.logs = value; } };
  assert.throws(() => initializeTestProfile({ app, platform: "darwin", account: f.account, executable: "/Applications/LazyMind.app/Contents/MacOS/LazyMindApp" }));
  process.env.LAZYMIND_RUNTIME_ROOT = "/fixture/official";
  const p = initializeTestProfile({ app, platform: "darwin", account: f.account, executable: path.join(f.profile.application, "Contents/MacOS/LazyMindApp"), build: { lazymindUnsignedTest: true, lazymindUnsignedRelease: "source", version: "1.0.0" } });
  assert.equal(fs.existsSync(p.profile), false, "identity selection must not create state before startup guard");
  activateTestProfile(app, p); assert.equal(values.userData, p.profile); assert.equal(values.sessionData, p.profile);
  assert.equal(process.env.LAZYMIND_RUNTIME_ROOT, undefined); assert.equal(process.env.LAZYMIND_HOME, p.cli);
  assert.equal(process.env.LAZYMIND_UNSIGNED_EXPECTED_RELEASE, "source"); assert.equal(process.env.LAZYMIND_UNSIGNED_EXPECTED_VERSION, "1.0.0");
});

test("unsigned installer uses an external helper and quits only after explicit acceptance", async t => {
  const f = fixture(t), resources = path.join(f.profile.application, "Contents/Resources");
  fs.mkdirSync(path.join(resources, "runtime/bin"), { recursive: true });
  fs.writeFileSync(path.join(resources, "runtime/bin/unsigned-update-helper"), "fixture helper, never executable");
  fs.writeFileSync(path.join(resources, "unsigned-test.json"), JSON.stringify({ ...f.config, appId: "ai.lazymind.desktop.test" }));
  const calls = []; let installChild;
  const spawn = (file, args, options) => {
    assert.ok(file.startsWith(f.profile.updater+path.sep)); assert.equal(options.shell, false);
    const child = Object.assign(new EventEmitter(), { stdin: new PassThrough(), stdout: new PassThrough(), unref() {} });
    let input = ""; child.stdin.on("data", value => { input += value; });
    child.stdin.on("end", () => {
      calls.push(args[0]);
      if (args[0] === "prepare") { assert.equal(JSON.parse(input).release.releaseId, "target"); child.stdout.write('{"status":"prepared","id":"install-123"}\n'); child.emit("close", 0); }
      else installChild = child;
    }); return child;
  };
  const installer = createUnsignedTestInstaller({ profile: f.profile, resourcesPath: resources, currentVersion: "1.0.0", spawn,
    setQuitting: value => calls.push(value), quit: () => calls.push("quit") }); t.after(() => installer.dispose());
  assert.equal(installer.capability({ ...f.config, mode: "official" }).supported, false);
  const stage = await installer.prepare({ configuration: f.config, release: { releaseId: "target", version: "1.1.0" }, receipt: { releaseId: "target", version: "1.1.0", packagePath: "/fixture/package.zip" } });
  const first = stage.install(), second = stage.install(); assert.equal(first, second);
  await new Promise(resolve => setImmediate(resolve)); assert.deepEqual(calls, ["prepare", "install"]);
  installChild.stdout.write('{"status":"accepted"}\n'); assert.deepEqual(await first, { handoff: true });
  assert.deepEqual(calls, ["prepare", "install", true, "quit"]);
  await assert.rejects(installer.prepare({ configuration: f.config }), { code: "native_install_unavailable" });
});

test("official package excludes unsigned modules and the build compiles test identity explicitly", () => {
  const builder = fs.readFileSync(new URL("../electron/electron-builder.config.cjs", import.meta.url), "utf8");
  const build = fs.readFileSync(new URL("./build-darwin-arm64.sh", import.meta.url), "utf8");
  assert.match(builder, /!src\/unsigned-test-\*\.js/); assert.match(builder, /!bin\/unsigned-update-helper/);
  assert.match(builder, /ai\.lazymind\.desktop\.test/); assert.match(build, /-tags=lazymind_unsigned_test/);
  assert.match(build, /rm -f "\$\{RUNTIME_ROOT\}\/bin\/unsigned-update-helper"/);
});

test("actual builder configurations keep production identity and separate the unsigned package", async t => {
  const f = fixture(t), configPath = path.join(f.home, "build.json");
  fs.writeFileSync(configPath, JSON.stringify(f.config));
  const builderURL = new URL("../electron/electron-builder.config.cjs", import.meta.url);
  const builderRequire = createRequire(builderURL);
  const load = env => {
    const module = { exports: {} };
    vm.runInNewContext(fs.readFileSync(builderURL, "utf8"), { module, require: builderRequire,
      __dirname: path.dirname(builderURL.pathname), process: { platform: "darwin", arch: "arm64", env: { LAZYMIND_DESKTOP_RUNTIME_STAGE: f.home, ...env } },
      console, Buffer, setTimeout });
    return module.exports;
  };
  const official = load({}), unsigned = load({ LAZYMIND_DESKTOP_UPDATE_CONFIG: configPath, LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST: "true" });
  assert.equal(official.appId, "ai.lazymind.desktop"); assert.equal(official.productName, "LazyMind");
  assert.equal(official.extraMetadata, undefined); assert.ok(official.files.includes("!src/unsigned-test-*.js"));
  assert.ok(official.extraResources.find(x => x.to === "runtime").filter.includes("!bin/unsigned-update-helper"));
  assert.equal(unsigned.appId, "ai.lazymind.desktop.test"); assert.equal(unsigned.productName, "LazyMind Test");
  assert.equal(unsigned.extraMetadata.lazymindUnsignedTest, true); assert.ok(!unsigned.files.includes("!src/unsigned-test-*.js"));
  assert.equal(unsigned.extraMetadata.lazymindUnsignedRelease, f.config.releaseId);
  const { writeNativeUpdateMetadata } = require("./native-update-metadata.cjs");
  const previous = process.env.LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST;
  process.env.LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST = "true";
  t.after(() => { if (previous === undefined) delete process.env.LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST; else process.env.LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST = previous; });
  const context = { electronPlatformName: "darwin", arch: 3, appOutDir: f.home, packager: { appInfo: { productFilename: "LazyMind Test", version: "1.0.0" } } };
  await writeNativeUpdateMetadata(context, { configuration: f.config, signingMode: "adhoc" });
  const marker = path.join(f.home,"LazyMind Test.app/Contents/Resources/unsigned-test.json");
  assert.equal(JSON.parse(fs.readFileSync(marker)).appId,"ai.lazymind.desktop.test");
  await writeNativeUpdateMetadata(context, { configuration: undefined }); assert.equal(fs.existsSync(marker),false);
});
