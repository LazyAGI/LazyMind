import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";
import { mkdtempSync, rmSync, writeFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
const require = createRequire(import.meta.url);
const { createDesktopUpdater, registerUpdateIPC } = require("../electron/src/update-controller");
const { createDownloadIntentStore } = require("../electron/src/update-download-intent");
const { createInstallIntentStore } = require("../electron/src/update-install-intent");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client");
const settle = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
const release = { releaseId: "fixture-r2", version: "1.1.0", package: { size: 4, sha256: "a".repeat(64) } };
const decision = { status: "direct", next: release, route: [release.releaseId] };

function fixture(t, options = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), "lazymind-install-flow-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const store = createInstallIntentStore(path.join(dir, "install.json"));
  const downloadStore = createDownloadIntentStore(path.join(dir, "download.json"));
  const packagePath = path.join(dir, "fixture.zip"); writeFileSync(packagePath, "fake");
  let active = options.active || 0, paused = false;
  const calls = [], timers = new Set();
  const clock = { setTimeout(fn, ms) { const timer = { fn, ms }; timers.add(timer); return timer; }, clearTimeout(timer) { timers.delete(timer); } };
  const tick = async () => { const timer = [...timers].find(item => item.ms <= 2000); assert.ok(timer, "a waiting/countdown tick is scheduled"); timers.delete(timer); await timer.fn(); await settle(); };
  const runtime = {
    async verifyComponents() { return { dataProfile: "same-v1" }; },
    async status() { calls.push("status"); if (options.statusError) throw Object.assign(new Error(), { code: "task_state_unknown" }); return { active, paused, epoch: 0 }; },
    async pause(epoch) { calls.push("pause"); paused = true; return options.pause ? options.pause(epoch) : { active, paused, epoch }; },
    async resume() { calls.push("resume"); paused = false; },
    async stop() { calls.push("stop"); assert.equal(paused, true); assert.equal(store.read().phase, "installing", "intent must precede shutdown"); if (options.stopError) throw Object.assign(new Error(), { code: "runtime_stop_failed" }); },
    async verifyStartup() { calls.push("verify"); if (options.healthError) throw Object.assign(new Error(), { code: "update_startup_failed" }); return { configuration: { releaseId: options.bootRelease || release.releaseId, version: "1.1.0" }, local: { dataProfile: "same-v1", dirty: false } }; },
  };
  const make = (extra = {}) => {
    const updater = createDesktopUpdater({ currentVersion: "1.0.0", downloadIntent: downloadStore, installIntent: store, runtime, clock,
      readLocalState: async () => ({ dataProfile: "same-v1", dirty: false }), revealPackage: file => { calls.push(["reveal", file]); },
      nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, async prepare() { calls.push("prepare"); if (options.prepareError) throw Object.assign(new Error(), { code: "native_signature_invalid" }); return { async install() { calls.push("install"); return { handoff: true }; } }; } },
      invokeHelper: async (command, request, control) => {
        calls.push(command);
        if (command === "config") return { status: "enabled", config: { version: extra.currentVersion || "1.0.0" } };
        if (command === "discover") return options.discover?.() || (extra.currentVersion === release.version ? { status: "up_to_date", reason: "no_newer_compatible_release" } : decision);
        if (command === "download") {
          if (options.transfer) await options.transfer(control);
          return { status: "downloaded", releaseId: request.releaseId, version: "1.1.0", packagePath };
        }
        throw new Error(command);
      }, ...extra });
    t.after(() => updater.dispose()); return updater;
  };
  return { dir, store, calls, make, tick, packagePath, setActive: value => { active = value; }, paused: () => paused };
}
async function begin(f) { const u = f.make(); await u.check(); assert.equal(u.getState().canInstall, true); await u.install(release.releaseId); await settle(); return u; }
async function finishCountdown(f) { for (let i = 0; i < 10; i++) await f.tick(); }

