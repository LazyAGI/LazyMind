#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { spawn } from "node:child_process";
import { commandRunner, isPortClosed, localGatewayURL } from "./runtime-smoke.mjs";

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const ACTIVE_RUNTIME_STATES = new Set(["ready", "running", "starting", "stale"]);

function waitForChildExit(child, timeoutMs) {
  if (!child?.once || child.exitCode != null || child.signalCode != null) {
    return Promise.resolve(true);
  }
  return new Promise((resolve) => {
    let settled = false;
    const finish = (exited) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      child.removeListener?.("exit", onExit);
      child.removeListener?.("close", onExit);
      resolve(exited);
    };
    const onExit = () => finish(true);
    const timer = setTimeout(() => finish(false), timeoutMs);
    child.once("exit", onExit);
    child.once("close", onExit);
  });
}

export async function terminatePackagedApp(child, options = {}) {
  if (!child?.kill) return;
  if (!child.once) {
    child.kill();
    return;
  }
  if (child.exitCode != null || child.signalCode != null) return;

  const gracefulExit = waitForChildExit(child, options.gracefulTimeoutMs ?? 2_000);
  child.kill("SIGTERM");
  if (await gracefulExit) return;

  const forcedExit = waitForChildExit(child, options.forceTimeoutMs ?? 5_000);
  child.kill("SIGKILL");
  if (!(await forcedExit)) {
    throw new Error("Packaged Desktop did not exit after SIGKILL");
  }
}

function ownedRuntimeArgs(command, state, options, runtimePaths) {
  return [
    command, "--profile", "desktop", "--owner-token", state.ownerToken,
    "--runtime-root", options.runtimeRoot, "--resources-root", runtimePaths.resourcesRoot,
    "--repo-root", runtimePaths.repoRoot,
  ];
}

function runtimeStatusStopped(status) {
  if (!status || ACTIVE_RUNTIME_STATES.has(status.overallStatus)) return false;
  return Object.values(status.services || {})
    .every((service) => !ACTIVE_RUNTIME_STATES.has(service?.status));
}

export function packagedRuntimePaths(appPath, platform = process.platform) {
  const platformPath = platform === "win32" ? path.win32 : path.posix;
  const resourcesRoot = platform === "darwin"
    ? platformPath.join(appPath, "Contents", "Resources", "runtime")
    : platformPath.join(platformPath.dirname(appPath), "resources", "runtime");
  return {
    resourcesRoot,
    repoRoot: platformPath.join(resourcesRoot, "app"),
    manager: platformPath.join(resourcesRoot, "bin", platform === "win32" ? "local-runtime-manager.exe" : "local-runtime-manager"),
  };
}

export function packagedExecutable(appPath, platform = process.platform) {
  return platform === "darwin" ? path.posix.join(appPath, "Contents", "MacOS", "LazyMind") : appPath;
}

export async function waitForPackagedRuntime(runtimeRoot, options = {}) {
  const readState = options.readState || (async () =>
    JSON.parse(await readFile(path.join(runtimeRoot, "state", "runtime-state.json"), "utf8")));
  const timeoutMs = options.timeoutMs ?? 180_000;
  const pollIntervalMs = options.pollIntervalMs ?? 1_000;
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() <= deadline) {
    try {
      const state = await readState();
      if (state.profile === "desktop" && state.overallStatus === "ready") return state;
      lastError = new Error(`runtime status is ${state.overallStatus || "unknown"}`);
    } catch (error) {
      lastError = error;
    }
    await delay(pollIntervalMs);
  }
  throw new Error(`packaged Desktop runtime did not become ready: ${lastError?.message || "timeout"}`);
}

export async function verifyPackagedAPI(state, request = globalThis.fetch) {
  const gateway = localGatewayURL(state);
  const sessionResponse = await request(`${gateway}/_local/admin-session`, { method: "POST" });
  if (!sessionResponse.ok) throw new Error(`admin session failed: HTTP ${sessionResponse.status}`);
  const payload = await sessionResponse.json();
  const token = payload?.data?.token || payload?.token;
  if (!token) throw new Error("admin session did not return a token");
  const healthResponse = await request(`${gateway}/api/core/health`, {
    headers: { authorization: `Bearer ${token}` },
  });
  if (!healthResponse.ok) throw new Error(`Core health failed: HTTP ${healthResponse.status}`);
  return gateway;
}

