import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopUpdater, registerUpdateIPC } = require("../electron/src/update-controller.js");
const { isTrustedNotificationSender } = require("../electron/src/native-notifications.js");
const { createDesktopBridge } = require("../electron/src/preload.js");
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
const available = () => ({ status: "bridge", reason: "compatible", route: ["r2", "r3"], targetVersion: "2.0.0", policySequence: 42, policyDigest: "a".repeat(64), next: { releaseId: "r2", version: "1.1.0", package: { size: 100, url: "https://fixture.invalid/package", sha256: "b".repeat(64) } } });

function fixture(overrides = {}) {
  const calls = [];
  const invokeHelper = async (command, request, options) => {
    calls.push({ command, request, options });
    if (overrides.helper) return overrides.helper(command, request, options);
    if (command === "config") return { status: "enabled", config: { channel: "stable" } };
    if (command === "discover") return available();
    if (command === "download") return { status: "downloaded", releaseId: "r2", version: "1.1.0", packagePath: "/fixture/cache/package.zip", policySequence: 42, policyDigest: "a".repeat(64) };
    throw new Error(`unexpected helper command ${command}`);
  };
  const updater = createDesktopUpdater({ currentVersion: "1.0.0", invokeHelper,
    readLocalState: async () => ({ systemVersion: "14.0.0", dataProfile: "known-v1", components: {} }),
    revealPackage: overrides.revealPackage || (() => {}),
  });
  return { updater, calls };
}

test("simultaneous checks share one operation and expose the helper's accepted decision", async () => {
  const gate = deferred();
  const f = fixture({ helper: async (command) => command === "config" ? { status: "enabled", config: { channel: "stable" } } : gate.promise });
  const first = f.updater.check();
  const second = f.updater.check();
  gate.resolve(available());
  await Promise.all([first, second]);
  assert.equal(f.calls.filter((call) => call.command === "discover").length, 1);
  const state = f.updater.getState();
  assert.equal(state.phase, "available");
  assert.equal(state.currentVersion, "1.0.0");
  assert.equal(state.releaseId, "r2");
  assert.deepEqual(state.route, ["r2", "r3"]);
  state.route.push("injected");
  assert.deepEqual(f.updater.getState().route, ["r2", "r3"]);
  assert.equal(state.packagePath, undefined);
  assert.equal(state.policyURL, undefined);
});

test("unconfigured and failed checks are distinguishable from up to date", async () => {
  const disabled = fixture({ helper: async () => ({ status: "disabled", reason: "not_configured" }) });
  await disabled.updater.check();
  assert.equal(disabled.updater.getState().phase, "disabled");
  assert.equal(disabled.calls.length, 1);
  const failed = fixture({ helper: async (command) => {
    if (command === "config") return { status: "enabled", config: { channel: "stable" } };
    throw Object.assign(new Error("do not expose private path or URL"), { code: "policy_signature" });
  } });
  await failed.updater.check();
  assert.equal(failed.updater.getState().phase, "error");
  assert.equal(failed.updater.getState().reason, "policy_signature");
  assert.doesNotMatch(JSON.stringify(failed.updater.getState()), /private path/);
});

test("double download click does not start another helper or execute an installer", async () => {
  const gate = deferred();
  let reveal;
  const f = fixture({ revealPackage: (path) => { reveal = path; }, helper: async (command, request, options) => {
    if (command === "config") return { status: "enabled", config: { channel: "stable" } };
    if (command === "discover") return available();
    options.onProgress({ status: "downloading", downloadedBytes: 20, totalBytes: 100 });
    return gate.promise;
  } });
  await f.updater.check();
  const first = f.updater.download("r2");
  const second = f.updater.download("r2");
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(f.updater.getState().phase, "downloading");
  assert.equal(f.updater.getState().downloadedBytes, 20);
  assert.equal(f.calls.filter((call) => call.command === "download").length, 1);
  await f.updater.check();
  assert.equal(f.calls.filter((call) => call.command === "discover").length, 1, "checking cannot replace an active download");
  gate.resolve({ status: "downloaded", releaseId: "r2", version: "1.1.0", packagePath: "/fixture/cache/package.zip" });
  await Promise.all([first, second]);
  assert.equal(f.updater.getState().phase, "downloaded");
  assert.equal(f.updater.getState().canInstall, false);
  await f.updater.revealDownloaded();
  assert.equal(reveal, "/fixture/cache/package.zip");
  assert.ok(f.calls.every((call) => ["config", "discover", "download"].includes(call.command)));
});

test("stale or forged renderer targets cannot select a URL or another release", async () => {
  const f = fixture(); await f.updater.check();
  await assert.rejects(() => f.updater.download("r3"));
  await assert.rejects(() => f.updater.download({ releaseId: "r2", url: "https://attacker.invalid" }));
  assert.equal(f.calls.filter((call) => call.command === "download").length, 0);
});

test("cancel aborts the helper and ignores late success and progress", async () => {
  const gate = deferred(); let options;
  const f = fixture({ helper: async (command, _request, nextOptions) => {
    if (command === "config") return { status: "enabled", config: { channel: "stable" } };
    if (command === "discover") return available();
    options = nextOptions; return gate.promise;
  } });
  await f.updater.check(); const download = f.updater.download("r2");
  await new Promise((resolve) => setImmediate(resolve));
  f.updater.cancel();
  assert.equal(options.signal.aborted, true);
  options.onProgress({ status: "downloading", downloadedBytes: 100, totalBytes: 100 });
  gate.resolve({ status: "downloaded", releaseId: "r2", packagePath: "/late.zip" });
  await download;
  assert.equal(f.updater.getState().phase, "available");
  await assert.rejects(() => f.updater.revealDownloaded());
});

