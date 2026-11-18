import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const { createDownloadIntentStore } = require("../electron/src/update-download-intent.js");
const target = { releaseId: "fixture-r2", version: "1.1.0", package: { size: 100 } };
const decision = () => ({ status: "direct", next: target, route: [target.releaseId] });
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
const settle = () => new Promise(resolve => setImmediate(resolve));

function fixture(t, options = {}) {
  const directory = mkdtempSync(path.join(tmpdir(), "lazymind-update-intent-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const filename = path.join(directory, "download-intent.json");
  const store = createDownloadIntentStore(filename);
  const calls = [];
  const make = (extra = {}) => {
    const updater = createDesktopUpdater({ currentVersion: "1.0.0", downloadIntent: store,
      readLocalState: async () => ({}),
      invokeHelper: async (command, request, control) => {
        calls.push({ command, request });
        if (command === "config") return { status: "enabled", config: { channel: "stable" } };
        if (command === "discover") return options.discover ? options.discover() : decision();
        if (command === "download") return options.download ? options.download(control) :
          { status: "downloaded", releaseId: target.releaseId, packagePath: "/fixture/package.zip" };
        throw new Error("unexpected operation");
      }, ...extra });
    t.after(() => updater.dispose());
    return updater;
  };
  return { store, filename, calls, make };
}

test("an interrupted download is visible after a new controller starts, without automatic download", async t => {
  const transfer = deferred();
  const f = fixture(t, { download: () => transfer.promise });
  const first = f.make(); await first.check();
  const pending = first.download(target.releaseId); await settle();
  assert.deepEqual(f.store.read(), { version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
  first.dispose();
  const reopened = createDownloadIntentStore(f.filename);
  const second = f.make({ downloadIntent: reopened }); await second.start();
  assert.equal(second.getState().phase, "interrupted");
  assert.equal(second.getState().nextVersion, "1.1.0");
  assert.equal(f.calls.filter(call => call.command === "download").length, 1);
  transfer.resolve({ status: "downloaded", releaseId: target.releaseId, packagePath: "/fixture/package.zip" }); await pending;
  assert.equal(second.getState().phase, "interrupted", "the old operation cannot complete the new session");
  assert.deepEqual(createDownloadIntentStore(f.filename).read(), { version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
});

test("continue rechecks the target and sends cached-package verification through the existing helper", async t => {
  const f = fixture(t);
  f.store.write({ version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
  const updater = f.make(); await updater.start(); await updater.resumeDownload();
  assert.deepEqual(f.calls.map(call => call.command), ["config", "discover", "download"]);
  assert.equal(f.calls.at(-1).request.releaseId, target.releaseId);
  assert.equal(updater.getState().phase, "downloaded");
  assert.equal(updater.getState().canInstall, false, "download recovery does not authorize installation");
  assert.doesNotMatch(readFileSync(f.filename, "utf8"), /packagePath|https?:|token/i);
});

test("a replacement or revoked release is not silently downloaded by continue", async t => {
  for (const response of [{ ...decision(), next: { ...target, releaseId: "fixture-r3" }, route: ["fixture-r3"] }, { status: "blocked", reason: "source_revoked" }]) {
    const f = fixture(t, { discover: () => response });
    f.store.write({ version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
    const updater = f.make(); await updater.resumeDownload();
    assert.equal(f.calls.filter(call => call.command === "download").length, 0);
    assert.notEqual(updater.getState().phase, "downloaded");
  }
});

test("cancel during the continuation check prevents the later download", async t => {
  const check = deferred(); const f = fixture(t, { discover: () => check.promise });
  f.store.write({ version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
  const updater = f.make(); const resuming = updater.resumeDownload(); await settle();
  updater.cancel(); check.resolve(decision()); await resuming;
  assert.equal(f.calls.filter(call => call.command === "download").length, 0);
  assert.equal(f.store.read(), null);
});

test("explicit and scheduled checks cannot lose a completed package or restart it", async t => {
  const f = fixture(t); let revealed;
  const updater = f.make({ revealPackage: file => { revealed = file; } });
  await updater.check(); await updater.download(target.releaseId);
  await updater.check(); updater.cancel(); await updater.revealDownloaded();
  assert.equal(updater.getState().phase, "downloaded");
  assert.equal(revealed, "/fixture/package.zip");
  assert.equal(f.calls.filter(call => call.command === "discover").length, 1);
  assert.equal(f.store.read(), null, "cancel removes the pending intention, not the cached package");
});

test("cancelling an active transfer clears disk intent even when old progress and success arrive later", async t => {
  const transfer = deferred(); let control;
  const f = fixture(t, { download: options => { control = options; return transfer.promise; } });
  const updater = f.make(); await updater.check(); const downloading = updater.download(target.releaseId); await settle();
  assert.ok(createDownloadIntentStore(f.filename).read());
  updater.cancel(); assert.equal(control.signal.aborted, true);
  assert.equal(createDownloadIntentStore(f.filename).read(), null);
  control.onProgress({ downloadedBytes: 100, totalBytes: 100 });
  transfer.resolve({ status: "downloaded", releaseId: target.releaseId, packagePath: "/fixture/late.zip" }); await downloading;
  assert.equal(createDownloadIntentStore(f.filename).read(), null);
  const reopened = f.make({ downloadIntent: createDownloadIntentStore(f.filename) });
  assert.notEqual(reopened.getState().phase, "interrupted");
  assert.equal(f.calls.filter(call => call.command === "download").length, 1);
});

test("cancelling the interrupted prompt persists across controller and store recreation", async t => {
  const f = fixture(t);
  f.store.write({ version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
  const updater = f.make({ downloadIntent: createDownloadIntentStore(f.filename) });
  assert.equal(updater.getState().phase, "interrupted"); updater.cancel();
  assert.notEqual(updater.getState().phase, "interrupted");
  assert.equal(createDownloadIntentStore(f.filename).read(), null);
  const reopened = f.make({ downloadIntent: createDownloadIntentStore(f.filename) });
  assert.notEqual(reopened.getState().phase, "interrupted");
  assert.equal(f.calls.length, 0);
});

test("disk write failure prevents an unrecorded transfer and is reported without local paths", async t => {
  const f = fixture(t);
  const updater = f.make({ downloadIntent: { read: () => null, write: () => { throw new Error("/private/user/path"); }, clear: () => {} } });
  await updater.check(); await updater.download(target.releaseId);
  assert.equal(f.calls.filter(call => call.command === "download").length, 0);
  assert.equal(updater.getState().reason, "update_record_write");
  assert.doesNotMatch(JSON.stringify(updater.getState()), /private\/user/);
});

test("invalid records and records from another installed version never imply successful installation", async t => {
  const f = fixture(t);
  writeFileSync(f.filename, "{truncated");
  const invalid = f.make();
  assert.equal(invalid.getState().phase, "error");
  assert.equal(invalid.getState().reason, "update_record_read");
  assert.equal(f.calls.length, 0);
  f.store.write({ version: 1, sourceVersion: "0.9.0", releaseId: target.releaseId, nextVersion: "1.1.0" });
  const changed = f.make(); await changed.start();
  assert.equal(f.calls.filter(call => call.command === "download").length, 0);
  assert.notEqual(changed.getState().phase, "installed");
});

test("download intent storage is bounded, validates records, and clears idempotently", t => {
  const f = fixture(t);
  assert.equal(f.store.read(), null);
  assert.throws(() => f.store.write({ version: 1, releaseId: "../../arbitrary", sourceVersion: "1", nextVersion: "2" }));
  const valid = { version: 1, sourceVersion: "1.0.0", releaseId: target.releaseId, nextVersion: "1.1.0" };
  writeFileSync(f.filename, JSON.stringify(valid)); assert.deepEqual(f.store.read(), valid);
  writeFileSync(f.filename, " ".repeat(8192) + JSON.stringify(valid)); assert.throws(() => f.store.read());
  f.store.clear(); f.store.clear(); assert.equal(f.store.read(), null);
});
