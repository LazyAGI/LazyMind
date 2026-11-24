import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller");
const { createDownloadIntentStore } = require("../electron/src/update-download-intent");
const { createCLIInstallIntentStore } = require("../electron/src/update-install-intent");
const a = "a".repeat(32), b = "b".repeat(32);
const offers = { releaseId: "target", version: "1.1.0", package: { size: 1 }, components: { size: 1 } };
const profile = "sqlite-v1-fixture";
const prepared = id => ({ status: "prepared", releaseId: offers.releaseId, attemptId: id });
const absent = { status: "absent" };
const error = code => Object.assign(new Error(code), { code });

function fixture(t) {
  const directory = mkdtempSync(path.join(os.tmpdir(), "cancel-v1-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const filename = path.join(directory, "install.json");
  const openStore = () => createCLIInstallIntentStore(filename, { invokeSync(action) {
    assert.equal(action, "inspect-store");
    return '{"status":"allowed"}';
  } });
  const initial = { version: 1, phase: "pending", sourceVersion: "1.0.0", nextVersion: offers.version,
    releaseId: offers.releaseId, attemptId: a, dataProfile: profile, packagePath: path.join(directory, "pkg.zip") };
  openStore().write(initial);
  const f = { proof: prepared(a), failClear: false, failDiscard: false, inspect: null, incompatible: false, offer: offers };
  const downloadStore = createDownloadIntentStore(path.join(directory, "download.json"));
  f.downloadStore = downloadStore;
  f.store = openStore;
  f.read = () => openStore().read();
  f.open = () => {
    const events = [], store = openStore(), clear = store.clear;
    store.clear = () => {
      if (f.failClear) throw error("update_record_write");
      clear();
    };
    const updater = createDesktopUpdater({ currentVersion: initial.sourceVersion, installIntent: store,
      downloadIntent: { ...downloadStore, clear() {
        if (f.failDownloadClear) throw error("update_record_write");
        downloadStore.clear();
      } },
      runtime: {
        async inspectV1Handoff() {
          events.push("inspect");
          const view = f.proof;
          await f.inspect?.();
          if (view.status === "blocked") throw error("update_transaction_conflict");
          return view;
        },
        async discardV1Handoff(release, attempt) {
          events.push("discard:" + attempt);
          if (f.failDiscard) throw error("data_unknown");
          if (f.proof.status !== "absent" && (f.proof.status !== "prepared" || f.proof.attemptId !== attempt || f.proof.releaseId !== release)) throw error("update_transaction_conflict");
          f.proof = absent;
        },
        async verifyComponents() { if (f.incompatible) throw error("component_update_incompatible"); return { dataProfile: profile }; },
      },
      readLocalState: async () => ({ dataProfile: profile, dirty: false }),
      invokeHelper: async command => {
        events.push(command);
        if (command === "config") return { status: "enabled", config: { updaterProtocol: 1 } };
        if (command === "discover") return { status: "direct", next: f.offer, route: [f.offer.releaseId] };
        if (command === "download") return { status: "downloaded", releaseId: offers.releaseId, packagePath: initial.packagePath };
        assert.fail(command);
      },
      nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, prepare() { events.push("native"); throw Error("unexpected install"); } },
    });
    t.after(() => updater.dispose());
    return { updater, events };
  };
  return f;
}

for (const retry of ["restart", "same session"]) {
  test(`explicit Later recovers after Go removed A and shell clear failed: ${retry}`, async t => {
    const f = fixture(t), old = f.open();
    await old.updater.start();
    f.failClear = true;
    await assert.rejects(old.updater.later(), { code: "update_record_write" });
    assert.equal(f.proof.status, "absent");
    assert.equal(f.read().attemptId, a);
    f.failClear = false;
    if (retry === "same session") {
      assert.equal(old.updater.getState().pendingCancellation, true, "the renderer must expose a cancellation retry");
      await assert.rejects(old.updater.install(offers.releaseId));
      await old.updater.later();
      assert.equal(old.updater.getState().pendingUpdate, false);
      assert.ok(!old.events.includes("download"));
    } else {
      old.updater.dispose();
      const fresh = f.open();
      let entered, allow;
      const inspecting = new Promise(resolve => { entered = resolve; });
      const gate = new Promise(resolve => { allow = resolve; });
      f.inspect = () => { entered(); return gate; };
      const starting = fresh.updater.start();
      await inspecting;
      const staleClick = fresh.updater.install(offers.releaseId).catch(() => {});
      allow();
      await Promise.all([starting, staleClick]);
      assert.equal(fresh.updater.getState().pendingUpdate, false);
      assert.ok(!["repair", "interrupted"].includes(fresh.updater.getState().phase));
      assert.ok(!fresh.events.some(x => x === "download" || x === "native" || x.startsWith("discard:")));
    }
    assert.equal(f.read(), null);
  });
}

for (const scenario of ["still A", "replacement B", "already authorized"]) {
  test(`reopened Later reconciles exactly its saved cancellation: ${scenario}`, async t => {
    const f = fixture(t), old = f.open();
    await old.updater.start();
    f.failDiscard = true;
    await assert.rejects(old.updater.later());
    old.updater.dispose();
    f.failDiscard = false;
    if (scenario === "replacement B") f.proof = { ...prepared(b), previousAttemptId: a };
    if (scenario === "already authorized") f.proof = { status: "blocked" };
    const expected = f.proof, fresh = f.open();
    await fresh.updater.start();
    if (scenario === "still A") {
      assert.equal(f.read(), null);
      assert.equal(f.proof.status, "absent");
      assert.deepEqual(fresh.events.filter(x => x.startsWith("discard:")), ["discard:" + a]);
    } else {
      assert.equal(f.read().attemptId, a);
      await assert.rejects(fresh.updater.install(offers.releaseId));
      await assert.rejects(fresh.updater.later());
      assert.deepEqual(f.proof, expected);
      assert.ok(!fresh.events.some(x => x.startsWith("discard:")));
    }
    assert.ok(!fresh.events.some(x => x === "download" || x === "native"));
  });
}

test("incompatible-update cleanup can finish after a restart without reoffering the same update", async t => {
  const f = fixture(t), old = f.open();
  await old.updater.start();
  f.incompatible = true;
  f.failDownloadClear = true;
  await old.updater.install(offers.releaseId);
  for (let i = 0; i < 100 && f.proof.status !== "absent"; i++) await new Promise(setImmediate);
  assert.equal(f.proof.status, "absent");
  assert.equal(f.read().attemptId, a);
  assert.equal(f.downloadStore.read().releaseId, offers.releaseId);
  old.updater.dispose();
  f.failDownloadClear = false;
  f.offer = { ...offers, releaseId: "new-target", version: "1.2.0" };
  const fresh = f.open();
  await fresh.updater.start();
  assert.equal(f.read(), null);
  assert.equal(f.downloadStore.read(), null);
  assert.equal(fresh.updater.getState().phase, "blocked");
  assert.equal(fresh.updater.getState().reason, "component_update_incompatible");
  assert.ok(!fresh.events.some(x => ["discover", "download", "native"].includes(x)));
  const checked = await fresh.updater.check();
  assert.equal(checked.phase, "available");
  assert.equal(checked.releaseId, "new-target");
  assert.ok(fresh.events.includes("discover"));
  assert.ok(!fresh.events.some(x => ["download", "native"].includes(x)));
});

for (const replacement of ["no reminder", "new unbound reminder", "new bound reminder"]) {
  test(`a stale cancellation record cannot act on ${replacement}`, async t => {
    const f = fixture(t), old = f.open();
    await old.updater.start();
    f.failClear = true;
    await assert.rejects(old.updater.later());
    const original = f.read();
    assert.equal(f.store().cancellation.read().attemptId, a);
    old.updater.dispose();
    f.failClear = false;
    f.proof = replacement === "new unbound reminder" ? absent : prepared(b);
    if (replacement === "no reminder") f.store().clear(1);
    else f.store().write({ ...original, attemptId: replacement === "new bound reminder" ? b : undefined });
    const expected = f.read(), goBefore = f.proof, fresh = f.open();
    await fresh.updater.start();
    assert.equal(f.store().cancellation.read(), null);
    assert.deepEqual(f.read(), expected);
    assert.deepEqual(f.proof, goBefore);
    assert.equal(Boolean(fresh.updater.getState().pendingUpdate), Boolean(expected));
    assert.ok(!fresh.events.some(x => x.startsWith("discard:") || x === "download" || x === "native"));
    if (!expected) assert.ok(!fresh.events.includes("inspect"));
  });
}
