import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import path from "node:path";
import vm from "node:vm";
import test from "node:test";

const require = createRequire(import.meta.url);
const startup = require("../electron/src/update-startup.js");
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const tx = "a".repeat(32), owner = "fixture-target-owner", revision = "b".repeat(64);
const copy = value => JSON.parse(JSON.stringify(value));

function fixture(options = {}) {
  const calls = [];
  const record = {
    status: "maintenance_required", phase: "installing", transactionId: tx, owner,
    targetReleaseId: "target", targetVersion: "1.1.0", dataProfile: "format-v2-fixture",
    sourceHadRAG: options.absent !== true, componentMode: options.absent ? "absent" : "sync-target",
    targetRAGRevision: options.absent ? "" : revision, componentsPrepared: true,
  };
  const peer = { transactionId: tx, owner, ready: true, sealed: true };
  const verified = {
    status: "ready", transactionId: tx, owner, releaseId: "target", version: "1.1.0",
    dataProfile: "format-v2-fixture", dirty: false,
    components: options.absent ? {} : { rag: revision },
    peers: { core: copy(peer), scan: copy(peer) },
  };
  const invoke = async (command, args = [], limits = {}) => {
    const action = command === "update-bootstrap" ? args[args.indexOf("--action") + 1] : command;
    calls.push({ command, action, args: [...args], limits });
    if (options.intercept) {
      const result = await options.intercept(action, { record, verified, calls });
      if (result !== undefined) return typeof result === "string" ? result : JSON.stringify(result);
    }
    switch (action) {
      case "inspect": return JSON.stringify(record);
      case "repair-adopt-current":
        record.status = "maintenance_required";
        record.targetReleaseId = "repair";
        record.targetVersion = "1.2.0";
        verified.releaseId = "repair";
        verified.version = "1.2.0";
        return JSON.stringify({ status: "adopted", transactionId: tx, owner,
          originalSourceReleaseId: "source", originalTargetReleaseId: "target",
          targetReleaseId: "repair", targetVersion: "1.2.0" });
      case "prepare-components": return JSON.stringify({ status: "prepared", transactionId: tx, owner });
      case "activate": return JSON.stringify({ status: "activated", transactionId: tx, owner });
      case "up": return JSON.stringify({ status: "ready", transactionId: tx, owner, bootstrap: true });
      case "verify": return JSON.stringify(verified);
      case "complete": return JSON.stringify({ status: "completed", transactionId: tx, owner,
        peersReleased: { core: true, scan: true } });
      case "down": return JSON.stringify({ status: "stopped", transactionId: tx, owner });
      default: throw new Error("unexpected startup command " + command + "/" + action);
    }
  };
  const parameters = {
    isPackaged: true, currentReleaseId: options.repair ? "repair" : "target",
    currentVersion: options.repair ? "1.2.0" : "1.1.0", ownerToken: owner, invoke,
  };
  return { calls, record, verified, parameters };
}

function prepare(f) {
  assert.equal(typeof startup.prepareDesktopStartup, "function",
    "v2 must finish target activation before normal startup is allowed");
  return startup.prepareDesktopStartup(f.parameters);
}

test("target startup waits for runtime guard readiness before verifying", async t => {
  const f = fixture();
  let acknowledge;
  const guardReady = new Promise(resolve => { acknowledge = resolve; });
  t.after(acknowledge);
  f.parameters.onRuntimeStarted = () => guardReady;

  const attempt = prepare(f);
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(f.calls.map(call => call.action), ["inspect", "activate", "up"]);
  acknowledge();
  const result = await attempt;
  assert.equal(result.allowed, true);
  assert.equal(result.runtimeStarted, true);
  assert.deepEqual(f.calls.map(call => call.action), ["inspect", "activate", "up", "verify", "complete"]);
  assert.ok(f.calls[2].args.includes(tx), "normal runtime must receive the startup transaction before its first loop");
});

test("target startup stops the sealed runtime if the guard cannot start", async () => {
  const f = fixture();
  const error = new Error("Desktop runtime guard exited before it was ready");
  const guardReady = Promise.reject(error);
  void guardReady.catch(() => {});
  f.parameters.onRuntimeStarted = () => guardReady;

  await assert.rejects(prepare(f), error);
  assert.deepEqual(f.calls.map(call => call.action), ["inspect", "activate", "up", "down"]);
});

