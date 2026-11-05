import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createNativeUpdateInstaller } = require("../electron/src/native-update-installer.js");
const { nativeUpdateMetadata } = require("./native-update-metadata.cjs");
const bytes = Buffer.from("fixture update package, never executable");
const hash = value => createHash("sha256").update(value).digest("hex");
const metadata = config => ({ provider: "generic", url: "https://updates.invalid/disabled/",
  updaterCacheDirName: "LazyMind-fixture-official-stable", ...(config.platform === "windows" ? { publisherName: [config.signingIdentity] } : {}),
  lazymind: { protocol: 1, releaseId: config.releaseId, version: config.version, mode: config.mode,
    platform: config.platform, arch: config.arch, signingIdentity: config.signingIdentity } });

function fixture(t, { platform = "win32", rejectSignature = false, alterVersion = false, unavailable = false, spawnError, macError, manualEvents = false } = {}) {
  const directory = mkdtempSync(path.join(tmpdir(), "lazymind-native-update-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const configuration = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official",
    releaseId: "fixture-source", version: "1.0.0", platform: platform === "win32" ? "windows" : platform,
    arch: "amd64", signingIdentity: "Fixture Publisher", updaterProtocol: 1 };
  const packagePath = path.join(directory, platform === "win32" ? "original.exe" : "original.zip");
  const stagedPath = path.join(directory, platform === "win32" ? "staged.exe" : "staged.zip");
  writeFileSync(packagePath, bytes);
  writeFileSync(path.join(directory, "app-update.yml"), JSON.stringify(metadata(configuration)));
  const release = { releaseId: "fixture-target", version: "1.1.0", platform: configuration.platform, arch: "amd64",
    signingIdentity: configuration.signingIdentity, package: { sha256: hash(bytes), size: bytes.length } };
  const receipt = { releaseId: release.releaseId, version: release.version, packagePath };
  const calls = [], native = new EventEmitter();
  let feed, info, updater, child, quitting = false;
  class Updater extends EventEmitter {
    autoDownload = true;
    autoInstallOnAppQuit = true;
    allowDowngrade = true;
    netSession = { setProxy: async value => { assert.deepEqual(value, { mode: "direct" }); } };
    setFeedURL(value) { feed = value; calls.push("feed"); }
    async checkForUpdates() {
      assert.equal(this.autoDownload, false); assert.equal(this.autoInstallOnAppQuit, false);
      assert.equal(this.allowDowngrade, false); assert.equal(this.disableDifferentialDownload, true);
      assert.equal(this.disableWebInstaller, true);
      assert.match(feed.url, /^http:\/\/127\.0\.0\.1:\d+\/$/);
      const filename = platform === "win32" ? "latest.yml" : "latest-mac.yml";
      const denied = await fetch(new URL(filename, feed.url)); assert.equal(denied.status, 401);
      const response = await fetch(new URL(filename, feed.url), { headers: this.requestHeaders });
      assert.equal(response.status, 200); info = await response.json();
      assert.equal(info.version, release.version); assert.equal(info.files.length, 1);
      assert.equal(info.files[0].size, bytes.length);
      const url = new URL(info.files[0].url, feed.url);
      assert.equal(url.origin, new URL(feed.url).origin);
      calls.push("check");
      return { isUpdateAvailable: !unavailable, updateInfo: { ...info, version: alterVersion ? "9.0.0" : info.version } };
    }
    async downloadUpdate() {
      if (rejectSignature) throw Object.assign(new Error("fixture private signature details"), { code: "ERR_UPDATER_INVALID_SIGNATURE" });
      const response = await fetch(new URL(info.files[0].url, feed.url), { headers: this.requestHeaders });
      const content = Buffer.from(await response.arrayBuffer());
      assert.deepEqual(content, bytes);
      assert.equal(createHash("sha512").update(content).digest("base64"), info.files[0].sha512);
      writeFileSync(stagedPath, content); calls.push("verified-download");
      return [stagedPath];
    }
    quitAndInstall() {
      assert.equal(platform, "darwin", "Windows must not use library elevation fallbacks");
      assert.equal(quitting, true); calls.push("mac-install");
      if (!manualEvents) queueMicrotask(() => macError ? this.emit("error", new Error("fixture native failure")) : native.emit("before-quit-for-update"));
    }
  }
  const installer = createNativeUpdateInstaller({ platform, arch: "x64", currentVersion: "1.0.0", isPackaged: true,
    resourcesPath: directory, nativeAutoUpdater: native,
    createUpdater: () => { updater = new Updater(); return updater; },
    setQuitting: value => { quitting = value; }, quit: () => { assert.equal(quitting, true); calls.push("quit"); },
    spawn: (file, args, options) => {
      assert.equal(file, stagedPath);
      assert.deepEqual(args, ["--updated", "/S", "--force-run", "--simple-install"]);
      assert.equal(options.shell, false); assert.equal(options.detached, true);
      child = new EventEmitter(); child.unref = () => calls.push("unref");
      calls.push("spawn");
      if (!manualEvents) queueMicrotask(() => spawnError ? child.emit("error", Object.assign(new Error("fixture private path"), { code: spawnError })) : child.emit("spawn"));
      return child;
    },
  });
  t.after(() => installer.dispose());
  return { directory, configuration, release, receipt, installer, calls, packagePath, stagedPath, native,
    get child() { return child; },
    get updater() { return updater; }, get quitting() { return quitting; }, get feed() { return feed; } };
}

test("verified preparation only stages; an explicit Windows handoff spawns once and preserves the original", async t => {
  const f = fixture(t);
  assert.equal(f.installer.capability(f.configuration).supported, true);
  const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
  assert.deepEqual(f.calls, ["feed", "check", "verified-download"]);
  assert.equal(f.updater.autoInstallOnAppQuit, false);
  await Promise.all([prepared.install(), prepared.install()]);
  assert.equal(f.calls.filter(value => value === "spawn").length, 1);
  assert.equal(f.calls.filter(value => value === "quit").length, 1);
  assert.deepEqual(readFileSync(f.packagePath), bytes);
});

test("Mac staging does not invoke native installation until explicit handoff", async t => {
  const f = fixture(t, { platform: "darwin" });
  const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
  assert.ok(!f.calls.includes("mac-install"));
  await prepared.install();
  assert.equal(f.calls.filter(value => value === "mac-install").length, 1);
  assert.ok(!f.calls.includes("spawn"));
});

test("cache tampering before preparation or after verified staging never starts an installer", async t => {
  for (const platform of ["win32", "darwin"]) for (const stage of ["before", "after"]) {
    const f = fixture(t, { platform });
    const corrupt = Buffer.from(bytes); corrupt[0] ^= 1;
    if (stage === "before") {
      writeFileSync(f.packagePath, corrupt);
      await assert.rejects(f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt }));
      assert.equal(f.updater, undefined); assert.equal(f.calls.length, 0);
    } else {
      const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
      writeFileSync(f.stagedPath, corrupt); await assert.rejects(prepared.install());
    }
    assert.ok(!f.calls.includes("spawn")); assert.ok(!f.calls.includes("mac-install")); assert.ok(!f.calls.includes("quit"));
  }
});