export async function runPackagedAppSmoke(options, dependencies = {}) {
  const runtimePaths = packagedRuntimePaths(options.app, options.platform);
  const launch = dependencies.launch || ((app) => spawn(app, [], {
    detached: Boolean(options.leaveRunning),
    stdio: "ignore",
  }));
  const child = launch(packagedExecutable(options.app, options.platform));
  let state;
  let verified = false;
  try {
    const readiness = waitForPackagedRuntime(options.runtimeRoot, {
      readState: dependencies.readState,
      timeoutMs: options.timeoutMs,
      pollIntervalMs: dependencies.pollIntervalMs,
    });
    const earlyExit = child?.once
      ? new Promise((_, reject) => {
        child.once("error", reject);
        child.once("exit", (code, signal) => reject(
          new Error(`packaged Desktop exited before readiness (code=${code}, signal=${signal || "none"})`),
        ));
      })
      : new Promise(() => {});
    state = await Promise.race([readiness, earlyExit]);
    const gateway = await verifyPackagedAPI(state, dependencies.fetch);
    verified = true;
    if (options.leaveRunning) child?.unref?.();
    return { gateway, state, runtimePaths };
  } finally {
    let gracefulShutdownError;
    try {
      if (state?.ownerToken && !(options.leaveRunning && verified)) {
        const run = dependencies.runManager || commandRunner(runtimePaths.manager, {
          env: {
            ...process.env,
            LAZYMIND_LOCAL_DOWN_TIMEOUT: "180s",
            LAZYMIND_PROCESS_COMPOSE_DOWN_TIMEOUT: "150s",
          },
          timeout: 190_000,
        });
        try {
          await run(ownedRuntimeArgs("down", state, options, runtimePaths));
        } catch (error) {
          gracefulShutdownError = error;
        }
      }
    } finally {
      if (!(options.leaveRunning && verified && state?.ownerToken)) {
        const terminate = dependencies.terminateApp || terminatePackagedApp;
        await terminate(child);
      }
    }

    if (state?.ownerToken && !(options.leaveRunning && verified) && gracefulShutdownError) {
      const cleanupRun = dependencies.runCleanupManager || commandRunner(runtimePaths.manager, {
        env: {
          ...process.env,
          LAZYMIND_LOCAL_DOWN_TIMEOUT: "60s",
          LAZYMIND_PROCESS_COMPOSE_DOWN_TIMEOUT: "45s",
        },
        timeout: 70_000,
      });
      let cleanupError;
      try {
        await (dependencies.cleanupDelay || delay)(1_000);
        await cleanupRun(ownedRuntimeArgs("down", state, options, runtimePaths));
      } catch (error) {
        cleanupError = error;
      }

      let stoppedStatus;
      let statusError;
      try {
        stoppedStatus = JSON.parse(await cleanupRun([
          ...ownedRuntimeArgs("status", state, options, runtimePaths), "--json",
        ]));
      } catch (error) {
        statusError = error;
      }
      const port = Number(state?.config?.localProxy?.port || state?.config?.localProxy?.Port || 0);
      const portClosed = dependencies.isPortClosed || isPortClosed;
      const gatewayStopped = !port || await portClosed(port);
      if (!gatewayStopped || !runtimeStatusStopped(stoppedStatus)) {
        const details = [
          `graceful shutdown: ${gracefulShutdownError.message}`,
          cleanupError ? `bounded cleanup: ${cleanupError.message}` : "bounded cleanup completed",
          statusError ? `status check: ${statusError.message}` : `runtime status: ${stoppedStatus?.overallStatus || "unknown"}`,
          `gateway port ${port || "unknown"}: ${gatewayStopped ? "closed" : "open"}`,
        ];
        throw new Error(`Packaged runtime cleanup could not be verified; ${details.join("; ")}`);
      }
      const warn = dependencies.warn || console.warn;
      warn(`::warning::Graceful packaged runtime shutdown failed, but bounded cleanup was verified: ${gracefulShutdownError.message}`);
    } else if (state?.ownerToken && !(options.leaveRunning && verified)) {
      const port = Number(state?.config?.localProxy?.port || state?.config?.localProxy?.Port || 0);
      const portClosed = dependencies.isPortClosed || isPortClosed;
      if (port && !(await portClosed(port))) throw new Error(`Local Proxy port ${port} remains open`);
    }
  }
}

function parseOptions(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) values[argv[index].replace(/^--/, "")] = argv[index + 1];
  return {
    app: values.app,
    runtimeRoot: values["runtime-root"],
    platform: values.platform,
    timeoutMs: values["timeout-ms"] ? Number(values["timeout-ms"]) : undefined,
    leaveRunning: values["leave-running"] === "true",
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  runPackagedAppSmoke(parseOptions(process.argv.slice(2)))
    .then(({ gateway }) => console.log(`packaged Desktop smoke passed at ${gateway}`))
    .catch((error) => { console.error(error); process.exitCode = 1; });
}
