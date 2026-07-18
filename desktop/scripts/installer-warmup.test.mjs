import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const {
  assertMaintenanceRuntimeStopped,
  runInstallerWarmupLifecycle,
} = require("../electron/src/installer-warmup.js");

function readyStatus() {
  return {
    overallStatus: "ready",
    ownerMatched: true,
    config: { frontendPort: 8090 },
    services: { frontend: { status: "running" } },
  };
}

function stoppedStatus() {
  return {
    overallStatus: "stopped",
    ownerMatched: true,
    config: { frontendPort: 8090 },
    services: {
      frontend: { status: "stopped" },
      "process-supervisor": { status: "stopped" },
    },
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

test("installer warmup waits for verified runtime shutdown before disposing its renderer", async () => {
  const events = [];
  const shutdown = deferred();
  let statusReads = 0;
  const run = runInstallerWarmupLifecycle({
    startRuntime: async () => events.push("start"),
    readStatus: async () => (++statusReads === 1 ? readyStatus() : stoppedStatus()),
    createRenderer: () => ({ id: "renderer" }),
    loadRenderer: async () => events.push("render"),
    stopRuntime: async () => {
      events.push("stop-started");
      await shutdown.promise;
      events.push("stop-finished");
    },
    disposeRenderer: () => events.push("disposed"),
    log: () => {},
  });

  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(events, ["start", "render", "stop-started"]);
  shutdown.resolve();
  await run;
  assert.deepEqual(events, ["start", "render", "stop-started", "stop-finished", "disposed"]);
});

test("installer warmup still cleans up after renderer failure", async () => {
  const events = [];
  let statusReads = 0;
  await assert.rejects(runInstallerWarmupLifecycle({
    startRuntime: async () => events.push("start"),
    readStatus: async () => (++statusReads === 1 ? readyStatus() : stoppedStatus()),
    createRenderer: () => ({ id: "renderer" }),
    loadRenderer: async () => {
      events.push("render");
      throw new Error("renderer failed");
    },
    stopRuntime: async () => events.push("stop"),
    disposeRenderer: () => events.push("disposed"),
    log: () => {},
  }), /renderer failed/);
  assert.deepEqual(events, ["start", "render", "stop", "disposed"]);
});

test("installer warmup fails when shutdown cannot be verified", async () => {
  let statusReads = 0;
  await assert.rejects(runInstallerWarmupLifecycle({
    startRuntime: async () => {},
    readStatus: async () => (++statusReads === 1 ? readyStatus() : {
      ...stoppedStatus(),
      overallStatus: "ready",
      services: { frontend: { status: "running" } },
    }),
    createRenderer: () => ({}),
    loadRenderer: async () => {},
    stopRuntime: async () => {},
    disposeRenderer: () => {},
    log: () => {},
  }), /did not stop cleanly/);
});

test("maintenance stopped verification rejects mismatched ownership", () => {
  assert.throws(
    () => assertMaintenanceRuntimeStopped({ ...stoppedStatus(), ownerMatched: false }),
    /ownerMatched=false/,
  );
});
