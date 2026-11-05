import assert from "node:assert/strict";
import fs from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopUpdater } = require("../electron/src/update-controller.js");
const { createInstallIntentStore } = require("../electron/src/update-install-intent.js");
const { createDownloadIntentStore } = require("../electron/src/update-download-intent.js");
const settle = () => new Promise(resolve => setImmediate(resolve));
const release = { releaseId: "fixture-target", version: "1.1.0", package: { size: 4, sha256: "a".repeat(64) } };
const profile = "fixture-profile";

function fixture(t) {
  const directory = fs.mkdtempSync(path.join(tmpdir(), "lazymind-install-failures-"));
  const intentPath = path.join(directory, "install.json"), downloadPath = path.join(directory, "download.json");
  const packagePath = path.join(directory, "fixture.zip"); fs.writeFileSync(packagePath, "fake");
  const intent = createInstallIntentStore(intentPath), download = createDownloadIntentStore(downloadPath);
  const calls = [], states = [], controllers = [], timers = new Set();
  let epoch = 0;
  const clock = { setTimeout(fn, ms) { const timer = { fn, ms }; timers.add(timer); return timer; }, clearTimeout(timer) { timers.delete(timer); } };
  const runtime = {
    async verifyComponents() { return { dataProfile: profile }; },
    async status() { calls.push("status"); return { active: 0, paused: false, epoch: 0 }; },
    async pause(value) { calls.push("pause"); epoch = value; return { active: 0, paused: true, epoch }; },
    async resume(value) { assert.equal(value, epoch); calls.push("resume"); epoch = 0; },
    async stop() { calls.push("stop"); assert.ok(epoch > 0); assert.equal(intent.read().phase, "installing"); },
    async verifyStartup() { calls.push("verify"); return { configuration: { releaseId: release.releaseId, version: release.version }, local: { dataProfile: profile, dirty: false } }; },
  };
  const make = (options = {}) => {
    const updater = createDesktopUpdater({ currentVersion: options.version || "1.0.0", installIntent: intent, downloadIntent: download, clock,
      runtime: { ...runtime, ...options.runtime }, readLocalState: async () => ({ dataProfile: profile, dirty: false }),
      nativeInstaller: { capability: () => ({ supported: true }), dispose() {}, async prepare() {
        calls.push("prepare"); return { async install() { calls.push("install"); if (options.installError) throw options.installError; return { handoff: true }; } };
      } },
      invokeHelper: async (command, request) => {
        calls.push(command);
        if (command === "config") return { status: "enabled", config: { version: options.version || "1.0.0" } };
        if (command === "discover") return options.version === release.version ? { status: "up_to_date", reason: "no_newer_compatible_release" } : { status: "direct", next: release, route: [release.releaseId] };
        if (command === "download") return { status: "downloaded", releaseId: request.releaseId, version: release.version, packagePath };
        throw new Error(`unexpected helper command: ${command}`);
      },
      revealPackage: filename => { assert.equal(filename, packagePath); assert.equal(fs.readFileSync(filename, "utf8"), "fake"); calls.push("reveal"); },
    });
    updater.subscribe(state => states.push(state)); controllers.push(updater); return updater;
  };
  t.after(() => { for (const updater of controllers) updater.dispose(); fs.rmSync(directory, { recursive: true, force: true }); });
  const seed = () => {
    download.write({ version: 1, sourceVersion: "1.0.0", nextVersion: release.version, releaseId: release.releaseId });
    intent.write({ version: 1, phase: "installing", sourceVersion: "1.0.0", nextVersion: release.version,
      releaseId: release.releaseId, dataProfile: profile, packagePath });
  };
  const countdown = async () => {
    for (let i = 0; i < 10; i++) {
      const timer = [...timers].find(item => item.ms === 1000); assert.ok(timer, "countdown must be scheduled");
      timers.delete(timer); timer.fn(); await settle();
    }
  };
  return { directory, intentPath, downloadPath, packagePath, intent, download, calls, states, make, seed, countdown,
    paused: () => epoch !== 0 };
}

function failWrite(t, f, predicate) {
  const rename = fs.renameSync;
  return t.mock.method(fs, "renameSync", (source, destination) => {
    if (destination === f.intentPath && predicate(JSON.parse(fs.readFileSync(source, "utf8")))) {
      throw Object.assign(new Error(`fixture write failed: ${destination}`), { code: "EIO" });
    }
    return rename(source, destination);
  });
}
function failClear(t, filename, when = () => true) {
  const unlink = fs.unlinkSync;
  return t.mock.method(fs, "unlinkSync", target => {
    if (target === filename && when()) throw Object.assign(new Error(`fixture unlink failed: ${target}`), { code: "EACCES" });
    return unlink(target);
  });
}
async function begin(f, options) {
  const updater = f.make(options); await updater.check(); await updater.install(release.releaseId); await settle(); return updater;
}
function assertRetained(f) {
  assert.equal(fs.readFileSync(f.packagePath, "utf8"), "fake");
  assert.ok(!fs.readdirSync(f.directory).some(name => name.endsWith(".tmp")), "failed atomic writes must clean their temporary files");
  assert.ok(!f.states.some(state => state.phase === "installed"));
}

