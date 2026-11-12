import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client");
const { createInstallIntentStore } = require("../electron/src/update-install-intent");

test("failed admission recovery remains repair across repeated later/install actions and process recreation", async t => {
  const dir = mkdtempSync(path.join(tmpdir(), "lazymind-resume-admission-")); t.after(() => rmSync(dir, { recursive: true, force: true }));
  const intent = createInstallIntentStore(path.join(dir, "intent.json"));
  let admitted = true; const commands = [], timers = new Set();
  const clock = { setTimeout(fn, ms) { const timer = { fn, ms }; timers.add(timer); return timer; }, clearTimeout(timer) { timers.delete(timer); } };
  const runtime = createUpdateRuntimeClient({ localWork: () => 0, setAdmission: value => { admitted = value; }, stopAuxiliary: async () => { throw new Error("must not stop"); }, verifyStartup: async () => { throw new Error("must not verify"); },
    runSidecar: async (command, args) => {
      if (command === "update-components-check") return JSON.stringify({ status: "compatible", dataProfile: "fixture-profile" });
      const action = args[1], epoch = Number(args[3] || 0); commands.push(action);
      if (action === "resume") throw new Error("fixture unavailable");
      assert.ok(["status", "pause"].includes(action)); return JSON.stringify({ active: 0, paused: action === "pause", epoch: action === "pause" ? epoch : 0 });
    },
  });
  const release = { releaseId: "fixture-r2", version: "1.1.0", package: { size: 4 }, components: { size: 2, sha256: "b".repeat(64) } };
  const make = (extra = {}) => createDesktopUpdater({ currentVersion: "1.0.0", installIntent: intent, runtime, clock, readLocalState: async () => ({ dataProfile: "fixture-profile", dirty: false }),
    nativeInstaller: { capability: () => ({ supported: true }), prepare: async () => ({ install: () => { throw new Error("must not install"); } }), dispose() {} },
    invokeHelper: async command => {
      if (command === "config") return { status: "enabled", config: { version: extra.currentVersion || "1.0.0" } };
      if (command === "discover") return extra.currentVersion === release.version ? { status: "up_to_date", reason: "no_newer_compatible_release" } : { status: "direct", next: release, route: [release.releaseId] };
      if (command === "download") return { status: "downloaded", releaseId: release.releaseId, version: release.version, componentsPath: path.join(dir, "components.json"), packagePath: path.join(dir, "fixture.zip") };
      throw new Error(command);
    }, ...extra,
  });
  const updater = make(); t.after(() => updater.dispose()); await updater.check(); await updater.install(release.releaseId); await new Promise(resolve => setImmediate(resolve));
  assert.equal(updater.getState().phase, "countdown"); assert.equal(admitted, false);
  await assert.rejects(updater.later(), { code: "update_resume_failed" }); assert.equal(updater.getState().phase, "repair");
  await assert.rejects(updater.later());
  assert.equal(updater.getState().phase, "repair"); assert.equal(updater.getState().reason, "update_resume_failed"); assert.equal(admitted, false);
  await assert.rejects(updater.cancel()); await assert.rejects(updater.install(release.releaseId));
  assert.equal(intent.read().phase, "repair"); assert.equal(commands.filter(x => x === "resume").length, 1);
  updater.dispose(); const reopened = make(); t.after(() => reopened.dispose()); const before = commands.length; await reopened.start();
  assert.equal(reopened.getState().phase, "repair"); assert.equal(commands.length, before, "restart must not repeat an unknown operation");
  const repaired = make({ currentVersion: "1.1.0", runtime: { ...runtime, verifyStartup: async () => ({ configuration: { version: "1.1.0", releaseId: release.releaseId }, local: { dataProfile: "fixture-profile", dirty: false } }) } }); t.after(() => repaired.dispose());
  await repaired.start(); assert.equal(repaired.getState().phase, "installed", "manual repair must be confirmable after admission-only failure"); assert.equal(intent.read(), null);
});