test("activation failure starts no service and does not complete the intent", async () => {
  const f = fixture({ intercept: action => action === "activate"
    ? { status: "blocked", reason: "component_update_incompatible" } : undefined });
  await assert.rejects(prepare(f));
  assert.deepEqual(f.calls.map(call => call.action), ["inspect", "activate"]);
});

test("malformed startup state cannot fall back to ordinary startup", async () => {
  const f = fixture({ intercept: action => action === "inspect" ? "{truncated" : undefined });
  await assert.rejects(prepare(f));
  assert.deepEqual(f.calls.map(call => call.action), ["inspect"]);
});

test("another installed release cannot consume the original target intent", async () => {
  const f = fixture();
  f.record.targetReleaseId = "another-target";
  await assert.rejects(prepare(f));
  assert.deepEqual(f.calls.map(call => call.action), ["inspect"]);
});

for (const scenario of ["scan-not-ready", "scan-wrong-owner", "core-wrong-transaction", "wrong-rag", "dirty"]) {
  test("startup rejects incomplete proof: " + scenario, async () => {
    const f = fixture();
    switch (scenario) {
      case "scan-not-ready": f.verified.peers.scan.ready = false; break;
      case "scan-wrong-owner": f.verified.peers.scan.owner = "different-owner"; break;
      case "core-wrong-transaction": f.verified.peers.core.transactionId = "c".repeat(32); break;
      case "wrong-rag": f.verified.components.rag = "d".repeat(64); break;
      case "dirty": f.verified.dirty = true; break;
    }
    await assert.rejects(prepare(f));
    assert.ok(!f.calls.some(call => call.action === "complete"));
    assert.ok(f.calls.some(call => call.action === "down"), "failed verification must stop its sealed runtime");
  });
}

test("absent source cannot become enabled RAG during target verification", async () => {
  const f = fixture({ absent: true });
  f.verified.components.rag = revision;
  await assert.rejects(prepare(f));
  assert.ok(!f.calls.some(call => call.action === "complete"));
});

test("startup compares the exact component plan supplied by the runtime", async t => {
  const otherRevision = "e".repeat(64);
  for (const [id, absent] of [["cluster", false], ["cluster", true], ["future-component", false]]) {
    for (const missing of [false, true]) {
      await t.test(`${id}, rag=${!absent}, missing=${missing}`, async () => {
        const f = fixture({ absent });
        f.record.targetComponents = { ...(absent ? {} : { rag: { id: "rag", revision } }), [id]: { id, revision: otherRevision } };
        if (!missing) f.verified.components[id] = otherRevision;
        if (missing) {
          await assert.rejects(prepare(f), { code: "update_startup_failed" });
          assert.equal(f.calls.at(-1).action, "down");
        } else {
          assert.equal((await prepare(f)).allowed, true);
          assert.equal(f.calls.at(-1).action, "complete");
        }
      });
    }
  }
});

test("cold target import can exceed fifteen seconds without starting business", async t => {
  let release;
  const delayed = new Promise(resolve => { release = resolve; });
  const f = fixture({ intercept: async action => {
    if (action === "activate") {
      await delayed;
      return { status: "activated", transactionId: tx, owner };
    }
  } });
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let settled = false;
  const attempt = prepare(f).finally(() => { settled = true; });
  t.after(release);
  await new Promise(resolve => setImmediate(resolve));
  t.mock.timers.tick(16_000);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(settled, false);
  assert.ok(!f.calls.some(call => call.action === "up"));
  assert.equal(f.calls.find(call => call.action === "activate").limits.timeout, 360_000);
  release();
  assert.equal((await attempt).allowed, true);
});

test("Windows simple installation allows target Python preparation before services start", async () => {
  const f = fixture();
  f.parameters.platform = "win32";
  await prepare(f);
  assert.deepEqual(f.calls.map(call => call.action), ["inspect", "activate", "up", "verify", "complete"]);
  assert.equal(f.calls[1].limits.timeout, 30 * 60_000);
});

test("ordinary allowed startup does not invoke activation or service startup", async () => {
  const f = fixture({ intercept: action => action === "inspect" ? { status: "allowed" } : undefined });
  const result = await prepare(f);
  assert.equal(result.allowed, true);
  assert.equal(result.runtimeStarted, false);
  assert.deepEqual(f.calls.map(call => call.action), ["inspect"]);
});

