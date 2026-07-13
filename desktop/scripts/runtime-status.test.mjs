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
