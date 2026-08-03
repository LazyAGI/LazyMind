import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import {
  assertReadyStatus,
  commandRunner,
  localGatewayURL,
  runRuntimeSmoke,
  runtimeArgs,
} from "./runtime-smoke.mjs";

test("command runner enforces a hard timeout without waiting for close", async () => {
  const child = new EventEmitter();
  child.stdout = new EventEmitter();
  child.stderr = new EventEmitter();
  child.stdout.destroy = () => { child.stdout.destroyed = true; };
  child.stderr.destroy = () => { child.stderr.destroyed = true; };
  child.kill = (signal) => { child.killedWith = signal; };
  child.unref = () => { child.unrefed = true; };
  const run = commandRunner("manager", { timeout: 1 }, () => child);

  await assert.rejects(run(["down"]), /timed out after 1ms/);
  assert.equal(child.killedWith, "SIGKILL");
  assert.equal(child.stdout.destroyed, true);
  assert.equal(child.stderr.destroyed, true);
  assert.equal(child.unrefed, true);
});

function readyStatus(profile) {
  return {
    profile,
    overallStatus: "ready",
    config: { localProxy: { port: 18090 } },
    services: Object.fromEntries(
      ["local-proxy", "auth-service", "core", "frontend"].map((name) => [name, { status: "running" }]),
    ),
  };
}

test("builds the same runtime command contract for local and desktop profiles", () => {
  const common = { runtimeRoot: "/tmp/lazymind smoke", repoRoot: "/repo" };
  assert.deepEqual(runtimeArgs({ ...common, profile: "local" }, "up"), [
    "up", "--profile", "local", "--runtime-root", common.runtimeRoot, "--repo-root", "/repo",
  ]);
  assert.deepEqual(runtimeArgs({ ...common, profile: "desktop", ownerToken: "owner" }, "up"), [
    "up", "--profile", "desktop", "--runtime-root", common.runtimeRoot,
    "--repo-root", "/repo", "--owner-token", "owner",
  ]);
});

test("validates the shared minimum service graph", () => {
  for (const profile of ["local", "desktop"]) {
    const status = readyStatus(profile);
    assert.doesNotThrow(() => assertReadyStatus(status, profile));
    assert.equal(localGatewayURL(status), "http://127.0.0.1:18090");
  }
  const broken = readyStatus("local");
  broken.services.core.status = "failed";
  assert.throws(() => assertReadyStatus(broken, "local"), /core is not ready/);
  assert.equal(
    localGatewayURL({ config: { localProxy: { Port: 18091 } } }),
    "http://127.0.0.1:18091",
  );
});

for (const profile of ["local", "desktop"]) {
  test(`runs the complete ${profile} smoke sequence and shuts down`, async () => {
    const calls = [];
    const status = readyStatus(profile);
    const run = async (args) => {
      calls.push(args[0]);
      return args[0] === "status" ? JSON.stringify(status) : "";
    };
    const fetch = async (url, options = {}) => {
      calls.push([url, options]);
      return url.endsWith("admin-session")
        ? { ok: true, json: async () => ({ data: { token: "local-token" } }) }
        : { ok: true };
    };
    const result = await runRuntimeSmoke({
      manager: "manager",
      profile,
      runtimeRoot: "/tmp/runtime",
      ownerToken: profile === "desktop" ? "owner" : undefined,
    }, { start: (args) => { calls.push(args[0]); return null; }, run, fetch, isPortClosed: async () => true });

    assert.equal(result.profile, profile);
    assert.deepEqual(calls.filter((call) => typeof call === "string"), ["up", "status", "down"]);
    assert.equal(calls[3][1].headers.authorization, "Bearer local-token");
  });
}

test("always stops a started runtime when an API assertion fails", async () => {
  const commands = [];
  const run = async (args) => {
    commands.push(args[0]);
    return args[0] === "status" ? JSON.stringify(readyStatus("local")) : "";
  };
  await assert.rejects(
    runRuntimeSmoke(
      { manager: "manager", profile: "local", runtimeRoot: "/tmp/runtime" },
      {
        start: (args) => { commands.push(args[0]); return null; },
        run,
        fetch: async () => ({ ok: false, status: 500 }),
        isPortClosed: async () => true,
      },
    ),
    /admin session failed/,
  );
  assert.deepEqual(commands, ["up", "status", "down"]);
});

test("rejects desktop smoke without an ownership token", async () => {
  await assert.rejects(
    runRuntimeSmoke({ manager: "manager", profile: "desktop", runtimeRoot: "/tmp/runtime" }),
    /ownerToken/,
  );
});
