import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const copy = value => JSON.parse(JSON.stringify(value));
const settle = () => new Promise(resolve => setImmediate(resolve));
const sourceRevision = "a".repeat(64), targetRevision = "b".repeat(64);

function fixture(t, options = {}) {
  const root = mkdtempSync(path.join(tmpdir(), "lazymind-sync-target-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const target = {
    id: "rag", revision: options.sameTarget ? sourceRevision : targetRevision,
    tag: "c".repeat(64), sha256: "d".repeat(64), platform: "darwin", arch: "arm64",
    pythonAbi: "cp311", baseFingerprint: "e".repeat(64), packages: { fixture: "1" },
  };
  const source = { ...target, revision: sourceRevision };
  const catalog = Buffer.from(JSON.stringify({ schemaVersion: 1, platform: "darwin", arch: "arm64",
    components: { rag: target } }));
  const componentsPath = path.join(root, "components.json"), packagePath = path.join(root, "app.zip");
  writeFileSync(componentsPath, catalog);
  const packageBytes = Buffer.from("synthetic, never executable");
  writeFileSync(packagePath, packageBytes);
  const release = {
    releaseId: "target", version: "1.1.0",
    package: { size: packageBytes.length, sha256: createHash("sha256").update(packageBytes).digest("hex") },
    components: { size: catalog.length, sha256: createHash("sha256").update(catalog).digest("hex") },
  };
  const receipt = { status: "downloaded", releaseId: "target", version: "1.1.0", packagePath, componentsPath };
  const local = {
    dataProfile: "format-v2-fixture", dirty: false,
    components: options.absent ? {} : { rag: sourceRevision },
    componentMetadata: options.absent ? {} : { rag: source },
  };
  const calls = [], timers = new Set();
  let record;
  // The store is a fake external state-writer boundary, not a second authority.
  // Backend-owned plan fields survive the coordinator's phase transitions.
  const store = {
    read: () => record ? copy(record) : null,
    write: value => { record = { ...record, ...copy(value) }; },
    clear: () => { calls.push("clear"); record = undefined; },
  };
  const runtime = {
    async prepareComponents(actualRelease, actualReceipt) {
      calls.push("prepare-components");
      assert.deepEqual(actualRelease.components, release.components);
      assert.equal(actualReceipt.componentsPath, componentsPath);
      if (options.prepareError) throw Object.assign(new Error("fixture rejected component"), { code: options.prepareError });
      if (options.prepareWait) await options.prepareWait;
      const plan = {
        status: "prepared", transactionId: "1".repeat(32),
        componentMode: options.absent ? "absent" : "sync-target",
        sourceHadRAG: !options.absent, targetRAG: options.absent ? undefined : target,
        dataProfile: local.dataProfile,
      };
      record = { ...record, ...copy(plan) };
      return plan;
    },
    async verifyComponents() { calls.push("verify-components"); return { dataProfile: local.dataProfile }; },
    async status() { calls.push("status"); return { active: 0, paused: false, epoch: 0 }; },
    async pause(epoch) { calls.push("pause"); return { active: 0, paused: true, epoch }; },
    async resume() { calls.push("resume"); },
    async stop() { calls.push("stop"); },
    async verifyStartup() { throw new Error("not used before handoff"); },
  };
  const updater = createDesktopUpdater({
    currentVersion: "1.0.0", installIntent: store, runtime,
    clock: {
      setTimeout(fn, ms) { const timer = { fn, ms }; timers.add(timer); return timer; },
      clearTimeout(timer) { timers.delete(timer); },
    },
    readLocalState: async () => copy(local),
    nativeInstaller: {
      capability: () => ({ supported: true }), dispose() {}, discardPreparation: async () => {},
      async prepare() {
        calls.push("native-prepare");
        return { async install() {
          calls.push("install");
          if (options.unknownHandoff) throw Object.assign(new Error("fixture unknown handoff"),
            { code: "native_handoff_unknown", handoffStarted: true });
          return { handoff: true };
        } };
      },
    },
    invokeHelper: async command => command === "config"
      ? { status: "enabled", config: { version: "1.0.0", updaterProtocol: options.legacy ? 1 : 2 } }
      : command === "discover" ? { status: "direct", next: release, route: ["target"] }
      : receipt,
  });
  t.after(() => updater.dispose());
  return {
    updater, calls, runtime, store, target, local,
    async begin() { await updater.check(); await updater.install("target"); await settle(); },
    async countdown() {
      for (let i = 0; i < 10; i++) {
        const timer = [...timers].find(value => value.ms === 1000);
        assert.ok(timer, "countdown must be reached only after component preparation");
        timers.delete(timer);
        timer.fn();
        await settle();
      }
    },
  };
}

for (const sameTarget of [false, true]) {
  test("installed RAG always prepares the exact Desktop target; same target=" + sameTarget, async t => {
    const f = fixture(t, { sameTarget });
    await f.begin();
    assert.equal(f.calls.filter(call => call === "prepare-components").length, 1);
    assert.ok(f.calls.indexOf("prepare-components") < f.calls.indexOf("native-prepare"));
    assert.equal(f.store.read().componentMode, "sync-target");
    assert.equal(f.store.read().targetRAG.revision, f.target.revision);
    assert.equal(f.local.components.rag, sourceRevision, "preparation cannot activate under the old Desktop");
    await f.countdown();
    assert.equal(f.calls.filter(call => call === "install").length, 1);
  });
}

test("uninstalled RAG remains absent while the Desktop update proceeds", async t => {
  const f = fixture(t, { absent: true });
  await f.begin();
  assert.equal(f.store.read().componentMode, "absent");
  assert.equal(f.store.read().targetRAG, undefined);
  await f.countdown();
  assert.ok(f.calls.includes("install"));
  assert.deepEqual(f.local.components, {});
});

test("damaged source RAG cannot bypass preparation as a lightweight install", async t => {
  const f = fixture(t, { prepareError: "component_state_unknown" });
  await f.begin();
  assert.equal(f.updater.getState().reason, "component_state_unknown");
  assert.ok(!f.calls.includes("native-prepare"));
  assert.ok(!f.calls.includes("stop"));
  assert.ok(!f.calls.includes("install"));
});

test("inconsistent preparation response cannot turn installed RAG into absent", async t => {
  const f = fixture(t);
  const prepare = f.runtime.prepareComponents;
  f.runtime.prepareComponents = async (...args) => ({ ...await prepare(...args), componentMode: "absent" });
  await f.begin();
  assert.ok(!f.calls.includes("native-prepare"));
  assert.ok(!f.calls.includes("stop"));
  assert.ok(["error", "blocked", "repair"].includes(f.updater.getState().phase));
});

test("component preparation failure leaves current work and selection running", async t => {
  const f = fixture(t, { prepareError: "artifact_hash" });
  await f.begin();
  assert.ok(!f.calls.includes("pause"));
  assert.ok(!f.calls.includes("stop"));
  assert.equal(f.local.components.rag, sourceRevision);
});

test("later waits for ongoing preparation and never advances to native handoff", async t => {
  let release;
  const wait = new Promise(resolve => { release = resolve; });
  const f = fixture(t, { prepareWait: wait });
  t.after(release);
  await f.begin();
  assert.ok(f.calls.includes("prepare-components"));
  assert.ok(!f.calls.includes("native-prepare"));
  let done = false;
  const later = f.updater.later().then(() => { done = true; });
  await settle();
  assert.equal(done, false);
  release();
  await later;
  assert.ok(!f.calls.includes("install"));
  assert.equal(f.local.components.rag, sourceRevision);
});

test("actual native handoff unknown cannot resume ordinary admission", async t => {
  const f = fixture(t, { legacy: true, unknownHandoff: true });
  await f.begin();
  await f.countdown();
  assert.equal(f.updater.getState().phase, "repair");
  assert.equal(f.store.read().phase, "repair");
  assert.ok(!f.calls.includes("resume"), "dispatched native installation cannot be cancelled by finally");
  assert.equal(f.calls.filter(call => call === "install").length, 1);
});
