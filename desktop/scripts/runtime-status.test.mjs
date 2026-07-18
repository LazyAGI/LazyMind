import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  desktopRuntimeReady,
  requiredDesktopServices,
  runtimeExitFailureMessage,
} = require("../electron/src/runtime-status.js");

function readyDesktopStatus(overrides = {}) {
  return {
    overallStatus: "ready",
    ownerMatched: true,
    config: { frontendPort: 8080, modeProfile: { VectorStore: { ManagedProcess: false } } },
    services: Object.fromEntries(requiredDesktopServices.map((name) => [name, { status: "running" }])),
    ...overrides,
  };
}

test("opens Desktop only after every required service is ready", () => {
  assert.equal(desktopRuntimeReady(readyDesktopStatus(), true), true);

  const status = readyDesktopStatus();
  status.services["file-watcher"] = { status: "starting" };
  assert.equal(desktopRuntimeReady(status, true), false);
});

test("requires a managed Milvus process to be ready", () => {
  const status = readyDesktopStatus({
    config: { frontendPort: 8080, modeProfile: { VectorStore: { ManagedProcess: true } } },
  });
  assert.equal(desktopRuntimeReady(status, true), false);

  status.services["milvus-lite"] = { status: "running" };
  assert.equal(desktopRuntimeReady(status, true), true);
});

test("rejects a complete runtime that is not owned by this Desktop", () => {
  assert.equal(desktopRuntimeReady(readyDesktopStatus(), false), false);
  assert.equal(desktopRuntimeReady(readyDesktopStatus({ ownerMatched: false }), true), false);
});

test("fails fast when a ready Desktop runtime has a stale owner", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "ready", ownerMatched: false },
    true,
    { code: 1 },
  );

  assert.match(message, /Another LazyMind Desktop instance owns/);
});

test("preserves a concrete sidecar failure instead of masking it with stale ownership", () => {
  const message = runtimeExitFailureMessage(
    { overallStatus: "ready", ownerMatched: false },
    true,
    {
      code: 1,
      detail: "replace relocatable desktop Python executable: the file is being used by another process",
    },
  );

  assert.match(message, /file is being used by another process/);
  assert.doesNotMatch(message, /Another LazyMind Desktop instance owns/);
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
