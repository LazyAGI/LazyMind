import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";

test("a slow verified backup can finish before the source app quits", async t => {
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "handoff-ack-")));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const profile = { application: path.join(root, "LazyMind.app"), updater: path.join(root, "updater") };
  const resources = path.join(profile.application, "Contents", "Resources");
  const config = { mode: "unsigned-test", platform: "darwin", arch: "arm64", updaterProtocol: 2,
    channel: "personal-test", audience: "test", signingIdentity: "adhoc", version: "1.0.0",
    trustDomain: "inplace-1", releaseId: "A" };
  fs.mkdirSync(path.join(resources, "runtime", "bin"), { recursive: true });
  fs.writeFileSync(path.join(resources, "runtime", "bin", "unsigned-update-helper"), "fixture");
  fs.writeFileSync(path.join(resources, "app-update.yml"), JSON.stringify({
    lazymind: { ...config, protocol: 2 }, updaterCacheDirName: "LazyMind-inplace-1-test-personal-test",
  }));
  const child = new EventEmitter();
  child.stdout = Object.assign(new EventEmitter(), { destroy() {} });
  let killed = false, quit = false, timer, budget;
  child.kill = () => { killed = true; };
  child.unref = () => {};
  const file = new URL("../electron/src/unsigned-test-installer.js", import.meta.url);
  const load = createRequire(file);
  const ctx = { module: { exports: {} }, require: name => {
    if (name === "node:child_process") return { execFile() {}, spawn: () => child };
    if (name === "node:util") return { promisify: () => async () => ({ stdout: JSON.stringify({
      status: "prepared", id: "1".repeat(32), releaseId: "B",
    }) }) };
    return load(name);
  }, setTimeout: (fn, ms) => { timer = fn; budget = ms; }, clearTimeout() {} };
  vm.runInNewContext(fs.readFileSync(file, "utf8"), ctx);
  const installer = ctx.module.exports.createUnsignedTestInstaller({ profile, currentVersion: config.version,
    resourcesPath: resources, setQuitting() {}, quit() { quit = true; } });
  const stage = await installer.prepare({ configuration: config, release: { releaseId: "B" }, receipt: { releaseId: "B" } });
  const outcome = stage.install().then(value => value, error => error);
  if (budget <= 45_000) timer();
  assert.equal(killed, false, "source killed its helper during a normal 45-second preparation");
  assert.equal(quit, false);
  assert.ok(budget >= 10 * 60_000, "allow the native helper's bounded preparation");
  child.stdout.emit("data", '{"status":"accepted"}\n');
  assert.equal((await outcome).handoff, true);
  assert.equal(quit, true);
});
