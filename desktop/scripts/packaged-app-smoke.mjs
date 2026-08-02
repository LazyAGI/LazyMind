#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { spawn } from "node:child_process";
import { commandRunner, isPortClosed, localGatewayURL } from "./runtime-smoke.mjs";

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

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
  const launch = dependencies.launch || ((app) => spawn(app, [], { detached: false, stdio: "ignore" }));
  const child = launch(packagedExecutable(options.app, options.platform));
  let state;
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
    return { gateway, state, runtimePaths };
  } finally {
    if (state?.ownerToken) {
      const run = dependencies.runManager || commandRunner(runtimePaths.manager);
      await run([
        "down", "--profile", "desktop", "--owner-token", state.ownerToken,
        "--runtime-root", options.runtimeRoot, "--resources-root", runtimePaths.resourcesRoot,
        "--repo-root", runtimePaths.repoRoot,
      ]);
      const port = Number(state?.config?.localProxy?.port || state?.config?.localProxy?.Port || 0);
      const portClosed = dependencies.isPortClosed || isPortClosed;
      if (port && !(await portClosed(port))) throw new Error(`Local Proxy port ${port} remains open`);
    }
    child?.kill?.();
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
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  runPackagedAppSmoke(parseOptions(process.argv.slice(2)))
    .then(({ gateway }) => console.log(`packaged Desktop smoke passed at ${gateway}`))
    .catch((error) => { console.error(error); process.exitCode = 1; });
}