test("native handoff waits for explicit spawn or native acceptance, including delayed failure", async t => {
  for (const platform of ["win32", "darwin"]) for (const succeeds of [true, false]) {
    const f = fixture(t, { platform, manualEvents: true });
    const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
    let settled = false;
    const attempt = prepared.install().then(value => { settled = true; return { value }; }, error => { settled = true; return { error }; });
    const deadline = Date.now() + 3000;
    while (platform === "win32" ? !f.child : !f.calls.includes("mac-install")) {
      assert.ok(Date.now() < deadline, "handoff was not requested");
      await new Promise(resolve => setTimeout(resolve, 1));
    }
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(settled, false); assert.ok(!f.calls.includes("quit"));
    if (platform === "win32") {
      if (succeeds) f.child.emit("spawn");
      else f.child.emit("error", Object.assign(new Error("fixture spawn failure"), { code: "EACCES" }));
    } else if (succeeds) f.native.emit("before-quit-for-update");
    else f.updater.emit("error", new Error("fixture native failure"));
    const outcome = await attempt;
    assert.equal(Boolean(outcome.error), !succeeds);
    if (!succeeds) { assert.equal(f.quitting, platform === "darwin"); assert.ok(!f.calls.includes("quit")); }
    if (platform === "win32" && succeeds) assert.equal(f.calls.filter(value => value === "quit").length, 1);
  }
});

