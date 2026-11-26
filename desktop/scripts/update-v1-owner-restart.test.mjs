import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { appendFileSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller");
const { createInstallIntentStore } = require("../electron/src/update-install-intent");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client");
const sourceVersion = "1.0.0";
const target = { releaseId: "target", version: "1.1.0", package: { sha256: "a".repeat(64), size: 5 },
  components: { sha256: "b".repeat(64), size: 6 } };
const profile = "sqlite-v1-" + "c".repeat(64);
const decision = { status: "direct", next: target, route: [target.releaseId] };
const tick = () => new Promise(resolve => setImmediate(resolve));
const goFile = dir => path.join(dir, "sidecar.json");
const readGo = dir => existsSync(goFile(dir)) ? JSON.parse(readFileSync(goFile(dir), "utf8")) : null;
const shell = dir => createInstallIntentStore(path.join(dir, "install.json"));
const blocked = { status: "blocked", reason: "update_transaction_conflict" };

// The sidecar owns the prepared/authorized distinction. Its signature, actor
// and scope checks are covered at the real Go boundary. A separate Node process
// runs the real source controller and exits without graceful cancellation.
function session(dir, owner, { crashAt, inspectBarrier, discardBarrier } = {}) {
  const events = [];
  const save = value => writeFileSync(goFile(dir), JSON.stringify(value));
  const crash = point => { if (crashAt === point) process.exit(37); };
  const runtime = createUpdateRuntimeClient({
    localWork: () => 0, setAdmission() {}, stopAuxiliary: async () => events.push("stop-auxiliary"),
    async runSidecar(command, args) {
      const arg = name => args.includes(name) ? args[args.indexOf(name) + 1] : undefined;
      if (command === "update-control") {
        const action = arg("--action");
        events.push(action);
        const epoch = action === "pause" ? Number(arg("--epoch")) : 0;
        return JSON.stringify(action === "stop" ? { status: "stopped" } : { active: 0, paused: epoch !== 0, epoch });
      }
      assert.equal(command, "update-components-check");
      const record = readGo(dir);
      if (args.includes("--inspect-handoff")) {
        events.push("inspect");
        const view = !record ? { status: "absent" } : record.phase === "prepared"
          ? { status: "prepared", releaseId: record.releaseId, attemptId: record.attemptId } : blocked;
        await inspectBarrier?.(view);
        return JSON.stringify(view);
      }
      if (args.includes("--discard-handoff")) {
        events.push("discard");
        const id = arg("--attempt-id");
        const result = record && (record.phase !== "prepared" || arg("--discard-handoff") !== record.releaseId || id !== record.attemptId)
          ? blocked : { status: "compatible" };
        await discardBarrier?.(id, result);
        if (result.status === "blocked") return JSON.stringify(result);
        rmSync(goFile(dir), { force: true });
        return JSON.stringify(result);
      }
      const prepare = arg("--prepare-handoff"), authorize = arg("--handoff");
      if (prepare || authorize) {
        assert.equal(prepare || authorize, target.releaseId);
        assert.equal(arg("--package"), path.join(dir, "target.zip"));
        assert.equal(arg("--components"), path.join(dir, "components.json"));
      }
      if (prepare) {
        events.push("prepare");
        if (record ? record.phase !== "prepared" || record.releaseId !== prepare || arg("--attempt-id") !== record.attemptId
          : args.includes("--attempt-id")) return JSON.stringify(blocked);
        const attemptId = (owner === "original" ? "1" : "2").repeat(32);
        save({ phase: "prepared", releaseId: prepare, attemptId, owner });
        crash("after-prepare");
        return JSON.stringify({ status: "prepared", releaseId: prepare, attemptId, dataProfile: profile });
      }
      if (authorize) {
        crash("before-final");
        events.push("authorize");
        if (record ? record.phase !== "prepared" || record.owner !== owner || arg("--attempt-id") !== record.attemptId
          : args.includes("--attempt-id")) return JSON.stringify(blocked);
        save({ ...record, phase: "authorized", releaseId: authorize, owner });
        crash("after-final");
      }
      return JSON.stringify({ status: "compatible", dataProfile: profile });
    },
  });
  const updater = createDesktopUpdater({
    currentVersion: sourceVersion, installIntent: shell(dir), runtime,
    clock: { setTimeout: (fn, ms) => ms <= 2000 ? setImmediate(fn) : { unref() {} }, clearTimeout: clearImmediate },
    readLocalState: async () => ({ dataProfile: profile, dirty: false }),
    invokeHelper: async command => {
      events.push(command);
      if (command === "config") return { status: "enabled", config: { updaterProtocol: 1, version: sourceVersion } };
      if (command === "discover") return decision;
      assert.equal(command, "download");
      return { status: "downloaded", releaseId: target.releaseId, packagePath: path.join(dir, "target.zip"), componentsPath: path.join(dir, "components.json") };
    },
    nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, prepare: async () => ({
      async install() { events.push("native"); return { handoff: true }; },
    }) },
  });
  return { updater, events };
}

