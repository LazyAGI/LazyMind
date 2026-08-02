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
  const port = Number(status?.config?.localProxy?.port || 0);
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

export function commandRunner(executable) {
  return (args) =>
    new Promise((resolve, reject) => {
      const child = spawn(executable, args, { stdio: ["ignore", "pipe", "pipe"] });
      let stdout = "";
      let stderr = "";
      child.stdout.on("data", (chunk) => { stdout += chunk; });
      child.stderr.on("data", (chunk) => { stderr += chunk; });
      child.once("error", reject);
      child.once("close", (code) => {
        if (code === 0) resolve(stdout);
        else reject(new Error(`${executable} ${args[0]} failed (${code}): ${stderr || stdout}`));
      });
    });
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
  const request = dependencies.fetch || globalThis.fetch;
  const portClosed = dependencies.isPortClosed || isPortClosed;
  let gatewayPort = 0;
  let started = false;

  try {
    await run(runtimeArgs(options, "up"));
    started = true;
    const statusText = await run([...runtimeArgs(options, "status"), "--json"]);
    const status = JSON.parse(statusText);
    assertReadyStatus(status, options.profile);
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
