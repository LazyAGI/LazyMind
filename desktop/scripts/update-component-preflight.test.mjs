import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client.js");
const { createInstallIntentStore } = require("../electron/src/update-install-intent.js");
const { createDownloadIntentStore } = require("../electron/src/update-download-intent.js");
const settle = () => new Promise(resolve => setImmediate(resolve));
const failure = code => Object.assign(new Error(code), { code });

function fixture(t, check) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "component-preflight-"));
  const store = createInstallIntentStore(path.join(dir, "install.json"));
  const download = createDownloadIntentStore(path.join(dir, "download.json"));
  const target = { releaseId: "target", version: "1.1.0", package: { size: 4, sha256: "a".repeat(64) }, components: { size: 2, sha256: "b".repeat(64) } };
  const receipt = { status: "downloaded", releaseId: target.releaseId, version: target.version, packagePath: path.join(dir, "package.zip"), componentsPath: path.join(dir, "components.json") };
  fs.writeFileSync(receipt.packagePath, "fake"); fs.writeFileSync(receipt.componentsPath, "{}");
  const calls = [], timers = new Set(); let paused = false, checks = 0;
  const runtime = {
    async verifyComponents(release, actual) { calls.push("components"); assert.deepEqual(release, target); assert.equal(actual.componentsPath, receipt.componentsPath); return check(++checks); },
    async status() { return { active: 0, paused: false, epoch: 0 }; },
    async pause(epoch) { paused = true; return { active: 0, paused: true, epoch }; },
    async resume() { calls.push("resume"); paused = false; },
    async stop() { calls.push("stop"); },
  };
  const u = createDesktopUpdater({ currentVersion: "1.0.0", installIntent: store, downloadIntent: download, runtime,
    clock: { setTimeout(fn, ms) { const timer = { fn, ms }; timers.add(timer); return timer; }, clearTimeout(t) { timers.delete(t); } },
    readLocalState: async () => ({ dataProfile: "fixture", dirty: false }),
    nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, async discardPreparation() { calls.push("discard"); },
      async prepare() { calls.push("prepare"); return { async install() { calls.push("install"); return { handoff: true }; } }; } },
    invokeHelper: async command => command === "config" ? { status: "enabled", config: { version: "1.0.0" } }
      : command === "discover" ? { status: "direct", next: target, route: [target.releaseId] } : receipt,
  });
  t.after(() => { u.dispose(); fs.rmSync(dir, { recursive: true, force: true }); });
  return { u, store, download, calls, runtime, target, paused: () => paused,
    async begin() { await u.check(); await u.install(target.releaseId); await settle(); },
    async countdown() { for (let i = 0; i < 10; i++) { const timer = [...timers].find(t => t.ms === 1000); assert.ok(timer); timers.delete(timer); timer.fn(); await settle(); } },
  };
}

test("incompatible components are rejected before native preparation and leave no automatic continuation", async t => {
  const f = fixture(t, () => { throw failure("component_update_incompatible"); }); await f.begin();
  assert.equal(f.u.getState().phase, "blocked"); assert.equal(f.u.getState().reason, "component_update_incompatible");
  assert.equal(f.calls.includes("prepare"), false); assert.equal(f.calls.includes("stop"), false); assert.equal(f.calls.includes("install"), false);
  assert.equal(f.store.read(), null); assert.equal(f.download.read(), null); assert.equal(f.u.getState().releaseId, undefined);
  await f.u.check(); assert.equal(f.u.getState().phase, "available", "a later compatible publication can be checked without restarting");
});

test("component selection is checked again after admission pauses and before shutdown", async t => {
  const f = fixture(t, n => { if (n === 2) throw failure("component_update_incompatible"); return { dataProfile: "fixture" }; });
  await f.begin(); assert.equal(f.u.getState().phase, "countdown"); await f.countdown();
  assert.equal(f.calls.filter(x => x === "components").length, 2); assert.equal(f.u.getState().phase, "blocked");
  assert.equal(f.paused(), false); assert.equal(f.calls.includes("stop"), false); assert.equal(f.calls.includes("install"), false);
  assert.equal(f.store.read(), null);
});

test("same identity proceeds, but a changed final measured profile cannot authorize installation", async t => {
  for (const changed of [false, true]) {
    const f = fixture(t, n => ({ dataProfile: changed && n === 2 ? "changed-profile" : "fixture" }));
    await f.begin(); await f.countdown(); assert.equal(f.calls.includes("install"), !changed);
    assert.equal(f.calls.includes("stop"), !changed);
    if (changed) assert.equal(f.paused(), false);
  }
});

