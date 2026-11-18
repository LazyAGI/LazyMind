import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createHash } from "node:crypto";
import { copyFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createNativeUpdateInstaller } = require("../electron/src/native-update-installer.js");
const settle = () => new Promise(resolve => setImmediate(resolve));
const timeout = 1000;

async function fixture(t, platform) {
  const directory = mkdtempSync(path.join(tmpdir(), "lazymind-handoff-timeout-"));
  const bytes = Buffer.from("fixture package: never executable");
  const packagePath = path.join(directory, "original.zip"), stagedPath = path.join(directory, "staged.zip");
  writeFileSync(packagePath, bytes);
  const configuration = { trustDomain: "fixture", audience: "official", channel: "stable", mode: "official",
    releaseId: "source", version: "1.0.0", platform: platform === "win32" ? "windows" : "darwin", arch: "amd64",
    signingIdentity: "Fixture Publisher", updaterProtocol: 1 };
  writeFileSync(path.join(directory, "app-update.yml"), JSON.stringify({
    updaterCacheDirName: "LazyMind-fixture-official-stable", publisherName: [configuration.signingIdentity],
    lazymind: { protocol: 1, releaseId: "source", version: "1.0.0", mode: "official",
      platform: configuration.platform, arch: "amd64", signingIdentity: configuration.signingIdentity },
  }));
  const calls = [], native = new EventEmitter(), child = new EventEmitter();
  let quitting = false;
  child.unref = () => calls.push("unref");
  const updater = Object.assign(new EventEmitter(), {
    netSession: { setProxy: async () => {} }, setFeedURL() {},
    checkForUpdates: async () => ({ isUpdateAvailable: true, updateInfo: { version: "1.1.0" } }),
    downloadUpdate: async () => { copyFileSync(packagePath, stagedPath); return [stagedPath]; },
    quitAndInstall: () => calls.push("handoff"),
  });
  const installer = createNativeUpdateInstaller({ platform, arch: "x64", currentVersion: "1.0.0", isPackaged: true,
    resourcesPath: directory, createUpdater: () => updater, nativeAutoUpdater: native, handoffTimeoutMs: timeout,
    spawn: () => { calls.push("handoff"); return child; },
    setQuitting: value => { quitting = value; }, quit: () => calls.push("quit"),
  });
  t.after(() => { installer.dispose(); rmSync(directory, { recursive: true, force: true }); });
  const input = { configuration,
    release: { releaseId: "target", version: "1.1.0", platform: configuration.platform, arch: "amd64",
      signingIdentity: configuration.signingIdentity, package: { size: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") } },
    receipt: { releaseId: "target", version: "1.1.0", packagePath },
  };
  const stage = await installer.prepare(input);
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let outcome;
  const attempt = stage.install().then(value => { outcome = { value }; }, error => { outcome = { error }; });
  const deadline = Date.now() + 3000;
  while (!calls.includes("handoff")) { assert.ok(Date.now() < deadline, "handoff must start"); await settle(); }
  return { installer, input, stage, attempt, calls, packagePath, bytes,
    outcome: () => outcome, quitting: () => quitting,
    accept: () => platform === "win32" ? child.emit("spawn") : native.emit("before-quit-for-update"),
    fail: () => platform === "win32" ? child.emit("error", new Error("late fixture error")) : updater.emit("error", new Error("late fixture error")),
  };
}

for (const platform of ["win32", "darwin"]) {
  test(`${platform} handoff timeout remains unknown after late events and repeated install`, async t => {
    const f = await fixture(t, platform);
    t.mock.timers.tick(timeout - 1); await settle();
    assert.equal(f.outcome(), undefined, "cannot time out before the deadline");
    t.mock.timers.tick(1); await f.attempt;
    const error = f.outcome().error;
    assert.equal(error.code, "native_handoff_unknown"); assert.equal(error.handoffStarted, true);
    assert.equal(f.quitting(), platform === "darwin"); assert.ok(!f.calls.includes("quit"));
    f.accept(); f.fail(); await settle();
    assert.equal(f.outcome().error, error); assert.equal(f.quitting(), platform === "darwin");
    assert.ok(!f.calls.includes("quit"), "late acceptance cannot quit the running app");
    await assert.rejects(f.stage.install(), e => e === error);
    await assert.rejects(f.installer.prepare(f.input), { code: "native_update_busy" });
    assert.equal(f.calls.filter(call => call === "handoff").length, 1);
    assert.deepEqual(readFileSync(f.packagePath), f.bytes);
  });

  test(`${platform} acceptance before the deadline cancels the handoff timeout`, async t => {
    const f = await fixture(t, platform);
    t.mock.timers.tick(timeout - 1); await settle();
    assert.equal(f.outcome(), undefined);
    f.accept(); await f.attempt;
    assert.deepEqual(f.outcome(), { value: { handoff: true } });
    t.mock.timers.tick(timeout * 2); await settle();
    assert.deepEqual(f.outcome(), { value: { handoff: true } }); assert.equal(f.quitting(), true);
    assert.equal(f.calls.filter(call => call === "quit").length, platform === "win32" ? 1 : 0);
    assert.deepEqual(await f.stage.install(), { handoff: true });
    assert.equal(f.calls.filter(call => call === "handoff").length, 1);
  });
}
