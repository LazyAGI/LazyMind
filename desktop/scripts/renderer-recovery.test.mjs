import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  waitForRendererWithRuntimeRecovery,
} = require("../electron/src/renderer-recovery.js");

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, reject, resolve };
}

function rendererAttempt(ready) {
  let pending = true;
  let disposed = 0;
  ready.promise.finally(() => {
    pending = false;
  }).catch(() => {});
  return {
    ready: ready.promise,
    isPending: () => pending,
    dispose: async () => {
      disposed += 1;
    },
    disposed: () => disposed,
  };
}

test("recreates a pending renderer once when the full runtime becomes ready", async () => {
  const runtime = deferred();
  const firstReady = deferred();
  const secondReady = deferred();
  const attempts = [rendererAttempt(firstReady), rendererAttempt(secondReady)];
  let starts = 0;
  const recoveryReasons = [];

  const resultPromise = waitForRendererWithRuntimeRecovery({
    startAttempt: async () => attempts[starts++],
    runtimeReady: runtime.promise,
    onRecovery: async (reason) => recoveryReasons.push(reason),
  });

  runtime.resolve({ overallStatus: "ready" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(starts, 2);
  assert.equal(attempts[0].disposed(), 1);
  assert.deepEqual(recoveryReasons, ["runtime-ready"]);

  secondReady.resolve();
  assert.equal(await resultPromise, attempts[1]);
  assert.equal(starts, 2);
});

test("does not recreate a renderer that becomes ready before the runtime", async () => {
  const runtime = deferred();
  const ready = deferred();
  const attempt = rendererAttempt(ready);
  let starts = 0;

  const resultPromise = waitForRendererWithRuntimeRecovery({
    startAttempt: async () => {
      starts += 1;
      return attempt;
    },
    runtimeReady: runtime.promise,
  });

  ready.resolve();
  assert.equal(await resultPromise, attempt);
  assert.equal(starts, 1);
  assert.equal(attempt.disposed(), 0);
  runtime.resolve({ overallStatus: "ready" });
});

test("waits for runtime readiness before replacing a failed first renderer", async () => {
  const runtime = deferred();
  const firstReady = deferred();
  const secondReady = deferred();
  const attempts = [rendererAttempt(firstReady), rendererAttempt(secondReady)];
  let starts = 0;

  const resultPromise = waitForRendererWithRuntimeRecovery({
    startAttempt: async () => attempts[starts++],
    runtimeReady: runtime.promise,
  });

  firstReady.reject(new Error("initial renderer failed"));
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(starts, 1);

  runtime.resolve({ overallStatus: "ready" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(starts, 2);
  secondReady.resolve();
  assert.equal(await resultPromise, attempts[1]);
});

test("never starts a third renderer when the recovery attempt fails", async () => {
  const runtime = deferred();
  const firstReady = deferred();
  const secondReady = deferred();
  const attempts = [rendererAttempt(firstReady), rendererAttempt(secondReady)];
  let starts = 0;

  const resultPromise = waitForRendererWithRuntimeRecovery({
    startAttempt: async () => attempts[starts++],
    runtimeReady: runtime.promise,
  });

  runtime.resolve({ overallStatus: "ready" });
  await new Promise((resolve) => setImmediate(resolve));
  secondReady.reject(new Error("recovery renderer failed"));

  await assert.rejects(resultPromise, /recovery renderer failed/);
  assert.equal(starts, 2);
});

test("does not recreate the renderer after the user closes the frontend", async () => {
  const runtime = deferred();
  const ready = deferred();
  const attempt = rendererAttempt(ready);
  let starts = 0;

  const resultPromise = waitForRendererWithRuntimeRecovery({
    startAttempt: async () => {
      starts += 1;
      return attempt;
    },
    runtimeReady: runtime.promise,
    shouldRecover: () => false,
  });

  runtime.resolve({ overallStatus: "ready" });
  assert.equal(await resultPromise, null);
  assert.equal(starts, 1);
  assert.equal(attempt.disposed(), 1);
});
