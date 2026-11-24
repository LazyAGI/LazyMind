import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createUpdateInstallFlow } = require("../electron/src/update-install-flow");
const { createInstallIntentStore } = require("../electron/src/update-install-intent");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client");

for (const scenario of ["idle", "handoff write fails", "busy on the first final pause"]) {
  test(`v1 persists the verified component selection after the final idle check: ${scenario}`, async t => {
    const dir = mkdtempSync(path.join(os.tmpdir(), "v1-component-handoff-"));
    t.after(() => rmSync(dir, { recursive: true, force: true }));
    const events = [], profile = "sqlite-v1-" + "c".repeat(64);
    let awaitingFinalPause = false;
    let busyOnce = scenario === "busy on the first final pause";
    const release = { releaseId: "target", version: "1.1.0", package: { sha256: "a".repeat(64), size: 9 },
      components: { sha256: "b".repeat(64), size: 6 } };
    const receipt = { releaseId: "target", version: release.version, dataProfile: profile,
      packagePath: path.join(dir, "target.zip"), componentsPath: path.join(dir, "components.json") };
    const decision = { status: "direct", next: release };
    const runtime = createUpdateRuntimeClient({
      localWork: () => 0, setAdmission() {}, stopAuxiliary: async () => { events.push("stop-auxiliary"); },
      async runSidecar(command, args) {
        if (command === "update-components-check") {
          if (args.includes("--prepare-handoff")) {
            return JSON.stringify({ status: "prepared", releaseId: release.releaseId, attemptId: "d".repeat(32), dataProfile: profile });
          }
          if (args.includes("--handoff")) {
            assert.equal(args[args.indexOf("--handoff") + 1], release.releaseId);
            assert.equal(args[args.indexOf("--package") + 1], receipt.packagePath);
            assert.equal(args[args.indexOf("--components") + 1], receipt.componentsPath);
            events.push("persist");
            if (scenario === "handoff write fails") return JSON.stringify({ status: "blocked", reason: "update_record_write" });
          } else events.push("preflight");
          return JSON.stringify({ status: "compatible", dataProfile: profile });
        }
        assert.equal(command, "update-control", "v1 should not create a native-v2 transaction");
        const action = args[args.indexOf("--action") + 1];
        events.push(action);
        const epoch = action === "pause" ? Number(args[args.indexOf("--epoch") + 1]) : 0;
        if (action === "pause" && awaitingFinalPause) {
          awaitingFinalPause = false;
          const active = busyOnce ? 1 : 0;
          busyOnce = false;
          events.push(active ? "final-busy" : "final-idle");
          return JSON.stringify({ paused: true, epoch, active });
        }
        return JSON.stringify(action === "stop" ? { status: "stopped" } : { paused: epoch !== 0, epoch, active: 0 });
      },
    });
    let view = {};
    const flow = createUpdateInstallFlow({
      currentVersion: "1.0.0", runtime, store: createInstallIntentStore(path.join(dir, "reminder.json")),
      context: () => ({ configuration: { updaterProtocol: 1 }, offer: decision, receipt }),
      download: async () => {}, rediscover: async () => {
        events.push("rediscover");
        awaitingFinalPause = true;
        return { decision, local: { dataProfile: profile } };
      },
      clock: { setTimeout: setImmediate, clearTimeout: clearImmediate },
      nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, prepare: async () => ({ install: async () => {
        events.push("native"); return { handoff: true };
      } }) },
      publish: change => { view = { ...view, ...change }; }, getState: () => view,
    });
    t.after(() => flow.dispose());
    await flow.begin("target");
    for (let i = 0; i < 100 && !events.includes("native") && view.phase !== "error"; i++) {
      await new Promise(resolve => setImmediate(resolve));
    }
    const idle = events.lastIndexOf("final-idle");
    assert.ok(idle > events.lastIndexOf("rediscover"));
    if (scenario === "busy on the first final pause") {
      const busy = events.indexOf("final-busy"), resumed = events.indexOf("resume");
      assert.ok(busy >= 0 && busy < resumed && resumed < idle);
    }
    assert.ok(events.indexOf("persist") > idle, "only the final idle admission can authorize the durable handoff");
    assert.ok(!events.slice(0, idle).some(event => ["persist", "stop-auxiliary", "stop", "native"].includes(event)));
    if (scenario === "handoff write fails") {
      assert.equal(view.reason, "update_record_write");
      assert.ok(events.includes("resume"));
      assert.ok(!events.some(event => ["stop-auxiliary", "stop", "native"].includes(event)));
    } else {
      assert.deepEqual(events.filter(event => ["persist", "stop-auxiliary", "stop", "native"].includes(event)),
        ["persist", "stop-auxiliary", "stop", "native"]);
    }
  });
}
