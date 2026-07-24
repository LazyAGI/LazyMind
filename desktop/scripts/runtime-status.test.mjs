import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { runtimeExitFailureMessage } = require("../electron/src/runtime-status.js");

test("fails fast when a ready Desktop runtime has a stale owner", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "ready", ownerMatched: false },
    true,
    { code: 1 },
  );

  assert.match(message, /Another LazyMind Desktop instance owns/);
});

test("does not reject a ready runtime owned by this Desktop instance", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "ready", ownerMatched: true },
    true,
    { code: 0 },
  );

  assert.equal(message, "");
});

test("preserves the existing failure for an exited stopped runtime", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "stopped", services: { core: { status: "stopped" } } },
    true,
    { code: 1 },
  );

  assert.equal(message, "Runtime status is stopped; services: core:stopped");
});

test("ignores a stale failed status while the new runtime sidecar is still starting", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "failed", services: { core: { status: "failed" } } },
    true,
    null,
  );

  assert.equal(message, "");
});

test("reports a failed status after the runtime sidecar exits", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "failed", services: { core: { status: "failed" } } },
    true,
    { code: 1 },
  );

  assert.equal(message, "Runtime status is failed; services: core:failed");
});
