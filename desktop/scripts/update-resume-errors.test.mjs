import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";
const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const { createDownloadIntentStore } = require("../electron/src/update-download-intent.js");
const intent = { version: 1, sourceVersion: "1.0.0", nextVersion: "1.1.0", releaseId: "fixture-r2" };
function fixture(t) {
  const dir = mkdtempSync(path.join(tmpdir(), "lazymind-update-errors-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, "intent.json"), store = createDownloadIntentStore(file), timers = [], calls = [];
  const make = (storage = store) => {
    const updater = createDesktopUpdater({ currentVersion: "1.0.0", downloadIntent: storage,
      readLocalState: async () => ({}), clock: { setTimeout: callback => { timers.push(callback); return callback; }, clearTimeout() {} },
      invokeHelper: async command => {
        calls.push(command);
        if (command === "config") return { status: "enabled", config: {} };
        if (command === "discover") return { status: "direct", route: [intent.releaseId], next: { releaseId: intent.releaseId, version: intent.nextVersion, package: { size: 100 } } };
        if (command === "download") return { status: "downloaded", releaseId: intent.releaseId, packagePath: "/fixture/package.zip" };
        throw new Error("unexpected command");
      } });
    t.after(() => updater.dispose()); return updater;
  };
  return { file, store, timers, calls, make };
}

test("startup and periodic checks preserve record failures until an explicit recheck", async t => {
  const f = fixture(t); writeFileSync(f.file, "{broken");
  const updater = f.make(); await updater.start();
  assert.equal(updater.getState().reason, "update_record_read");
  await f.timers[0](); assert.equal(updater.getState().reason, "update_record_read");
  assert.deepEqual(f.calls, []);
  await updater.check(); assert.equal(updater.getState().phase, "available");
  assert.equal(f.store.read(), null);
});

test("failed cancellation preserves a recoverable target for continue, retry-cancel and process recreation", async t => {
  for (const action of ["continue", "cancel", "recreate"]) {
    const f = fixture(t); f.store.write(intent); let failClear = true;
    const updater = f.make({ ...f.store, clear: () => { if (failClear) throw new Error("/private/fixture"); f.store.clear(); } });
    const result = updater.cancel();
    assert.equal(result.reason, "update_record_write");
    assert.equal(result.pendingDownload, true);
    assert.deepEqual(f.store.read(), intent);
    failClear = false;
    if (action === "continue") { await updater.resumeDownload(); assert.equal(updater.getState().phase, "downloaded"); }
    if (action === "cancel") { updater.cancel(); assert.equal(f.store.read(), null); assert.equal(updater.getState().pendingDownload, false); }
    if (action === "recreate") { const reopened = f.make(createDownloadIntentStore(f.file)); await reopened.start(); assert.equal(reopened.getState().phase, "interrupted"); }
  }
});
