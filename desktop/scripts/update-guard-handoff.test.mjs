import assert from "node:assert/strict";
import fs from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import vm from "node:vm";
import { EventEmitter } from "node:events";

const require = createRequire(import.meta.url);
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client.js");
const settle = () => new Promise(resolve => setImmediate(resolve));

function runtimeFixture(stopResult = { status: "stopped" }) {
  const calls = [];
  const runtime = createUpdateRuntimeClient({ localWork: () => 0, setAdmission() {}, verifyStartup() {},
    stopAuxiliary: async () => { calls.push("auxiliary"); },
    runSidecar: async (command, args) => {
      if (command === "update-components-check") return JSON.stringify({ status: "compatible", dataProfile: "fixture" });
      const action = args[1], epoch = Number(args[3] || 0); calls.push(action);
      return JSON.stringify(action === "stop" ? stopResult : { active: 0, paused: action === "pause", epoch: action === "pause" ? epoch : 0 });
    },
  });
  return { runtime, calls };
}

test("failed service shutdown does not confirm a successful stop", async () => {
  const f = runtimeFixture({ status: "unknown" });
  await f.runtime.pause(7);
  await assert.rejects(f.runtime.stop(7), { code: "runtime_stop_failed" });
});

test("the default runtime client preserves the existing stop behavior", async () => {
  const f = runtimeFixture();
  await f.runtime.pause(7); assert.deepEqual(await f.runtime.stop(7), { status: "stopped" });
  assert.deepEqual(f.calls, ["pause", "auxiliary", "stop"]);
});

test("the actual main entry preserves the resident guard during update shutdown", async () => {
  const source = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const runtimeSource = source.slice(source.indexOf("const updateRuntime = createUpdateRuntimeClient("), source.indexOf("const desktopUpdater = createDesktopUpdater("));
  assert.ok(runtimeSource);
  const guard = {}, calls = [];
  const context = { module: { exports: {} }, createUpdateRuntimeClient,
    desktopUpdateWork: 0, runtimeRestartPromise: null, inputRecorder: { isActive: () => false }, nativeRecording: null,
    browserRecorders: new Set(), desktopUpdateAdmission: true, runtimeStopping: false,
    agentHostRestartTimer: undefined, agentHostStableTimer: undefined, clearTimeout,
    agentHostProcess: null, runtimeProcess: null, guardProcess: guard,
    agentRecovery: { stop: async () => {} },
    managedBrowser: { clear: async () => calls.push("browser") },
    stopDesktopUpdateChild: async child => { assert.notEqual(child, guard); },
    runSidecar: async (_command, args) => { calls.push(args[1]); return JSON.stringify(args[1] === "stop" ? { status: "stopped" } : { active: 0, paused: true, epoch: Number(args[3]) }); },
  };
  vm.runInNewContext(`${runtimeSource}\nmodule.exports=updateRuntime;`, context);
  await context.module.exports.pause(7);
  await context.module.exports.stop(7);
  assert.deepEqual(calls, ["pause", "browser", "stop"]);
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
