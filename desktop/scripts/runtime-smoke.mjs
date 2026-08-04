#!/usr/bin/env node

import { spawn } from "node:child_process";
import net from "node:net";
import process from "node:process";

const READY_STATES = new Set(["ready", "running"]);

export function runtimeArgs(options, command) {
  const args = [command, "--profile", options.profile, "--runtime-root", options.runtimeRoot];
  if (options.repoRoot) args.push("--repo-root", options.repoRoot);
  if (options.resourcesRoot) args.push("--resources-root", options.resourcesRoot);
  if (options.ownerToken) args.push("--owner-token", options.ownerToken);
  return args;
}

export function localGatewayURL(status) {
  const port = Number(status?.config?.localProxy?.port || status?.config?.localProxy?.Port || 0);
  if (!Number.isInteger(port) || port <= 0) {
    throw new Error("runtime status does not contain a valid Local Proxy port");
  }
  return `http://127.0.0.1:${port}`;
}

export function assertReadyStatus(status, profile) {
  if (status?.profile !== profile) {
    throw new Error(`runtime profile mismatch: got ${status?.profile}, want ${profile}`);
  }
  if (!READY_STATES.has(status?.overallStatus)) {
    throw new Error(`runtime is not ready: ${status?.overallStatus || "unknown"}`);
  }
  for (const name of ["local-proxy", "auth-service", "core", "frontend"]) {
    if (!READY_STATES.has(status?.services?.[name]?.status)) {
      throw new Error(`required service ${name} is not ready`);
    }
  }
}

export function commandRunner(executable, spawnOptions = {}, spawnProcess = spawn) {
  return (args) =>
    new Promise((resolve, reject) => {
      const { timeout, ...childOptions } = spawnOptions;
      const child = spawnProcess(executable, args, {
        ...childOptions,
        stdio: ["ignore", "pipe", "pipe"],
      });
      let stdout = "";
      let stderr = "";
      let settled = false;
      let timeoutTimer;
      const finish = (callback, value) => {
        if (settled) return;
        settled = true;
        if (timeoutTimer) clearTimeout(timeoutTimer);
        callback(value);
      };
      child.stdout.on("data", (chunk) => { stdout += chunk; });
      child.stderr.on("data", (chunk) => { stderr += chunk; });
      child.once("error", (error) => finish(reject, error));
      child.once("close", (code, signal) => {
        if (code === 0) finish(resolve, stdout);
        else finish(reject, new Error(`${executable} ${args[0]} failed (code=${code}, signal=${signal || "none"}): ${stderr || stdout}`));
      });
      if (Number.isFinite(timeout) && timeout > 0) {
        timeoutTimer = setTimeout(() => {
          if (settled) return;
          child.kill?.("SIGKILL");
          child.stdout?.destroy?.();
          child.stderr?.destroy?.();
          child.unref?.();
          finish(reject, new Error(`${executable} ${args[0]} timed out after ${timeout}ms: ${stderr || stdout}`));
        }, timeout);
      }
    });
}

export function commandStarter(executable) {
  return (args) => spawn(executable, args, { stdio: ["ignore", "pipe", "pipe"] });
}

export async function waitForReadyRuntime(options, run, runtimeProcess) {
  const deadline = Date.now() + (options.timeoutMs ?? 180_000);
  let lastError;
  let processExit;
  runtimeProcess?.once?.("error", (error) => { processExit = error; });
  runtimeProcess?.once?.("exit", (code, signal) => {
    processExit = new Error(`runtime supervisor exited before readiness (code=${code}, signal=${signal || "none"})`);
  });
  while (Date.now() <= deadline) {
    if (processExit) throw processExit;
    try {
      const status = JSON.parse(await run([...runtimeArgs(options, "status"), "--json"]));
      assertReadyStatus(status, options.profile);
      return status;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, options.pollIntervalMs ?? 1_000));
  }
  throw new Error(`runtime did not become ready: ${lastError?.message || "timeout"}`);
}

export function isPortClosed(port, host = "127.0.0.1") {
  return new Promise((resolve) => {
    const socket = net.createConnection({ port, host });
    socket.setTimeout(500);
    socket.once("connect", () => { socket.destroy(); resolve(false); });
    socket.once("timeout", () => { socket.destroy(); resolve(true); });
    socket.once("error", () => resolve(true));
  });
}

export async function runRuntimeSmoke(options, dependencies = {}) {
  if (!["local", "desktop"].includes(options.profile)) {
    throw new Error("profile must be local or desktop");
  }
  if (!options.runtimeRoot) throw new Error("runtimeRoot is required");
  if (options.profile === "desktop" && !options.ownerToken) {
    throw new Error("desktop smoke requires an ownerToken");
  }

  const run = dependencies.run || commandRunner(options.manager);
  const start = dependencies.start || (dependencies.run
    ? (args) => { void dependencies.run(args); return null; }
    : commandStarter(options.manager));
  const request = dependencies.fetch || globalThis.fetch;
  const portClosed = dependencies.isPortClosed || isPortClosed;
  let gatewayPort = 0;
  let started = false;
  let runtimeProcess;

  try {
    runtimeProcess = start(runtimeArgs(options, "up"));
    started = true;
    const status = await waitForReadyRuntime(options, run, runtimeProcess);
    const gateway = localGatewayURL(status);
    gatewayPort = Number(new URL(gateway).port);

    const sessionResponse = await request(`${gateway}/_local/admin-session`, { method: "POST" });
    if (!sessionResponse.ok) throw new Error(`admin session failed: HTTP ${sessionResponse.status}`);
    const sessionPayload = await sessionResponse.json();
    const token = sessionPayload?.data?.token || sessionPayload?.token;
    if (!token) throw new Error("admin session did not return a token");

    const coreResponse = await request(`${gateway}/api/core/health`, {
      headers: { authorization: `Bearer ${token}` },
    });
    if (!coreResponse.ok) throw new Error(`Core health failed: HTTP ${coreResponse.status}`);
    return { profile: options.profile, gateway, status };
  } finally {
    if (started) await run(runtimeArgs(options, "down"));
    runtimeProcess?.kill?.();
    if (gatewayPort && !(await portClosed(gatewayPort))) {
      throw new Error(`Local Proxy port ${gatewayPort} remains open after shutdown`);
    }
  }
}

function parseOptions(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index]?.replace(/^--/, "");
    values[key] = argv[index + 1];
  }
  return {
    manager: values.manager,
    profile: values.profile,
    runtimeRoot: values["runtime-root"],
    repoRoot: values["repo-root"],
    resourcesRoot: values["resources-root"],
    ownerToken: values["owner-token"],
  };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  runRuntimeSmoke(parseOptions(process.argv.slice(2)))
    .then(({ profile, gateway }) => console.log(`${profile} runtime smoke passed at ${gateway}`))
    .catch((error) => { console.error(error); process.exitCode = 1; });
}