test("signature rejection or changed native metadata is a failure, never an installation", async t => {
  for (const options of [{ rejectSignature: true }, { alterVersion: true }, { unavailable: true }]) {
    const f = fixture(t, options);
    await assert.rejects(f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt }), error => {
      assert.doesNotMatch(error.message, /private/); return true;
    });
    assert.ok(!f.calls.includes("spawn")); assert.ok(existsSync(f.packagePath));
  }
});

test("disposing the adapter invalidates a prepared handoff", async t => {
  const f = fixture(t);
  const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
  f.installer.dispose();
  await assert.rejects(prepared.install());
  assert.ok(!f.calls.includes("spawn"));
});

test("Windows spawn failures neither elevate nor quit and are not automatically retried", async t => {
  for (const spawnError of ["EACCES", "UNKNOWN", "ENOENT"]) {
    const f = fixture(t, { spawnError });
    const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
    await assert.rejects(prepared.install()); await assert.rejects(prepared.install());
    assert.equal(f.calls.filter(value => value === "spawn").length, 1);
    assert.ok(!f.calls.includes("quit")); assert.equal(f.quitting, false);
    assert.deepEqual(readFileSync(f.packagePath), bytes);
  }
});

test("Mac native dispatch failure retains the quit guard and downloaded package", async t => {
  const f = fixture(t, { platform: "darwin", macError: true });
  const prepared = await f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt });
  await assert.rejects(prepared.install()); assert.equal(f.quitting, true);
  assert.ok(existsSync(f.packagePath));
});

test("unsupported or mismatched package identity never creates a native updater", async t => {
  const f = fixture(t);
  for (const configuration of [{ ...f.configuration, mode: "unsigned-test" }, { ...f.configuration, releaseId: "other" }, { ...f.configuration, signingIdentity: "Other Publisher" }]) {
    assert.equal(f.installer.capability(configuration).supported, false);
    await assert.rejects(f.installer.prepare({ configuration, release: f.release, receipt: f.receipt }));
  }
  const meta = metadata(f.configuration); delete meta.publisherName;
  writeFileSync(path.join(f.directory, "app-update.yml"), JSON.stringify(meta));
  assert.equal(f.installer.capability(f.configuration).supported, false);
  assert.equal(f.updater, undefined);
});

test("a release or receipt outside the selected identity cannot be handed off", async t => {
  const f = fixture(t);
  for (const change of [{ receipt: { ...f.receipt, releaseId: "other" } }, { release: { ...f.release, signingIdentity: "Other Publisher" } }, { release: { ...f.release, platform: "darwin" } }]) {
    await assert.rejects(f.installer.prepare({ configuration: f.configuration, release: f.release, receipt: f.receipt, ...change }));
  }
  assert.equal(f.calls.length, 0);
});

test("native metadata preserves build identity and supplies an explicit Windows publisher", async () => {
  const configuration = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official", releaseId: "fixture-source",
    signingIdentity: "Fixture Publisher", updaterProtocol: 1, policyURL: "https://fixture.invalid/policy.json", allowedOrigins: ["https://fixture.invalid"], publicKeys: { fixture: Buffer.alloc(32, 7).toString("base64") } };
  const config = { ...configuration, version: "1.0.0", platform: "windows", arch: "amd64" };
  assert.deepEqual(await nativeUpdateMetadata({ configuration, packageVersion: "1.0.0", platform: "windows", arch: "amd64", signingMode: "authenticode" }), metadata(config));
  await assert.rejects(nativeUpdateMetadata({ configuration, packageVersion: "1.0.0", platform: "windows", arch: "amd64", signingMode: "none" }));
  assert.equal(await nativeUpdateMetadata({ configuration: undefined }), null);
});