for (const phase of ["downloaded", "installing"]) {
  test(`${phase} record write failure preserves the pending intent and prevents shutdown`, async t => {
    const f = fixture(t);
    const fault = failWrite(t, f, record => phase === "downloaded" ? record.phase === "pending" && Boolean(record.packagePath) : record.phase === "installing");
    const updater = await begin(f);
    if (phase === "installing") { assert.equal(updater.getState().phase, "countdown"); await f.countdown(); }
    assert.equal(updater.getState().phase, "error"); assert.equal(updater.getState().reason, "update_record_write");
    assert.equal(f.intent.read().phase, "pending"); assert.equal(f.paused(), false);
    assert.ok(!f.calls.includes("stop")); assert.ok(!f.calls.includes("install"));
    assert.equal(f.calls.filter(call => call === "resume").length, phase === "installing" ? 1 : 0);
    if (phase === "downloaded") assert.ok(!f.calls.includes("prepare"));
    assertRetained(f); await updater.revealDownloaded();
    fault.mock.restore(); updater.dispose();
    const before = f.calls.length, reopened = f.make(); await reopened.start();
    assert.equal(reopened.getState().phase, "interrupted"); assert.equal(f.calls.length, before);
    await reopened.install(release.releaseId); await settle(); await f.countdown();
    assert.equal(f.calls.filter(call => call === "install").length, 1, "explicit retry can proceed after the write fault is removed");
  });
}

test("failure to persist repair retains the uncertain installing record across restart", async t => {
  const f = fixture(t); const fault = failWrite(t, f, record => record.phase === "repair");
  const updater = await begin(f, { installError: Object.assign(new Error("fixture handoff timeout"), { code: "native_handoff_unknown" }) });
  await f.countdown();
  assert.equal(updater.getState().phase, "repair"); assert.equal(updater.getState().reason, "update_record_write");
  assert.equal(f.intent.read().phase, "installing"); assertRetained(f);
  fault.mock.restore(); updater.dispose();
  const before = f.calls.length, reopened = f.make(); await reopened.start();
  assert.equal(reopened.getState().phase, "repair");
  await assert.rejects(reopened.install(release.releaseId), { code: "update_incomplete" });
  await assert.rejects(reopened.later(), { code: "update_incomplete" });
  assert.equal(f.calls.length, before); await reopened.revealDownloaded();
});

test("later cleanup failure keeps the pending record while restoring admission", async t => {
  const f = fixture(t), updater = await begin(f);
  assert.equal(updater.getState().phase, "countdown");
  const saved = f.intent.read(), fault = failClear(t, f.intentPath);
  await assert.rejects(updater.later(), { code: "update_record_write" });
  assert.equal(f.paused(), false); assert.deepEqual(f.intent.read(), saved);
  assert.ok(!f.calls.includes("stop")); assert.ok(!f.calls.includes("install")); assertRetained(f);
  await updater.revealDownloaded(); fault.mock.restore(); updater.dispose();
  const before = f.calls.length, reopened = f.make(); await reopened.start();
  assert.equal(reopened.getState().phase, "interrupted"); assert.equal(f.calls.length, before);
  await reopened.later(); assert.equal(f.intent.read(), null);
});

for (const record of ["download", "install"]) {
  test(`startup ${record} cleanup failure never reports success and can be retried on restart`, async t => {
    const f = fixture(t); f.seed(); const saved = f.intent.read();
    // The constructor may discard the old-version download reminder. Inject
    // this fault only after health verification, at successful-update cleanup.
    const fault = failClear(t, record === "download" ? f.downloadPath : f.intentPath, () => f.calls.includes("verify"));
    const updater = f.make({ version: release.version }); await updater.start();
    assert.equal(updater.getState().phase, "repair"); assert.deepEqual(f.intent.read(), saved); assertRetained(f);
    assert.ok(!f.calls.includes("install")); assert.ok(!f.calls.includes("download"));
    assert.deepEqual(f.calls, ["verify"]); assert.equal(f.download.read(), null);
    await updater.revealDownloaded(); fault.mock.restore(); updater.dispose();
    const reopened = f.make({ version: release.version }); await reopened.start();
    assert.equal(reopened.getState().phase, "installed"); assert.equal(f.intent.read(), null); assert.equal(f.download.read(), null);
    assert.equal(f.calls.filter(call => call === "verify").length, 2);
    assert.ok(!f.calls.includes("install")); assert.ok(!f.calls.includes("download"));
  });
}

for (const [name, local] of [
  ["dirty data", { dirty: true, dataProfile: profile }],
  ["different data fingerprint", { dirty: false, dataProfile: "different-profile" }],
]) {
  test(`startup rejects ${name} despite a matching release and healthy runtime`, async t => {
    const f = fixture(t); f.seed(); const saved = f.intent.read();
    const updater = f.make({ version: release.version, runtime: { async verifyStartup() {
      f.calls.push("verify"); return { configuration: { releaseId: release.releaseId, version: release.version }, local };
    } } });
    await updater.start();
    assert.equal(updater.getState().phase, "repair"); assert.equal(updater.getState().reason, "update_incomplete");
    assert.deepEqual(f.intent.read(), saved); assertRetained(f);
    assert.deepEqual(f.calls, ["verify"]);
    await assert.rejects(updater.install(release.releaseId), { code: "update_incomplete" });
    await assert.rejects(updater.later(), { code: "update_incomplete" });
    await updater.revealDownloaded(); updater.dispose();
    const healthy = f.make({ version: release.version }); await healthy.start();
    assert.equal(healthy.getState().phase, "installed"); assert.equal(f.intent.read(), null); assert.equal(f.download.read(), null);
    assert.ok(!f.calls.includes("install")); assert.ok(!f.calls.includes("download"));
  });
}
