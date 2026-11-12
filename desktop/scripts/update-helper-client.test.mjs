import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createRequire } from "node:module";
import { PassThrough } from "node:stream";
import test from "node:test";
const require = createRequire(import.meta.url);
const { createUpdateHelperClient } = require("../electron/src/update-helper-client.js");

function fixture(options = {}) {
  const child = new EventEmitter(); child.stdin = new PassThrough(); child.stdout = new PassThrough(); child.stderr = new PassThrough();
  const kills = []; child.kill = (signal) => { kills.push(signal); return true; };
  const calls = [];
  const invoke = createUpdateHelperClient({ helperPath: "/fixture/runtime/bin/update-helper", spawn: (...args) => { calls.push(args); return child; }, ...options });
  return { child, calls, kills, invoke };
}

test("helper client uses a fixed executable and command, passes bounded JSON via stdin, and streams progress", async () => {
  const f = fixture(); const events = []; let input = ""; f.child.stdin.on("data", (chunk) => { input += chunk; });
  const pending = f.invoke("download", { releaseId: "r2", state: { dataProfile: "known-v1" } }, { onProgress: (event) => events.push(event) });
  assert.equal(f.calls[0][0], "/fixture/runtime/bin/update-helper");
  assert.deepEqual(f.calls[0][1], ["download"]); assert.equal(f.calls[0][2].shell, false);
  assert.equal(JSON.parse(input).releaseId, "r2");
  f.child.stdout.write('{"status":"downloading","downloadedBytes":1,"totalBytes":2}\n{"status":"down');
  f.child.stdout.write('loaded","releaseId":"r2","packagePath":"/fixture/cache.zip"}\n');
  f.child.emit("close", 0);
  assert.equal((await pending).status, "downloaded"); assert.equal(events.length, 1);
});

test("helper failure remains a failure even when it emitted a JSON result", async () => {
  const f = fixture(); const pending = f.invoke("discover", {});
  f.child.stdout.write('{"status":"check_failed","reason":"policy_replay"}\n'); f.child.emit("close", 1);
  await assert.rejects(pending, (error) => error.code === "policy_replay");
  const broken = fixture(); const empty = broken.invoke("config", {}); broken.child.emit("close", 0);
  await assert.rejects(empty);
});

test("cancellation first uses the helper control stream so Windows can clean partial files", async () => {
  const f = fixture(); const controller = new AbortController();
  let input = ""; f.child.stdin.on("data", (chunk) => { input += chunk; });
  const pending = f.invoke("download", {}, { signal: controller.signal });
  let settled = false; void pending.then(() => { settled = true; }, () => { settled = true; });
  controller.abort(); await new Promise((resolve) => setImmediate(resolve));
  assert.equal(settled, false, "cancel cannot report completion before the helper exits");
  f.child.stdout.write('{"status":"check_failed","reason":"cancelled"}\n'); f.child.emit("close", 1);
  await assert.rejects(pending, (error) => error.name === "AbortError" || error.code === "cancelled");
  const messages = input.trim().split("\n").map((line) => JSON.parse(line));
  assert.deepEqual(messages.at(-1), { cancel: true });
  assert.equal(f.kills.length, 0, "graceful completion must not force-kill the helper");
  await assert.rejects(async () => f.invoke("force-stop", {}));
  assert.equal(f.calls.length, 1);
});

test("an unresponsive cancelled helper is killed only after the cleanup deadline and still awaited", async () => {
  const timers = [];
  const f = fixture({ clock: { setTimeout: (fn, delay) => { const timer = { fn, delay, unref() {} }; timers.push(timer); return timer; }, clearTimeout: (timer) => { timer.cleared = true; } } });
  const controller = new AbortController(); let settled = false;
  const pending = f.invoke("download", {}, { signal: controller.signal });
  void pending.then(() => { settled = true; }, () => { settled = true; });
  controller.abort(); await new Promise((resolve) => setImmediate(resolve));
  assert.equal(f.kills.length, 0); assert.equal(settled, false);
  const cleanup = timers.find((timer) => timer.delay === 5000); assert.ok(cleanup, "a bounded five-second cleanup deadline is required");
  cleanup.fn(); assert.deepEqual(f.kills, ["SIGKILL"]);
  await new Promise((resolve) => setImmediate(resolve)); assert.equal(settled, false);
  f.child.emit("close", null, "SIGKILL"); await assert.rejects(pending);
  assert.equal(f.kills.length, 1);
});