test("one click downloads while work continues; newly started tasks delay the ten second countdown", async t => {
  const transfer = deferred(); const f = fixture(t, { active: 1, transfer: () => transfer.promise });
  const u = await begin(f); assert.equal(u.getState().phase, "downloading"); assert.equal(f.paused(), false);
  f.setActive(2); transfer.resolve(); await settle(); assert.equal(u.getState().phase, "waiting");
  await f.tick(); assert.equal(f.calls.includes("pause"), false);
  f.setActive(0); await f.tick(); assert.equal(u.getState().phase, "countdown"); assert.equal(u.getState().secondsRemaining, 10);
  for (let i = 0; i < 9; i++) await f.tick();
  assert.equal(f.calls.includes("stop"), false); assert.equal(u.getState().secondsRemaining, 1);
  await f.tick(); assert.equal(f.calls.filter(x => x === "stop").length, 1); assert.equal(f.calls.filter(x => x === "install").length, 1);
  assert.equal(u.getState().phase, "installing", "handoff is not installed success");
});
test("work admitted between idle observation and pause returns to waiting and releases admission", async t => {
  let first = true; const f = fixture(t, { pause: async epoch => ({ epoch, paused: true, active: first ? (first = false, 1) : 0 }) });
  const u = await begin(f); assert.equal(f.paused(), false); assert.equal(f.calls.includes("resume"), true); assert.equal(f.calls.includes("stop"), false);
  await f.tick(); assert.equal(u.getState().phase, "countdown");
  await u.later(); assert.equal(f.paused(), false); assert.equal(f.store.read(), null); assert.equal(u.getState().phase, "downloaded");
  await u.revealDownloaded(); assert.deepEqual(f.calls.at(-1), ["reveal", f.packagePath]);
});
test("cancel during an in-flight pause waits for its response and releases that exact pause before completion", async t => {
  const pause = deferred(); const f = fixture(t, { pause: () => pause.promise }); const u = await begin(f);
  let completed = false; const later = u.later().then(() => { completed = true; }); await settle(); assert.equal(completed, false);
  pause.resolve({ active: 0, paused: true, epoch: 1 }); await later;
  assert.equal(f.paused(), false); assert.equal(f.calls.includes("stop"), false); assert.equal(f.store.read(), null);
});
test("unknown tasks, native preparation and stop failures never fall through to installation", async t => {
  for (const options of [{ statusError: true }, { prepareError: true }, { stopError: true }]) {
    const f = fixture(t, options); const u = await begin(f);
    if (options.stopError) await finishCountdown(f);
    assert.equal(f.calls.includes("install"), false); assert.ok(["error", "repair"].includes(u.getState().phase));
    assert.equal(statSync(f.packagePath).size, 4); await u.revealDownloaded();
  }
});
test("revocation at the final compatibility check prevents service shutdown", async t => {
  let changed = false; const f = fixture(t, { discover: () => changed ? { status: "blocked", reason: "source_revoked" } : decision });
  const u = await begin(f); changed = true; await finishCountdown(f);
  assert.equal(f.calls.includes("stop"), false); assert.equal(f.paused(), false); assert.equal(u.getState().reason, "offer_changed");
});
test("pending update after shutdown is explicit continuation, with no automatic download or pause", async t => {
  const f = fixture(t, { active: 1 }); const first = await begin(f); first.dispose();
  const previous = f.calls.length; const second = f.make(); await second.start();
  assert.equal(second.getState().pendingUpdate, true); assert.equal(second.getState().phase, "interrupted"); assert.equal(f.calls.length, previous);
  await second.install(release.releaseId); await settle(); assert.equal(second.getState().phase, "waiting");
});
test("startup confirms both new build identity and healthy compatible data before success", async t => {
  for (const options of [{}, { healthError: true }, { bootRelease: "wrong-release" }]) {
    const f = fixture(t, options); f.store.write({ version: 1, phase: "installing", sourceVersion: "1.0.0", nextVersion: "1.1.0", releaseId: release.releaseId, dataProfile: "same-v1", packagePath: f.packagePath });
    const u = f.make({ currentVersion: "1.1.0" }); await u.start();
    assert.equal(u.getState().phase, Object.keys(options).length ? "repair" : "installed");
    assert.equal(f.calls.includes("install"), false); assert.equal(f.calls.includes("download"), false);
    assert.equal(f.store.read() === null, !Object.keys(options).length);
  }
});
test("old version after installer handoff requires manual repair, never an automatic second handoff", async t => {
  const f = fixture(t); f.store.write({ version: 1, phase: "installing", sourceVersion: "1.0.0", nextVersion: "1.1.0", releaseId: release.releaseId, dataProfile: "same-v1", packagePath: f.packagePath });
  const u = f.make(); await u.start(); assert.equal(u.getState().phase, "repair"); await assert.rejects(u.install(release.releaseId));
  assert.equal(f.calls.includes("download"), false); await u.revealDownloaded();
});
test("legacy download and cancel actions cannot mutate an installation after shutdown starts", async t => {
  const f = fixture(t); const u = await begin(f); await finishCountdown(f);
  const state = u.getState(); await assert.rejects(u.later(), { code: "update_incomplete" }); await assert.rejects(u.cancel(), { code: "update_incomplete" }); await assert.rejects(u.download(release.releaseId), { code: "native_update_busy" });
  assert.deepEqual(u.getState(), state); assert.equal(f.calls.filter(x => x === "install").length, 1);
});
test("disk failure prevents download and corrupt intent blocks unattended installation", async t => {
  const f = fixture(t); const u = f.make({ installIntent: { read: () => null, write() { throw new Error(); }, clear() {} } }); await u.check(); await assert.rejects(u.install(release.releaseId), { code: "update_record_write" }); assert.equal(f.calls.includes("download"), false);
  writeFileSync(path.join(f.dir, "install.json"), "broken"); const corrupt = f.make(); await corrupt.start(); assert.equal(corrupt.getState().phase, "repair"); await assert.rejects(corrupt.install(release.releaseId));
});
test("runtime bridge locks local admission before remote pause and validates all responses", async () => {
  const calls = []; let remote = { active: 0, paused: false, epoch: 0 }, open = true;
  const client = createUpdateRuntimeClient({ runSidecar: async (command, args) => { calls.push([command, args, open]); return JSON.stringify(remote); }, localWork: () => 2, setAdmission: value => { open = value; }, stopAuxiliary: async () => {}, verifyStartup: async () => {} });
  assert.equal((await client.status()).active, 2); remote = { active: 1, paused: true, epoch: 4 }; assert.equal((await client.pause(4)).active, 3); assert.equal(calls.at(-1)[2], false);
  await assert.rejects(client.stop(4), { code: "tasks_active" }); remote = { active: 0, paused: false, epoch: 0 }; await client.resume(4); assert.equal(open, true);
  remote = {}; await assert.rejects(client.status(), { code: "task_state_unknown" });
});
test("install and later IPC require the trusted main frame", async () => {
  const handlers = new Map(); let called = 0;
  registerUpdateIPC({ ipcMain: { handle: (name, fn) => handlers.set(name, fn) }, updater: { install: () => { called++; }, later: () => { called++; } }, authorize: event => event.trusted });
  for (const name of ["install", "later"]) await assert.rejects(handlers.get(`lazymind:update:${name}`)({ trusted: false }, release.releaseId), { code: "update_sender_untrusted" });
  assert.equal(called, 0); await handlers.get("lazymind:update:install")({ trusted: true }, release.releaseId); assert.equal(called, 1);
});