test("closing the renderer's subscription leaves background downloads running", async () => {
  const f = fixture(); let updates = 0;
  const unsubscribe = f.updater.subscribe(() => { updates += 1; });
  await f.updater.check(); unsubscribe(); const before = updates;
  await f.updater.download("r2");
  assert.equal(f.updater.getState().phase, "downloaded");
  assert.equal(updates, before);
});

test("an immediate retry waits for old helper cleanup and ignores its late events", async () => {
  const old = deferred(), current = deferred(); let attempt = 0, oldOptions;
  const f = fixture({ helper: async (command, _request, options) => {
    if (command === "config") return { status: "enabled", config: { channel: "stable" } };
    if (command === "discover") return available();
    attempt += 1; if (attempt === 1) oldOptions = options;
    return attempt === 1 ? old.promise : current.promise;
  } });
  await f.updater.check(); const first = f.updater.download("r2");
  await new Promise((resolve) => setImmediate(resolve)); f.updater.cancel();
  const second = f.updater.download("r2"); await new Promise((resolve) => setImmediate(resolve));
  assert.equal(attempt, 1, "retry must not overlap a helper that is still cleaning up");
  old.resolve({ status: "downloaded", releaseId: "r2", packagePath: "/old.zip" }); await first;
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(attempt, 2);
  oldOptions.onProgress({ status: "downloading", downloadedBytes: 999, totalBytes: 999 });
  assert.equal(f.updater.getState().phase, "downloading");
  assert.notEqual(f.updater.getState().downloadedBytes, 999);
  current.resolve({ status: "downloaded", releaseId: "r2", packagePath: "/current.zip" }); await second;
  assert.equal(f.updater.getState().phase, "downloaded");
});

test("every published state has a strictly increasing revision", async () => {
  const f = fixture(), states = [];
  const unsubscribe = f.updater.subscribe((state) => states.push(state));
  await f.updater.check(); await f.updater.download("r2"); unsubscribe();
  assert.ok(states.length >= 3);
  for (let i = 0; i < states.length; i += 1) {
    assert.ok(Number.isSafeInteger(states[i].revision));
    if (i) assert.ok(states[i].revision > states[i - 1].revision);
  }
});

test("background checking starts once, schedules a bounded interval, and stops on disposal", async () => {
  const timers = []; const calls = [];
  const updater = createDesktopUpdater({ currentVersion: "1.0.0", readLocalState: async () => ({}),
    invokeHelper: async (command) => { calls.push(command); return command === "config" ? { status: "enabled", config: { channel: "stable" } } : { status: "up_to_date", reason: "no_newer_compatible_release" }; },
    clock: { setTimeout: (callback, delay) => { const timer = { callback, delay, unref() {} }; timers.push(timer); return timer; }, clearTimeout: (timer) => { timer.cancelled = true; } }, random: () => 0.5,
  });
  await Promise.all([updater.start(), updater.start()]);
  assert.equal(calls.filter((command) => command === "discover").length, 1);
  assert.equal(timers.length, 1); assert.ok(timers[0].delay >= 5.5 * 3600_000 && timers[0].delay <= 6.5 * 3600_000);
  updater.dispose(); assert.equal(timers[0].cancelled, true);
  await timers[0].callback(); assert.equal(calls.filter((command) => command === "discover").length, 1);
});

test("update IPC verifies the actual main frame and origin before every privileged action", async () => {
  const handlers = new Map(); const f = fixture();
  const frame = { url: "http://127.0.0.1:3131/settings" };
  const contents = { mainFrame: frame, isDestroyed: () => false };
  const window = { webContents: contents, isDestroyed: () => false };
  registerUpdateIPC({ ipcMain: { handle: (name, handler) => handlers.set(name, handler) }, updater: f.updater,
    authorize: (event) => isTrustedNotificationSender(event, window, "http://127.0.0.1:3131") });
  for (const name of ["status", "check", "download", "cancel", "reveal"]) {
    const handler = handlers.get(`lazymind:update:${name}`);
    assert.equal(typeof handler, "function");
    for (const event of [{ sender: {}, senderFrame: frame }, { sender: contents, senderFrame: {} }]) {
      await assert.rejects(async () => handler(event, "r2"));
    }
    frame.url = "https://attacker.invalid";
    await assert.rejects(async () => handler({ sender: contents, senderFrame: frame }, "r2"));
    frame.url = "http://127.0.0.1:3131/settings";
  }
  assert.equal(f.calls.length, 0);
  await handlers.get("lazymind:update:check")({ sender: contents, senderFrame: frame });
  assert.equal(f.updater.getState().phase, "available");
});

test("preload exposes only fixed update commands and cleans event listeners", async () => {
  const calls = []; const listeners = new Map();
  const bridge = createDesktopBridge({ invoke: async (...args) => { calls.push(args); },
    on: (name, listener) => listeners.set(name, listener), removeListener: (name, listener) => { assert.equal(listeners.get(name), listener); listeners.delete(name); } });
  await bridge.updateStatus(); await bridge.updateCheck(); await bridge.updateDownload("r2"); await bridge.updateCancel(); await bridge.updateReveal();
  assert.deepEqual(calls, [["lazymind:update:status"], ["lazymind:update:check"], ["lazymind:update:download", "r2"], ["lazymind:update:cancel"], ["lazymind:update:reveal"]]);
  let received; const unsubscribe = bridge.onUpdateState((state) => { received = state; });
  listeners.get("lazymind:update:state")({ sender: "must not escape" }, { phase: "available" });
  assert.deepEqual(received, { phase: "available" }); unsubscribe(); assert.equal(listeners.size, 0);
});