async function finish({ updater, events }, detail) {
  for (let i = 0; i < 150 && !events.includes("native") && !["error", "repair"].includes(updater.getState().phase); i++) {
    detail?.(`before finish tick ${i + 1}, phase=${updater.getState().phase}`);
    await tick();
    detail?.(`after finish tick ${i + 1}`);
  }
}

function trace(t, message) {
  const line = `${new Date().toISOString()} [owner-restart] ${t.name} | ${message}`;
  console.log(line);
  if (process.env.OWNER_RESTART_TRACE) appendFileSync(process.env.OWNER_RESTART_TRACE, line + "\n");
}

async function tracedCall(t, updater, method) {
  trace(t, `before updater.${method}()`);
  try {
    const result = await updater[method]();
    trace(t, `after updater.${method}() resolved`);
    return result;
  } catch (error) {
    trace(t, `after updater.${method}() rejected: ${error.message}`);
    throw error;
  }
}

function directory(t, detail) {
  trace(t, "case started");
  const dir = mkdtempSync(path.join(os.tmpdir(), "v1-owner-restart-"));
  t.after(() => {
    detail?.("before directory cleanup");
    rmSync(dir, { recursive: true, force: true });
    detail?.("after directory cleanup");
  });
  return dir;
}

function seedPrepared(dir) {
  shell(dir).write({ version: 1, phase: "pending", sourceVersion, releaseId: target.releaseId,
    nextVersion: target.version, dataProfile: profile, packagePath: path.join(dir, "target.zip") });
  const record = { phase: "prepared", releaseId: target.releaseId, attemptId: "1".repeat(32), owner: "original" };
  writeFileSync(goFile(dir), JSON.stringify(record));
  return record;
}

function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}

function interrupt(t, dir, point) {
  trace(t, `before interrupt child: ${point}`);
  const child = spawnSync(process.execPath, [fileURLToPath(import.meta.url), "--interrupt", dir, point], { encoding: "utf8", timeout: 5000 });
  trace(t, `after interrupt child: ${point}, status=${child.status}, error=${child.error?.code || "none"}`);
  assert.equal(child.status, 37, `source never reached ${point}: ${child.stdout} ${child.stderr}`);
}

