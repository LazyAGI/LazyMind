import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createInstallIntentStore } = require("../electron/src/update-install-intent");
const { createUpdateInstallFlow } = require("../electron/src/update-install-flow");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client");
const a = "a".repeat(32), b = "b".repeat(32), c = "c".repeat(32);
const release = { releaseId: "target", version: "2.0.0", package: { sha256: "1".repeat(64), size: 10 }, components: { sha256: "2".repeat(64), size: 1 } };
const offer = { status: "direct", next: release };
const prepared = (attemptId, previousAttemptId) => ({ status: "prepared", releaseId: release.releaseId, attemptId, previousAttemptId, dataProfile: "profile" });

test("inspect and discard allow the same native identity check as prepare and final authorization", async () => {
  const timeouts = [];
  const runtime = createUpdateRuntimeClient({ async runSidecar(_command, args, limits) {
    timeouts.push(limits.timeout);
    return JSON.stringify(args.includes("--inspect-handoff") || args.includes("--prepare-handoff")
      ? prepared(a) : { status: "compatible", dataProfile: "profile" });
  } });
  const receipt = { componentsPath: path.resolve("components.json"), packagePath: path.resolve("app.zip") };
  await runtime.verifyComponents(release, receipt);
  await runtime.verifyComponents(release, receipt, "prepare");
  await runtime.verifyComponents(release, receipt, "authorize", a);
  await runtime.inspectV1Handoff();
  await runtime.discardV1Handoff(release.releaseId, a);
  assert.deepEqual(timeouts, [30_000, 180_000, 180_000, 180_000, 180_000]);
});

function fixture(t, phase, attemptId) {
  const dir = mkdtempSync(path.join(os.tmpdir(), "v1-attempt-binding-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const store = createInstallIntentStore(path.join(dir, "install.json"));
  const packagePath = path.join(dir, "app.zip");
  store.write({ version: 1, sourceVersion: "1.0.0", nextVersion: release.version, releaseId: release.releaseId,
    phase, attemptId, packagePath, dataProfile: "profile" });
  let proof = prepared(a);
  const open = (override = {}) => {
    let state = {};
    const flow = createUpdateInstallFlow({
      store, currentVersion: "1.0.0", context: () => ({ configuration: { updaterProtocol: 1 }, offer, receipt: { packagePath, dataProfile: "profile" } }),
      runtime: { inspectV1Handoff: async () => proof, verifyComponents: async () => ({ dataProfile: "profile" }),
        status: async () => ({ active: 0 }), pause: async epoch => ({ paused: true, active: 0, epoch }), resume: async () => {},
        discardV1Handoff: async () => assert.fail("must not discard a different attempt"), ...override },
      nativeInstaller: { capability: () => ({ supported: true }), prepare: async () => ({ install: async () => assert.fail("unexpected native dispatch") }), dispose() {} },
      download: async () => {}, cancelDownload() {}, rediscover: async () => ({ decision: offer, local: { dataProfile: "profile" } }),
      clock: { setTimeout: setImmediate, clearTimeout: clearImmediate },
      publish: patch => { state = { ...state, ...patch }; }, getState: () => state,
    });
    t.after(() => flow.dispose());
    return { flow, view: () => state };
  };
  return { store, open, setProof(value) { proof = value; } };
}

for (const phase of ["pending", "installing"]) {
  test(`observing prepared A in ${phase} binds the reminder across a second restart`, async t => {
    const f = fixture(t, phase, phase === "installing" ? a : undefined);
    const first = f.open();
    await first.flow.startup();
    assert.equal(first.view().phase, "interrupted");
    first.flow.dispose();
    const observed = f.store.read();
    assert.equal(observed.attemptId, a);
    f.setProof(prepared(c, a)); // The shell never explicitly continued A.
    const second = f.open();
    await second.flow.startup();
    assert.equal(second.view().phase, "repair");
    await assert.rejects(second.flow.later());
    assert.deepEqual(f.store.read(), observed);
  });
}

for (const point of ["before Go commits", "after Go commits before reply", "after reply", "wrong predecessor"]) {
  test(`an interrupted explicit takeover preserves its exact binding: ${point}`, async t => {
    const f = fixture(t, "pending", a);
    let atBoundary = false, unblock, returned = false;
    const held = new Promise(resolve => { unblock = resolve; });
    t.after(unblock);
    const first = f.open({
      async verifyComponents(_release, _receipt, mode, attempt) {
        if (mode !== "prepare") return { dataProfile: "profile" };
        assert.equal(attempt, a, "begin lost the inspected attempt");
        assert.equal(f.store.read().attemptId, a, "begin erased the durable prior attempt");
        if (point !== "before Go commits") f.setProof(prepared(b, point === "wrong predecessor" ? c : a));
        if (point !== "after reply") { atBoundary = true; await held; }
        returned = true;
        return prepared(b, a);
      },
      async pause(epoch) {
        if (returned) { atBoundary = true; await held; }
        return { paused: true, active: 0, epoch };
      },
    });
    await first.flow.startup();
    await first.flow.begin(release.releaseId);
    for (let i = 0; !atBoundary && i < 100; i++) await new Promise(resolve => setImmediate(resolve));
    assert.ok(atBoundary, JSON.stringify(first.view()));
    first.flow.dispose();
    if (point === "after reply") assert.equal(f.store.read().attemptId, b);
    const second = f.open();
    await second.flow.startup();
    if (point === "wrong predecessor") {
      assert.equal(second.view().phase, "repair");
      await assert.rejects(second.flow.later());
      assert.equal(f.store.read().attemptId, a);
      return;
    }
    const recovered = point === "before Go commits" ? a : b;
    assert.equal(second.view().phase, "interrupted");
    assert.equal(f.store.read().attemptId, recovered);
    second.flow.dispose();
    f.setProof(prepared(c, recovered));
    const third = f.open();
    await third.flow.startup();
    assert.equal(third.view().phase, "repair", "an observation cannot authorize another takeover");
    assert.equal(f.store.read().attemptId, recovered);
  });
}
