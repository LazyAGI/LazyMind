import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { createRequire } from "node:module";
import test from "node:test";
import vm from "node:vm";
import { EventEmitter } from "node:events";

const require = createRequire(import.meta.url);
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client.js");
const { createUpdateInstallFlow } = require("../electron/src/update-install-flow.js");
const { createInstallIntentStore } = require("../electron/src/update-install-intent.js");
const settle = () => new Promise(resolve => setImmediate(resolve));

function runtimeFixture(stopGuard, stopResult = { status: "stopped" }) {
  const calls = [];
  const runtime = createUpdateRuntimeClient({ localWork: () => 0, setAdmission() {}, verifyStartup() {}, stopGuard,
    stopAuxiliary: async () => { calls.push("auxiliary"); },
    runSidecar: async (command, args) => {
      if (command === "update-components-check") return JSON.stringify({ status: "compatible", dataProfile: "fixture" });
      const action = args[1], epoch = Number(args[3] || 0); calls.push(action);
      return JSON.stringify(action === "stop" ? stopResult : { active: 0, paused: action === "pause", epoch: action === "pause" ? epoch : 0 });
    },
  });
  return { runtime, calls };
}

test("installation waits for guard cleanup after confirmed service shutdown", async t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-guard-flow-"));
  let finishGuard;
  const guardDone = new Promise(resolve => { finishGuard = resolve; });
  const f = runtimeFixture(async () => { assert.equal(f.calls.at(-1), "stop"); f.calls.push("guard"); await guardDone; });
  const timers = [], release = { releaseId: "target", version: "1.1.0", package: { size: 4 }, components: { size: 2, sha256: "b".repeat(64) } };
  let state = {};
  const flow = createUpdateInstallFlow({ currentVersion: "1.0.0", runtime: f.runtime,
    store: createInstallIntentStore(path.join(directory, "intent.json")),
    nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, prepare: async () => ({ install: async () => { f.calls.push("install"); return { handoff: true }; } }) },
    context: () => ({ configuration: {}, offer: { next: release }, receipt: { componentsPath: path.join(directory, "components.json"), packagePath: path.join(directory, "package.zip"), dataProfile: "fixture" } }),
    download: async () => {}, cancelDownload() {}, clearDownloadIntent() {}, revealPackage() {},
    rediscover: async () => ({ decision: { status: "direct", next: release }, local: { dataProfile: "fixture", dirty: false } }),
    publish: patch => { state = { ...state, ...patch }; }, getState: () => state,
    clock: { setTimeout(fn) { timers.push(fn); return fn; }, clearTimeout() {} },
  });
  t.after(async () => { finishGuard(); await settle(); flow.dispose(); fs.rmSync(directory, { recursive: true, force: true }); });
  await flow.begin(release.releaseId); await settle();
  for (let index = 0; index < 10; index++) { assert.equal(state.phase, "countdown"); timers.shift()(); await settle(); }
  assert.equal(f.calls.at(-1), "guard");
  assert.ok(!f.calls.includes("install"), "helper must not accept before guard has exited");
  finishGuard(); await settle();
  assert.equal(f.calls.at(-1), "install");
});

test("failed service shutdown never retires the guard", async () => {
  const f = runtimeFixture(async () => assert.fail("guard stopped before services"), { status: "unknown" });
  await f.runtime.pause(7);
  await assert.rejects(f.runtime.stop(7), { code: "runtime_stop_failed" });
});

test("failed guard cleanup never confirms the stop to the installer", async () => {
  const f = runtimeFixture(async () => { throw Object.assign(new Error("guard still alive"), { code: "runtime_stop_failed" }); });
  await f.runtime.pause(7);
  await assert.rejects(f.runtime.stop(7), { code: "runtime_stop_failed" });
});

test("the default runtime client preserves the existing stop behavior", async () => {
  const f = runtimeFixture(undefined);
  await f.runtime.pause(7); assert.deepEqual(await f.runtime.stop(7), { status: "stopped" });
  assert.deepEqual(f.calls, ["pause", "auxiliary", "stop"]);
});

test("the actual main entry stops only the unsigned guard and prevents restart while stopping", async () => {
  const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const runtimeSource = source.slice(source.indexOf("const updateRuntime = createUpdateRuntimeClient("), source.indexOf("const desktopUpdater = createDesktopUpdater("));
  const guardSource = source.slice(source.indexOf("function startGuard()"), source.indexOf("function quoteWindowsArgument("));
  assert.ok(runtimeSource && guardSource);
  for (const unsigned of [false, true]) {
    const guard = {}, calls = [];
    const context = { module: { exports: {} }, createUpdateRuntimeClient, unsignedTestProfile: unsigned ? {} : null,
      desktopUpdateWork: 0, runtimeRestartPromise: null, inputRecorder: { isActive: () => false }, nativeRecording: null,
      browserRecorders: new Set(), desktopUpdateAdmission: true, runtimeStopping: false,
      agentHostRestartTimer: undefined, agentHostStableTimer: undefined, clearTimeout,
      agentHostProcess: null, runtimeProcess: null, guardProcess: guard, guardPID: 0,
      managedBrowser: { clear: async () => calls.push("browser") },
      stopDesktopUpdateChild: async child => { if (child === guard) { assert.equal(calls.at(-1), "stop"); calls.push("guard"); } },
      runSidecar: async (_command, args) => { calls.push(args[1]); return JSON.stringify(args[1] === "stop" ? { status: "stopped" } : { active: 0, paused: true, epoch: Number(args[3]) }); },
      fs: { existsSync: () => true }, sidecarPath: "/fixture/sidecar",
      ensureDesktopLogDirs: () => assert.fail("guard restarted during unsigned update"),
    };
    vm.runInNewContext(`${guardSource}\n${runtimeSource}\nmodule.exports={runtime:updateRuntime,startGuard};`, context);
    await context.module.exports.runtime.pause(7);
    await context.module.exports.runtime.stop(7);
    assert.equal(calls.includes("guard"), unsigned);
    if (unsigned) { context.guardProcess = null; context.module.exports.startGuard(); }
  }
});

for (const closes of [true, false]) test(`guard child termination ${closes ? "waits for close" : "fails on timeout"}`, async () => {
  const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const stopSource = source.slice(source.indexOf("function stopDesktopUpdateChild("), source.indexOf("let windowHiddenByUser"));
  const child = Object.assign(new EventEmitter(), { exitCode: null, signalCode: null });
  let killed = 0, deadline, outcome;
  child.kill = () => { killed++; return true; };
  const context = { module: { exports: {} }, setTimeout: fn => { deadline = fn; return fn; }, clearTimeout: () => { deadline = undefined; } };
  vm.runInNewContext(`${stopSource}\nmodule.exports=stopDesktopUpdateChild;`, context);
  const waiting = context.module.exports(child).then(() => { outcome = "closed"; }, error => { outcome = error.code; });
  assert.equal(killed, 1); await settle(); assert.equal(outcome, undefined);
  child.emit("exit"); await settle(); assert.equal(outcome, undefined, "must wait for full child close");
  if (closes) child.emit("close"); else deadline();
  await waiting; assert.equal(outcome, closes ? "closed" : "runtime_stop_failed");
});