test("repair is not adopted automatically on ordinary startup", async () => {
  const f = fixture({ repair: true });
  f.record.status = "repair";
  const result = await prepare(f);
  assert.equal(result.allowed, false);
  assert.equal(result.repair, true);
  assert.deepEqual(f.calls.map(call => call.action), ["inspect"]);
});

test("explicit forward repair preserves the transaction and never invokes native install", async () => {
  const f = fixture({ repair: true });
  f.record.status = "repair";
  assert.equal(typeof startup.adoptDesktopRepair, "function");
  const result = await startup.adoptDesktopRepair(f.parameters);
  assert.equal(result.allowed, true);
  assert.deepEqual(f.calls.map(call => call.action),
    ["inspect", "repair-adopt-current", "prepare-components", "activate", "up", "verify", "complete"]);
  for (const call of f.calls.slice(1)) {
    assert.ok(call.args.includes(tx), "repair must continue the original transaction");
  }
});

test("unaccepted forward repair cannot clear or replace the old intent", async () => {
  const f = fixture({ repair: true, intercept: action => action === "repair-adopt-current"
    ? { status: "blocked", reason: "repair_target_not_accepted" } : undefined });
  f.record.status = "repair";
  assert.equal(typeof startup.adoptDesktopRepair, "function");
  await assert.rejects(startup.adoptDesktopRepair(f.parameters));
  assert.ok(!f.calls.some(call => ["activate", "up", "complete"].includes(call.action)));
});

test("lost completion ACK is an unknown outcome, never a second installation", async () => {
  const f = fixture({ intercept: action => {
    if (action === "complete") throw Object.assign(new Error("fixture timeout"), { code: "network_timeout" });
  } });
  await assert.rejects(prepare(f));
  assert.equal(f.calls.filter(call => call.action === "activate").length, 1);
  assert.ok(!f.calls.some(call => call.action === "repair-adopt-current"));
});

for (const mode of ["ordinary", "background", "installer-warmup"]) {
  test("actual Electron ready entry awaits maintenance before " + mode, async t => {
    const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
    const start = source.indexOf("app.whenReady().then");
    const end = source.indexOf('  app.on("window-all-closed"', start);
    assert.ok(start >= 0 && end > start);
    const calls = [];
    let ready, release;
    const blocked = new Promise(resolve => { release = resolve; });
    t.after(release);
    // This test owns production wiring, while preceding tests exercise the real
    // startup helper. Only that helper and unrelated Electron/OS work are faked.
    const context = {
      app: { whenReady: () => ({ then(fn) { ready = fn; } }), exit: code => calls.push("exit:" + code), setAppUserModelId() {} },
      session: { defaultSession: {} }, desktopCapturer: {}, Menu: {}, screen: {},
      installScreenCapture() {}, startupMetricsRecorder: { mark() {} },
      clearFrontendCaches: async () => {}, appendStartupLog() {}, serializeError: String,
      isWindows: mode === "installer-warmup", isInstallerWarmup: mode === "installer-warmup",
      cloudConfiguration: {}, powerMonitor: { on() {} }, clearTemporaryCredentials() {},
      isPackaged: true, ownerToken: owner, runtimeResourcesRoot: "/fixture/target",
      currentReleaseId: "target", currentVersion: "1.1.0", sidecarPath: "/fixture/manager",
      sidecarArgs: command => [command], sidecarEnv: () => ({}),
      runSidecar: async () => { calls.push("sidecar"); return {}; },
      async prepareDesktopStartup() { calls.push("maintenance"); await blocked; return { allowed: true, runtimeStarted: true }; },
      runInstallerWarmup: async () => { calls.push("installer-warmup"); },
      runMacInstallationWarmupIfNeeded: async () => { calls.push("mac-warmup"); },
      showActiveWindow: async () => { calls.push("window"); },
      windowHiddenByUser: mode === "background", frontendOpeningAllowed: false,
      desktopUpdater: { start() {} }, finishStartupMetrics() {},
      setTimeout: () => ({ unref() {} }), dialog: { showErrorBox() {} }, console,
    };
    vm.runInNewContext(source.slice(start, end), context);
    const attempt = ready();
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(calls, ["maintenance"], "normal/warmup entry bypassed the pre-startup decision");
    release();
    await attempt;
    assert.equal(calls[0], "maintenance");
    if (mode === "installer-warmup") assert.ok(calls.includes("installer-warmup"));
    if (mode === "ordinary") assert.ok(calls.includes("window"));
    if (mode === "background") assert.ok(!calls.includes("window"));
  });
}

