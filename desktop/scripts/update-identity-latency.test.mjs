import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import vm from "node:vm";

const require = createRequire(import.meta.url);
const { guardDesktopStartup, prepareDesktopStartup } = require("../electron/src/update-startup.js");
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client.js");
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const { createCLIInstallIntentStore } = require("../electron/src/update-install-intent.js");

// Model a bounded but slow signature provider at the existing process boundary.
// No real timer is needed to prove that the parent kills it prematurely.
const needsSignatureBudget = limits => {
  if (limits.timeout < 70_000) throw new Error("ETIMEDOUT");
};

test("startup guard and bootstrap inspection allow a slow identity provider", async () => {
  guardDesktopStartup({ isPackaged: true, sidecarPath: "/fixture/manager", args: ["startup-guard"],
    invoke: (_file, _args, limits) => { needsSignatureBudget(limits); return '{"status":"allowed"}'; } });
  const result = await prepareDesktopStartup({ isPackaged: true, ownerToken: "fixture-owner",
    invoke: async (_command, _args, limits) => { needsSignatureBudget(limits); return '{"status":"allowed"}'; } });
  assert.equal(result.allowed, true);
  assert.throws(() => guardDesktopStartup({ isPackaged: true, args: ["startup-guard"],
    invoke: () => { throw new Error("invalid signature"); } }), /update_startup_blocked/);
});

test("prepared component verification allows identity latency and still rejects failures", async () => {
  let reject = false;
  const client = createUpdateRuntimeClient({ runSidecar: async (_command, args, limits) => {
    if (args.includes("prepare-components")) return JSON.stringify({ transactionId: "a".repeat(32) });
    needsSignatureBudget(limits);
    return JSON.stringify(reject ? { status: "blocked", reason: "native_install_unavailable" }
      : { status: "compatible", dataProfile: "format-v2-fixture" });
  } });
  await client.prepareComponents({ releaseId: "target" }, { packagePath: "/fixture/app.zip", componentsPath: "/fixture/components.json" });
  assert.equal((await client.verifyComponents()).dataProfile, "format-v2-fixture");
  reject = true;
  await assert.rejects(client.verifyComponents(), { code: "native_install_unavailable" });
});

test("actual main wiring allows slow install-store reads and clears while rejecting blocked identity", t => {
  const root = mkdtempSync(path.join(tmpdir(), "lazymind-identity-store-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const start = source.indexOf("const desktopUpdater = createDesktopUpdater(");
  const end = source.indexOf("\nregisterUpdateIPC", start);
  assert.ok(start >= 0 && end > start);
  let store, active = false, blocked = false, rejectCancel = false;
  const calls = [];
  const context = {
    path, isPackaged: true, isWindows: false, sidecarPath: "/fixture/manager",
    app: { getVersion: () => "1.1.0", getPath: () => root }, createDesktopUpdater,
    createCLIInstallIntentStore: (...args) => (store = createCLIInstallIntentStore(...args)),
    createDownloadIntentStore: () => ({ read: () => null, clear() {}, write() {} }),
    sidecarArgs: (command, extra) => [command, ...extra], sidecarEnv: () => ({}),
    runSidecar: async () => '{"status":"allowed"}',
    nativeUpdateInstaller: { capability: () => ({ supported: true }), dispose() {} },
    updateRuntime: {}, invokeUpdateHelper: async () => ({ status: "enabled", config: { version: "1.1.0" } }),
    shell: { showItemInFolder() {} }, appendStartupLog() {},
    require(name) {
      assert.equal(name, "node:child_process");
      return { execFileSync(_file, args, limits) {
        needsSignatureBudget(limits);
        const action = args[args.indexOf("--action") + 1];
        calls.push(action);
        if (blocked) return '{"status":"blocked","reason":"native_install_unavailable"}';
        if (action === "cancel") return JSON.stringify({ status: rejectCancel ? "blocked" : "cancelled" });
        return JSON.stringify({ status: "allowed", ...(active ? { transactionId: "a".repeat(32) } : {}) });
      } };
    },
  };
  vm.runInNewContext(source.slice(start, end) + "\nthis.updater = desktopUpdater;", context);
  t.after(() => context.updater.dispose());
  assert.equal(context.updater.getState().phase, "idle");
  assert.equal(store.read(), null);
  active = true;
  assert.equal(store.read().transactionId, "a".repeat(32));
  store.clear();
  assert.equal(calls.at(-1), "cancel");
  blocked = true;
  assert.throws(() => store.read(), /update_record_read/);
  assert.throws(() => store.clear(), /update_record_read/);
  blocked = false; rejectCancel = true;
  assert.throws(() => store.clear(), /update_record_write/);
});
