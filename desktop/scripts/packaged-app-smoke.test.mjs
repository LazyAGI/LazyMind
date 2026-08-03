import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import {
  packagedExecutable,
  packagedRuntimePaths,
  runPackagedAppSmoke,
  terminatePackagedApp,
  waitForPackagedRuntime,
} from "./packaged-app-smoke.mjs";

test("resolves packaged runtime paths on macOS and Windows", () => {
  assert.equal(
    packagedExecutable("/Applications/LazyMind.app", "darwin"),
    "/Applications/LazyMind.app/Contents/MacOS/LazyMind",
  );
  assert.deepEqual(packagedRuntimePaths("/Applications/LazyMind.app", "darwin"), {
    resourcesRoot: "/Applications/LazyMind.app/Contents/Resources/runtime",
    repoRoot: "/Applications/LazyMind.app/Contents/Resources/runtime/app",
    manager: "/Applications/LazyMind.app/Contents/Resources/runtime/bin/local-runtime-manager",
  });
  assert.match(
    packagedRuntimePaths("C:\\Apps\\LazyMind\\LazyMind.exe", "win32").manager,
    /resources[\\/]runtime[\\/]bin[\\/]local-runtime-manager\.exe$/,
  );
});

test("waits through missing and starting state until Desktop is ready", async () => {
  const values = [new Error("missing"), { profile: "desktop", overallStatus: "starting" }, { profile: "desktop", overallStatus: "ready" }];
  const state = await waitForPackagedRuntime("/runtime", {
    pollIntervalMs: 0,
    timeoutMs: 100,
    readState: async () => {
      const value = values.shift();
      if (value instanceof Error) throw value;
      return value;
    },
  });
  assert.equal(state.overallStatus, "ready");
});

test("force kills a packaged app that stays resident after SIGTERM", async () => {
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  const signals = [];
  child.kill = (signal) => {
    signals.push(signal);
    if (signal === "SIGKILL") {
      child.signalCode = signal;
      queueMicrotask(() => child.emit("exit", null, signal));
    }
    return true;
  };

  await terminatePackagedApp(child, { gracefulTimeoutMs: 1, forceTimeoutMs: 100 });
  assert.deepEqual(signals, ["SIGTERM", "SIGKILL"]);
});

test("fails when a packaged app remains alive after SIGKILL", async () => {
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  child.kill = () => true;

  await assert.rejects(
    terminatePackagedApp(child, { gracefulTimeoutMs: 1, forceTimeoutMs: 1 }),
    /did not exit after SIGKILL/,
  );
});

test("launches a packaged app, verifies APIs, and performs owned shutdown", async () => {
  const calls = [];
  const state = { profile: "desktop", overallStatus: "ready", ownerToken: "owner-token", config: { localProxy: { port: 18090 } } };
  const result = await runPackagedAppSmoke({
    app: "/Applications/LazyMind.app", runtimeRoot: "/runtime", platform: "darwin",
  }, {
    launch: () => { calls.push("launch"); return { kill: () => calls.push("kill") }; },
    readState: async () => state,
    pollIntervalMs: 0,
    fetch: async (url, options = {}) => {
      calls.push([url, options]);
      return url.endsWith("admin-session")
        ? { ok: true, json: async () => ({ token: "token" }) }
        : { ok: true };
    },
    runManager: async (args) => calls.push(args),
    isPortClosed: async () => true,
  });
  assert.equal(result.gateway, "http://127.0.0.1:18090");
  assert.equal(calls[0], "launch");
  assert.ok(calls.some((call) => Array.isArray(call) && call[0] === "down"));
  assert.equal(calls.at(-1), "kill");
});

