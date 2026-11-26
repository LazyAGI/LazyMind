import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import test from "node:test";
import vm from "node:vm";
import startup from "../electron/src/update-startup.js";

const main = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8").replace(/\r\n/g, "\n");
const entry = main.slice(main.indexOf("app.whenReady().then"), main.indexOf('  app.on("window-all-closed"'));

test("an interrupted activated target explains which version can resume, without offering invalid adoption", async t => {
  const logs = fs.mkdtempSync(path.join(os.tmpdir(), "update-recovery-"));
  t.after(() => fs.rmSync(logs, { recursive: true, force: true }));
  let callback, message, offered, code;
  const actions = [];
  const state = { status: "repair", phase: "activated", sourceVersion: "0.5.225",
    currentReleaseId: "mac-228", targetReleaseId: "mac-227", targetVersion: "0.5.227",
    transactionId: "1".repeat(32), owner: "private-fixture-owner" };
  vm.runInNewContext(entry, {
    app: { whenReady: () => ({ then(fn) { callback = fn; } }), getPath: () => logs, exit(value) { code = value; } },
    isPackaged: true, currentVersion: "0.5.228", currentReleaseId: state.currentReleaseId, ownerToken: state.owner,
    prepareDesktopStartup: startup.prepareDesktopStartup, adoptDesktopRepair: startup.adoptDesktopRepair,
    async runSidecar(command, args) { actions.push(args[1]); return JSON.stringify(state); },
    dialog: { showMessageBox: async options => { offered = options.buttons; return { response: 1 }; },
      showErrorBox(_title, detail) { message = detail; } }, recordUpdateStartup: startup.recordUpdateStartup,
  });
  await callback();
  assert.deepEqual(actions, ["inspect"]);
  assert.equal(code, 1);
  assert.ok(!offered?.includes("Verify this version"));
  assert.match(message, /0\.5\.227/);
  const log = fs.readFileSync(path.join(logs, "desktop-update-recovery.log"), "utf8");
  assert.match(log, /0\.5\.228/);
  assert.match(log, /activated/);
  assert.doesNotMatch(log, /private-fixture-owner/);
});

test("reused release ID cannot hide an activated version mismatch", async () => {
  const actions = [], events = [];
  await assert.rejects(startup.prepareDesktopStartup({ isPackaged: true, currentVersion: "0.5.228",
    ownerToken: "owner", currentReleaseId: "same-id", onRecovery: event => events.push(event),
    async invoke(_cmd, args) {
      actions.push(args[1]);
      return JSON.stringify({ status: "maintenance_required", phase: "activated", transactionId: "1".repeat(32),
        owner: "owner", targetReleaseId: "same-id", targetVersion: "0.5.227", currentReleaseId: "same-id" });
    },
  }), { code: "update_original_target_required", targetVersion: "0.5.227" });
  assert.deepEqual(actions, ["inspect"]);
  assert.equal(events[0].targetVersion, "0.5.227");
});

for (const failedAction of ["prepare-components", "up"]) {
  test(`recovery diagnostics use the adopted target when ${failedAction} fails`, async () => {
    const tx = "a".repeat(32), identity = { transactionId: tx, owner: "owner" };
    const events = [];
    await assert.rejects(startup.adoptDesktopRepair({ isPackaged: true, currentVersion: "0.5.228",
      currentReleaseId: "new", ownerToken: identity.owner, onRecovery: event => events.push(event),
      async invoke(_, args) {
        const step = args[1];
        if (step === failedAction) return '{"status":"blocked","reason":"update_startup_failed"}';
        return JSON.stringify({
          inspect: { ...identity, status: "repair", phase: "installing", targetReleaseId: "old", targetVersion: "0.5.227",
            dataProfile: "fixture", sourceHadRAG: false, componentMode: "absent" },
          "repair-adopt-current": { ...identity, status: "adopted", targetReleaseId: "new", targetVersion: "0.5.228" },
          "prepare-components": { ...identity, status: "prepared" }, activate: { ...identity, status: "activated" },
        }[step]);
      },
    }), { code: "update_startup_failed" });
    assert.deepEqual(events, [{ action: failedAction, reason: "update_startup_failed", installedVersion: "0.5.228",
      targetVersion: "0.5.228", phase: failedAction === "up" ? "activated" : "repair" }]);
  });
}

test("the early guard preserves safe native reasons and drops free-form stderr", () => {
  for (const [stderr, code] of [["native_install_unavailable\n", "native_install_unavailable"],
    ["failed with sensitive path /home/private", "update_startup_blocked"]]) {
    assert.throws(() => startup.guardDesktopStartup({ isPackaged: true, args: ["startup-guard"],
      invoke() { throw Object.assign(new Error("child exited"), { stderr }); } }), { message: "update_startup_blocked", code });
  }
  assert.doesNotThrow(() => startup.recordUpdateStartup(() => { throw Error("early path unavailable"); }, {}));
});

test("an early Windows failure is recorded in the same directory as runtime diagnostics", t => {
  const logs = fs.mkdtempSync(path.join(os.tmpdir(), "early-windows-logs-"));
  t.after(() => fs.rmSync(logs, { recursive: true, force: true }));
  const begin = main.indexOf('try {\n  guardDesktopStartup');
  const end = main.indexOf("if (windowsDesktopPaths)", begin);
  assert.ok(begin >= 0 && end > begin);
  let exitCode;
  assert.throws(() => vm.runInNewContext(main.slice(begin, end), {
    app: { exit: code => { exitCode = code; }, getPath: () => { throw Error("Electron path not initialized"); } },
    windowsDesktopPaths: { logsDir: logs }, isPackaged: true, currentVersion: "0.5.228",
    process: { env: {} }, sidecarPath: "fixture", sidecarArgs: () => [],
    guardDesktopStartup: () => { throw Object.assign(new Error("update_startup_blocked"), { code: "update_record_read" }); },
    recordUpdateStartup: startup.recordUpdateStartup, dialog: { showErrorBox() {} },
  }), /update_startup_blocked/);
  assert.equal(exitCode, 1);
  const entry = JSON.parse(fs.readFileSync(path.join(logs, "desktop-update-recovery.log"), "utf8"));
  assert.equal(entry.reason, "update_record_read");
  assert.equal(entry.action, "startup-guard");
});
