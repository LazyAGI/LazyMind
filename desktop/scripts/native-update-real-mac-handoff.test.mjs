import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { EventEmitter } from "node:events";
import { copyFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const require = createRequire(import.meta.url);
const adapterPath = fileURLToPath(new URL("../electron/src/native-update-installer.js", import.meta.url));
const dependencyPath = require.resolve("../electron/node_modules/electron-updater/out/MacUpdater.js");
const settle = () => new Promise(resolve => setImmediate(resolve));
const timeout = 1000;

async function fixture(t) {
  const root = mkdtempSync(path.join(tmpdir(), "lazymind-real-mac-handoff-"));
  const bytes = Buffer.from("synthetic update package; never executable");
  const packagePath = path.join(root, "source.zip"), stagedPath = path.join(root, "stage.zip");
  writeFileSync(packagePath, bytes);
  const native = new EventEmitter();
  let nativeCalls = 0, nativeChecks = 0, quitting = false;
  native.checkForUpdates = () => { nativeChecks++; };
  native.quitAndInstall = () => { nativeCalls++; };
  class AppUpdater extends EventEmitter {
    constructor() {
      super();
      this._logger = { debug() {}, info() {}, warn() {} };
      this.netSession = { setProxy: async () => {} };
      this.app = { quit() { throw new Error("unexpected plain app quit"); } };
    }
    setFeedURL() {}
    async checkForUpdates() { return { isUpdateAvailable: true, updateInfo: { version: "1.1.0" } }; }
    async downloadUpdate() { copyFileSync(packagePath, stagedPath); return [stagedPath]; }
  }
  const dependency = { exports: {} };
  vm.runInNewContext(readFileSync(dependencyPath, "utf8"), {
    exports: dependency.exports,
    require: name => name === "./AppUpdater" ? { AppUpdater }
      : name === "electron" ? { autoUpdater: native } : {},
  }, { filename: dependencyPath });
  const modules = new Map();
  function load(filename) {
    if (modules.has(filename)) return modules.get(filename).exports;
    const module = { exports: {} };
    modules.set(filename, module);
    const localRequire = createRequire(filename);
    vm.runInNewContext(readFileSync(filename, "utf8"), {
      module, exports: module.exports, __dirname: path.dirname(filename), Buffer, process,
      setTimeout: (...args) => globalThis.setTimeout(...args),
      clearTimeout: (...args) => globalThis.clearTimeout(...args),
      require(name) {
        if (name === "electron") return { autoUpdater: native };
        if (name === "electron-updater") return dependency.exports;
        if (name.startsWith(".")) return load(localRequire.resolve(name));
        return localRequire(name);
      },
    }, { filename });
    return module.exports;
  }
  const configuration = {
    trustDomain: "fixture", audience: "official", channel: "stable", mode: "official",
    releaseId: "source", version: "1.0.0", platform: "darwin", arch: "amd64",
    signingIdentity: "Fixture Publisher", updaterProtocol: 1,
  };
  writeFileSync(path.join(root, "app-update.yml"), JSON.stringify({
    updaterCacheDirName: "LazyMind-fixture-official-stable",
    lazymind: { protocol: 1, releaseId: "source", version: "1.0.0", mode: "official",
      platform: "darwin", arch: "amd64", signingIdentity: configuration.signingIdentity },
  }));
  const { createNativeUpdateInstaller } = load(adapterPath);
  const installer = createNativeUpdateInstaller({
    platform: "darwin", arch: "x64", currentVersion: "1.0.0", isPackaged: true,
    resourcesPath: root, nativeAutoUpdater: native, handoffTimeoutMs: timeout,
    setQuitting: value => { quitting = value; },
  });
  t.after(() => { installer.dispose(); rmSync(root, { recursive: true, force: true }); });
  const input = {
    configuration,
    release: { releaseId: "target", version: "1.1.0", platform: "darwin", arch: "amd64",
      signingIdentity: configuration.signingIdentity,
      package: { size: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") } },
    receipt: { releaseId: "target", version: "1.1.0", packagePath },
  };
  const stage = await installer.prepare(input);
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let outcome;
  const attempt = stage.install().then(value => { outcome = { value }; }, error => { outcome = { error }; });
  const deadline = Date.now() + 3000;
  while (nativeChecks === 0 && nativeCalls === 0) {
    assert.ok(Date.now() < deadline, "real dependency must enter native preparation");
    await settle();
  }
  return {
    native, installer, stage, input, attempt,
    calls: () => nativeCalls, quitting: () => quitting, outcome: () => outcome,
    expire: async () => { t.mock.timers.tick(timeout); await attempt; },
  };
}

test("real MacUpdater late downloaded cannot install after pre-handoff timeout", async t => {
  const f = await fixture(t);
  assert.equal(f.calls(), 0);
  await f.expire();
  assert.ok(f.outcome().error);
  f.native.emit("update-downloaded");
  await settle();
  assert.equal(f.calls(), 0, "timeout must invalidate the callback that actually invokes native installation");
  assert.equal(f.quitting(), false);
  await assert.rejects(f.stage.install());
});

test("real MacUpdater dispose invalidates downloaded before native dispatch", async t => {
  const f = await fixture(t);
  f.installer.dispose();
  await f.attempt;
  f.native.emit("update-downloaded");
  assert.equal(f.calls(), 0);
});

test("native dispatch followed by unknown timeout keeps the quit constraint", async t => {
  const f = await fixture(t);
  f.native.emit("update-downloaded");
  assert.equal(f.calls(), 1);
  await f.expire();
  assert.equal(f.outcome().error.code, "native_handoff_unknown");
  assert.equal(f.quitting(), true, "a dispatched install is not a cancellable preparation");
  f.native.emit("update-downloaded");
  assert.equal(f.calls(), 1, "unknown handoff must never dispatch a second installation");
});

test("accepted real native handoff installs once despite repeated downloaded events", async t => {
  const f = await fixture(t);
  f.native.emit("update-downloaded");
  assert.equal(f.calls(), 1);
  f.native.emit("before-quit-for-update");
  await f.attempt;
  assert.equal(f.outcome().value?.handoff, true);
  f.native.emit("update-downloaded");
  t.mock.timers.tick(timeout * 2);
  assert.equal(f.calls(), 1);
  assert.equal(f.quitting(), true);
});