test("missing preflight capability fails closed", async t => {
  const f = fixture(t, () => ({ dataProfile: "fixture" })); delete f.runtime.verifyComponents;
  await f.begin(); assert.equal(f.calls.includes("prepare"), false); assert.equal(f.calls.includes("stop"), false);
});

test("later during component verification waits and never starts native preparation", async t => {
  let complete;
  const waiting = new Promise(resolve => { complete = resolve; });
  const f = fixture(t, () => waiting); await f.begin();
  assert.equal(f.calls.includes("prepare"), false);
  let finished = false; const later = f.u.later().then(() => { finished = true; }); await settle();
  assert.equal(finished, false); complete({ dataProfile: "fixture" }); await later;
  assert.equal(f.calls.includes("prepare"), false); assert.equal(f.calls.includes("stop"), false); assert.equal(f.store.read(), null);
});

test("changed final catalog pins prevent shutdown even when the package pin is unchanged", async t => {
  const f = fixture(t, () => ({ dataProfile: "fixture" })); await f.begin();
  f.target.components.sha256 = "c".repeat(64); await f.countdown();
  assert.equal(f.u.getState().reason, "offer_changed"); assert.equal(f.calls.includes("stop"), false); assert.equal(f.paused(), false);
});

test("rejected-offer cleanup failure retains the pending intent and remains retryable", async t => {
  const f = fixture(t, () => { throw failure("component_update_incompatible"); });
  const clear = f.store.clear; f.store.clear = () => { throw new Error("fixture cleanup failed"); };
  await f.begin(); assert.equal(f.u.getState().reason, "update_record_write"); assert.equal(f.u.getState().phase, "error");
  assert.equal(f.store.read().phase, "pending"); assert.equal(f.calls.includes("stop"), false);
  f.store.clear = clear; await f.u.later(); assert.equal(f.store.read(), null);
});

test("failed admission recovery after a final component rejection stays in repair", async t => {
  const f = fixture(t, n => { if (n === 2) throw failure("component_update_incompatible"); return { dataProfile: "fixture" }; });
  f.runtime.resume = async () => { throw failure("update_resume_failed"); };
  await f.begin(); await f.countdown();
  assert.equal(f.u.getState().phase, "repair"); assert.equal(f.u.getState().reason, "update_resume_failed");
  assert.equal(f.store.read().phase, "repair"); assert.equal(f.calls.includes("stop"), false);
  await assert.rejects(f.u.install(), { code: "update_incomplete" });
});

test("work admitted while a slow final check outlives the pause lease prevents shutdown", async t => {
  let busy = false;
  const f = fixture(t, n => { if (n === 2) busy = true; return { dataProfile: "fixture" }; });
  const pause = f.runtime.pause;
  f.runtime.pause = async epoch => ({ ...await pause(epoch), active: busy ? 1 : 0 });
  f.runtime.status = async () => ({ active: busy ? 1 : 0, paused: false, epoch: 0 });
  await f.begin(); await f.countdown();
  assert.equal(f.u.getState().phase, "waiting"); assert.equal(f.paused(), false);
  assert.equal(f.calls.includes("stop"), false); assert.equal(f.calls.includes("install"), false);
});

test("runtime client binds the private catalog path to the signed artifact and rejects malformed responses", async () => {
  const calls = []; let response = { status: "compatible", dataProfile: "fixture" };
  const client = createUpdateRuntimeClient({ runSidecar: async (...args) => { calls.push(args); return JSON.stringify(response); } });
  const release = { components: { sha256: "a".repeat(64), size: 2 } }, receipt = { componentsPath: path.join(os.tmpdir(), "components.json") };
  assert.deepEqual(await client.verifyComponents(release, receipt), { dataProfile: "fixture" });
  assert.deepEqual(calls[0].slice(0, 2), ["update-components-check", ["--components", receipt.componentsPath, "--sha256", release.components.sha256, "--size", "2"]]);
  response = { status: "blocked", reason: "component_update_incompatible" };
  await assert.rejects(client.verifyComponents(release, receipt), { code: "component_update_incompatible" });
  response = { status: "compatible" }; await assert.rejects(client.verifyComponents(release, receipt));
  await assert.rejects(client.verifyComponents(release, {}));
});