if (process.argv[2] === "--interrupt") {
  const old = session(process.argv[3], "original", { crashAt: process.argv[4] });
  await old.updater.check();
  await old.updater.install(target.releaseId);
  await finish(old);
  throw new Error(`source missed crash boundary; events=${old.events.join(",")}`);
} else {
  for (const point of ["after-prepare", "before-final"]) {
    for (const choice of ["continue", "later"]) {
      test(`source restart permits explicit ${choice} at ${point}`, async t => {
        const detail = point === "before-final" && choice === "continue" ? message => trace(t, message) : undefined;
        const dir = directory(t, detail);
        interrupt(t, dir, point);
        const prepared = readGo(dir);
        detail?.("before prepared phase assertion");
        assert.equal(prepared?.phase, "prepared", "the interrupted source must leave only a recoverable preparation");
        detail?.("after prepared phase assertion");
        const current = session(dir, "replacement");
        t.after(() => {
          detail?.("before updater.dispose()");
          current.updater.dispose();
          detail?.("after updater.dispose()");
        });
        await tracedCall(t, current.updater, "start");
        detail?.("before updater.getState()");
        const view = current.updater.getState();
        detail?.("after updater.getState()");
        detail?.("before recovery state assertion");
        assert.ok(view.pendingUpdate && view.phase !== "repair" && !view.updating, "renderer must expose both Continue and Later");
        detail?.("after recovery state assertion");
        detail?.("before startup events assertion");
        assert.ok(!current.events.some(event => ["download", "prepare", "authorize", "stop", "native"].includes(event)));
        detail?.("after startup events assertion");
        if (choice === "later") {
          await tracedCall(t, current.updater, "later");
          assert.equal(readGo(dir), null);
          assert.equal(shell(dir).read(), null);
          assert.ok(!current.updater.getState().pendingUpdate);
          assert.ok(!current.events.includes("native"));
        } else {
          detail?.("before updater.install()");
          await current.updater.install(view.releaseId);
          detail?.("after updater.install()");
          detail?.("before finish(current)");
          await finish(current, detail);
          detail?.("after finish(current)");
          detail?.("before handoff events assertion");
          assert.deepEqual(current.events.filter(event => ["prepare", "authorize", "stop", "native"].includes(event)), ["prepare", "authorize", "stop", "native"]);
          detail?.("after handoff events assertion");
          detail?.("before replacement owner assertion");
          assert.equal(readGo(dir).owner, "replacement");
          detail?.("after replacement owner assertion");
        }
      });
    }
  }
  for (const scenario of ["authorized", "legacy", "unknown", "different-target", "different-attempt"]) {
    test(`a reopened source cannot clear or dispatch the handoff: ${scenario}`, async t => {
      const dir = directory(t);
      if (scenario === "authorized") interrupt(t, dir, "after-final");
      else if (scenario === "different-attempt") {
        interrupt(t, dir, "before-final");
        const prior = readGo(dir);
        assert.equal(prior?.phase, "prepared");
        // The Go proof for a different attempt at the same public release
        // cannot turn an uncertain local installation back into a retry.
        shell(dir).write({ ...shell(dir).read(), phase: "installing" });
        writeFileSync(goFile(dir), JSON.stringify({ ...prior, attemptId: "3".repeat(32) }));
      }
      else {
        shell(dir).write({ version: 1, phase: scenario === "unknown" ? "installing" : "pending", sourceVersion, releaseId: target.releaseId,
          nextVersion: target.version, dataProfile: profile, packagePath: path.join(dir, "target.zip") });
        if (scenario !== "unknown") writeFileSync(goFile(dir), JSON.stringify(scenario === "legacy"
          ? { releaseId: target.releaseId, owner: "original" }
          : { phase: "prepared", releaseId: "another-release", attemptId: "3".repeat(32), owner: "original" }));
      }
      const before = readGo(dir), current = session(dir, "replacement");
      t.after(() => current.updater.dispose());
      await tracedCall(t, current.updater, "start");
      assert.equal(current.updater.getState().phase, "repair");
      await assert.rejects(current.updater.install(target.releaseId));
      await assert.rejects(tracedCall(t, current.updater, "later"));
      assert.deepEqual(readGo(dir), before);
      assert.ok(shell(dir).read());
      assert.ok(!current.events.some(event => ["prepare", "authorize", "discard", "stop", "native"].includes(event)));
    });
  }
  test("Later cannot be undone by a delayed startup reply that observed the prepared handoff", async t => {
    const dir = directory(t);
    const prepared = seedPrepared(dir);
    const captured = deferred(), reply = deferred();
    let first = true;
    const current = session(dir, "replacement", { inspectBarrier(view) {
      if (!first) return;
      first = false;
      captured.resolve(view);
      return reply.promise;
    } });
    t.after(() => { reply.resolve(); current.updater.dispose(); });
    const starting = tracedCall(t, current.updater, "start");
    const observed = await Promise.race([captured.promise, starting.then(() => assert.fail("startup returned without inspecting the prepared handoff"))]);
    assert.deepEqual(observed, { status: "prepared", releaseId: target.releaseId, attemptId: prepared.attemptId });
    const done = Promise.allSettled([starting, tracedCall(t, current.updater, "later")]);
    // An independent cancel can finish now. A serialized cancel can wait for
    // the captured read; both must leave the Go and shell receipts cleared.
    await tick();
    reply.resolve();
    for (const result of await done) assert.equal(result.status, "fulfilled", result.reason?.message);
    assert.equal(readGo(dir), null);
    assert.equal(shell(dir).read(), null);
    assert.ok(!current.updater.getState().pendingUpdate, "a stale read republished the cleared reminder");
    assert.ok(!current.events.some(event => ["download", "prepare", "authorize", "stop", "native"].includes(event)));
  });

  test("Later retains both receipts when the same target has a different attempt by the time Go handles cancellation", async t => {
    const dir = directory(t), prepared = seedPrepared(dir);
    const checked = deferred(), acknowledge = deferred();
    const current = session(dir, "replacement", { discardBarrier(id, result) {
      checked.resolve({ id, result });
      return acknowledge.promise;
    } });
    t.after(() => { acknowledge.resolve(); current.updater.dispose(); });
    await tracedCall(t, current.updater, "start");
    const view = current.updater.getState();
    assert.ok(view.pendingUpdate && view.phase !== "repair", "Later was not initially available");
    const reminder = shell(dir).read();
    const newer = { ...prepared, attemptId: "3".repeat(32), owner: "another-owner" };
    writeFileSync(goFile(dir), JSON.stringify(newer));
    const outcome = tracedCall(t, current.updater, "later").then(() => "accepted", () => "rejected");
    const requested = await Promise.race([checked.promise, outcome.then(() => assert.fail("Later returned without an attempt-bound discard acknowledgement"))]);
    assert.equal(requested.id, prepared.attemptId);
    assert.equal(requested.result.status, "blocked");
    assert.deepEqual(shell(dir).read(), reminder, "shell reminder was removed before Go acknowledged cancellation");
    acknowledge.resolve();
    assert.equal(await outcome, "rejected");
    assert.deepEqual(shell(dir).read(), reminder);
    assert.deepEqual(readGo(dir), newer);
    assert.ok(!current.events.some(event => ["download", "prepare", "authorize", "stop", "native"].includes(event)));
  });
}
