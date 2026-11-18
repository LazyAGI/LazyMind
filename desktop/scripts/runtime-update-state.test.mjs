import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
const { readRuntimeUpdateState } = createRequire(import.meta.url)("../electron/src/runtime-update-state.js");

test("checks request a fresh read-only runtime measurement and add actual OS version", async () => {
  const calls = [];
  let profile = "sqlite-v1-fixture";
  const invoke = async (...args) => { calls.push(args); return JSON.stringify({ dataProfile: profile, dirty: false, components: { rag: "fixture-revision" } }); };
  assert.deepEqual(await readRuntimeUpdateState(invoke, "14.7"), { systemVersion: "14.7.0", dataProfile: profile, dirty: false, components: { rag: "fixture-revision" } });
  profile = "sqlite-v1-changed";
  assert.equal((await readRuntimeUpdateState(invoke, "14.7")).dataProfile, profile);
  assert.equal(calls.length, 2);
  assert.equal(calls[0][0], "update-state");
  assert.ok(calls[0][2].timeout > 0 && calls[0][2].timeout <= 30000);
});

test("unknown or malformed local state never falls back to app version or cached status", async () => {
  for (const output of ["{broken", "{}", JSON.stringify({dataProfile:"x",dirty:false,components:[]}), "x".repeat(1024*1024+1)]) {
    await assert.rejects(readRuntimeUpdateState(async () => output, "14.7.0"), e => e.code === "data_unknown");
  }
  await assert.rejects(readRuntimeUpdateState(async () => { throw new Error("PRIVATE path and token"); }, "14.7.0"), e => e.code === "data_unknown" && !e.message.includes("PRIVATE"));
  const dirty = await readRuntimeUpdateState(async () => JSON.stringify({dataProfile:"",dirty:true,components:{}}), "10.0.1");
  assert.equal(dirty.dirty, true); assert.equal(dirty.dataProfile, "");
});

test("actual Desktop update entry uses fresh runtime measurement", () => {
  const main = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  const entry = main.slice(main.indexOf("const desktopUpdater = createDesktopUpdater("), main.indexOf("registerUpdateIPC({"));
  assert.match(entry, /readRuntimeUpdateState\(runSidecar, process\.getSystemVersion\(\)\)/);
  assert.doesNotMatch(entry, /currentStatus\?\.updateState/);
});