test("an explicitly completed newer repair uses the verified root for its guard and clears stale renderer repair state", async t => {
  const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const runtimeRoot = path.resolve("fixture-custom-runtime");
  let durable, ready, startDiscovery, started, guardArgs, prompted = 0;
  const f = fixture({ repair: true, intercept(action) {
    if (action === "complete") durable = null;
  } });
  Object.assign(f.record, { status: "repair", phase: "installing", version: 2, sourceVersion: "1.0.0",
    releaseId: "target", nextVersion: "1.1.0", runtimeRoot });
  durable = copy(f.record);
  const updater = createDesktopUpdater({
    currentVersion: "1.2.0", readLocalState: async () => ({ dataProfile: f.record.dataProfile, dirty: false }),
    invokeHelper: async command => command === "config" ? { status: "enabled", config: { updaterProtocol: 2 } }
      : { status: "up_to_date" },
    // Electron constructs the updater before app.whenReady. This store models
    // the independent CLI receipt that complete removes after peer release.
    installIntent: { read: () => durable && copy(durable), clear() { assert.equal(durable, null); } },
    runtime: { verifyStartup: async () => ({ configuration: { releaseId: "repair", version: "1.2.0" },
      local: { dataProfile: f.record.dataProfile, dirty: false } }) },
  });
  t.after(() => updater.dispose());
  assert.equal(updater.getState().phase, "repair");
  const context = {
    app: { whenReady: () => ({ then(fn) { ready = fn; } }), exit: code => assert.fail("unexpected app exit " + code) },
    session: { defaultSession: {} }, desktopCapturer: {}, Menu: {}, screen: {},
    installScreenCapture() {}, startupMetricsRecorder: { mark() {} }, clearFrontendCaches: async () => {},
    appendStartupLog() {}, serializeError: String, isWindows: false, isInstallerWarmup: false, cloudConfiguration: {},
    powerMonitor: { on() {} }, clearTemporaryCredentials() {}, isPackaged: true, ownerToken: owner,
    repoRoot: "/fixture/app", runtimeResourcesRoot: "/fixture/target", explicitRuntimeRoot: "", path,
    currentReleaseId: "repair", currentVersion: "1.2.0", runSidecar: f.parameters.invoke,
    prepareDesktopStartup: startup.prepareDesktopStartup, adoptDesktopRepair: startup.adoptDesktopRepair,
    startGuard() { guardArgs = context.sidecarArgs("guard"); },
    runMacInstallationWarmupIfNeeded: async () => {}, windowHiddenByUser: true, frontendOpeningAllowed: false,
    desktopUpdater: { ...updater, start(...args) { started = updater.start(...args); return started; } },
    setTimeout(fn) { startDiscovery = fn; return { unref() {} }; },
    dialog: { async showMessageBox() { prompted++; return { response: 0 }; }, showErrorBox: assert.fail },
  };
  vm.runInNewContext(source.slice(source.indexOf("function sidecarArgs("), source.indexOf("function sidecarEnv(")), context);
  vm.runInNewContext(source.slice(source.indexOf("app.whenReady().then"), source.indexOf('  app.on("window-all-closed"')), context);
  await ready();
  assert.equal(prompted, 1);
  assert.equal(f.calls.at(-1).action, "complete");
  assert.equal(durable, null);
  startDiscovery();
  await started;
  await t.test("guard receives the recorded root on its first invocation", () => {
    assert.ok(guardArgs.includes("--runtime-root"));
    assert.equal(guardArgs[guardArgs.indexOf("--runtime-root") + 1], runtimeRoot);
  });
  await t.test("completed repair does not reappear when renderer discovery starts", () => {
    const state = updater.getState();
    assert.ok(["installed", "up_to_date"].includes(state.phase), JSON.stringify(state));
    assert.equal(state.currentVersion, "1.2.0");
    assert.ok(!state.pendingUpdate);
  });
});