test("leaves a verified packaged app running for asynchronous workflow cleanup", async () => {
  const calls = [];
  const child = { unref: () => calls.push("unref"), kill: () => calls.push("kill") };
  const state = { profile: "desktop", overallStatus: "ready", ownerToken: "owner-token", config: { localProxy: { port: 18090 } } };
  const result = await runPackagedAppSmoke({
    app: "/Applications/LazyMind.app", runtimeRoot: "/runtime", platform: "darwin", leaveRunning: true,
  }, {
    launch: () => child,
    readState: async () => state,
    pollIntervalMs: 0,
    fetch: async (url) => url.endsWith("admin-session")
      ? { ok: true, json: async () => ({ token: "token" }) }
      : { ok: true },
    runManager: async () => calls.push("down"),
  });

  assert.equal(result.gateway, "http://127.0.0.1:18090");
  assert.deepEqual(calls, ["unref"]);
});

test("kills the packaged app when startup times out", async () => {
  let killed = false;
  await assert.rejects(
    runPackagedAppSmoke({ app: "/Applications/LazyMind.app", runtimeRoot: "/runtime", platform: "darwin", timeoutMs: 0 }, {
      launch: () => ({ kill: () => { killed = true; } }),
      readState: async () => { throw new Error("missing"); },
      pollIntervalMs: 0,
    }),
    /did not become ready/,
  );
  assert.equal(killed, true);
});

test("kills the packaged app when owned runtime shutdown fails", async () => {
  let killed = false;
  const state = { profile: "desktop", overallStatus: "ready", ownerToken: "owner-token", config: { localProxy: { port: 18090 } } };
  await assert.rejects(
    runPackagedAppSmoke({
      app: "/Applications/LazyMind.app", runtimeRoot: "/runtime", platform: "darwin",
    }, {
      launch: () => ({ kill: () => { killed = true; } }),
      readState: async () => state,
      pollIntervalMs: 0,
      fetch: async (url) => url.endsWith("admin-session")
        ? { ok: true, json: async () => ({ token: "token" }) }
        : { ok: true },
      runManager: async () => { throw new Error("shutdown timed out"); },
      runCleanupManager: async (args) => {
        if (args[0] === "status") {
          return JSON.stringify({ overallStatus: "failed", services: { "local-proxy": { status: "running" } } });
        }
        throw new Error("forced cleanup failed");
      },
      cleanupDelay: async () => {},
      isPortClosed: async () => false,
    }),
    /cleanup could not be verified/,
  );
  assert.equal(killed, true);
});

test("warns and succeeds when bounded cleanup verifies a failed graceful shutdown", async () => {
  let killed = false;
  const warnings = [];
  const cleanupCalls = [];
  const state = { profile: "desktop", overallStatus: "ready", ownerToken: "owner-token", config: { localProxy: { port: 18090 } } };
  const result = await runPackagedAppSmoke({
    app: "/Applications/LazyMind.app", runtimeRoot: "/runtime", platform: "darwin",
  }, {
    launch: () => ({ kill: () => { killed = true; } }),
    readState: async () => state,
    pollIntervalMs: 0,
    fetch: async (url) => url.endsWith("admin-session")
      ? { ok: true, json: async () => ({ token: "token" }) }
      : { ok: true },
    runManager: async () => { throw new Error("shutdown timed out"); },
    runCleanupManager: async (args) => {
      cleanupCalls.push(args[0]);
      return args[0] === "status"
        ? JSON.stringify({ overallStatus: "stopped", services: { "local-proxy": { status: "stopped" } } })
        : "";
    },
    cleanupDelay: async () => {},
    isPortClosed: async () => true,
    warn: (message) => warnings.push(message),
  });
  assert.equal(result.gateway, "http://127.0.0.1:18090");
  assert.equal(killed, true);
  assert.deepEqual(cleanupCalls, ["down", "status"]);
  assert.match(warnings[0], /^::warning::/);
});

test("fails immediately when the packaged application exits before readiness", async () => {
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  child.kill = () => {};
  const result = runPackagedAppSmoke({
    app: "/Applications/LazyMind.app", runtimeRoot: "/runtime", platform: "darwin", timeoutMs: 100,
  }, {
    launch: () => child,
    readState: async () => { throw new Error("missing"); },
    pollIntervalMs: 10,
  });
  child.exitCode = 0;
  child.emit("exit", 0, null);
  await assert.rejects(result, /exited before readiness/);
});
