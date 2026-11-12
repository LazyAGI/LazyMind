import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import vm from "node:vm";

const require = createRequire(import.meta.url);
const { createUpdateRuntimeClient } = require("../electron/src/update-runtime-client.js");
const source = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");

function fixture() {
  const children = [], handlers = new Map(), timers = new Map();
  let failSpawn = false;
  const context = vm.createContext({
    URL, process: { env: {} }, isWindows: false, isQuitting: false,
    agentConnectorPath: "/fixture/connector", sidecarEnv: () => ({}),
    ipcMain: { handle: (name, handler) => handlers.set(name, handler) },
    appendStartupLog() {}, serializeError: error => error.message, restartAgentHost() {},
    setTimeout: (fn, delay) => { const timer = {}; timers.set(timer, { fn, delay }); return timer; },
    clearTimeout: timer => timers.delete(timer),
    spawn() {
      if (failSpawn) throw new Error("fixture spawn failed");
      const child = Object.assign(new EventEmitter(), {
        pid: 4200 + children.length, exitCode: null, signalCode: null,
        stdout: new EventEmitter(), stderr: new EventEmitter(),
        kill() { this.killed = true; },
      });
      children.push(child);
      return child;
    },
    runConnectorJSON: async () => ({ state: "enabled", agent: "codex" }),
    createUpdateRuntimeClient,
    runSidecar: async (_command, args) => {
      const action = args[args.indexOf("--action") + 1];
      const epoch = Number(args[args.indexOf("--epoch") + 1]);
      return JSON.stringify({ active: 0, paused: action === "pause", epoch: action === "pause" ? epoch : 0 });
    },
    runtimeRestartPromise: null, inputRecorder: { isActive: () => false },
    nativeRecording: null, browserRecorders: new Set(),
  });
  const declarations = source.match(/^const agent(?:Connector\w+TimeoutMs|Logins) = .+;$/gm).join("\n");
  const admissions = source.slice(source.indexOf("function assertDesktopUpdateAdmission("), source.indexOf("function stopDesktopUpdateChild("));
  const agents = source.slice(source.indexOf("function runAgentConnector("), source.indexOf("\nfunction readAgentBindings("));
  const runtime = source.slice(source.indexOf("const updateRuntime = createUpdateRuntimeClient("), source.indexOf("\nconst desktopUpdater ="));
  vm.runInContext(
    "let desktopUpdateAdmission = true, desktopUpdateWork = 0;\n" +
    declarations + "\n" + admissions + "\n" + agents + "\n" + runtime + "\n" +
    'handleDesktopWork("fixture:login", (_event, agent) => runAgentConnector(agent, "login"));',
    context,
  );
  return {
    children, timers,
    start: () => handlers.get("fixture:login")({}, "codex"),
    bind: () => context.runAgentBinding("codex-cli", "set", "/fixture/codex"),
    active: async () => (await vm.runInContext("updateRuntime.status()", context)).active,
    pause: () => vm.runInContext("updateRuntime.pause(1)", context),
    failSpawn: () => { failSpawn = true; },
  };
}

function close(child) {
  child.stdout.emit("data", JSON.stringify({ state: "ready" }));
  child.exitCode = 0;
  child.emit("exit", 0, null);
  child.emit("close", 0, null);
}

test("update waits for login child after the status IPC has returned", async () => {
  const f = fixture();
  await f.start();
  assert.equal(await f.active(), 1, "the browser authorization is still running");
  close(f.children[0]);
  assert.equal(await f.active(), 0);
});

test("login exit does not finish update work before stdio close", async () => {
  const f = fixture();
  await f.start();
  f.children[0].exitCode = 0;
  f.children[0].emit("exit", 0, null);
  assert.equal(await f.active(), 1);
  close(f.children[0]);
  assert.equal(await f.active(), 0);
});

test("replaced login remains counted until its own child closes", async () => {
  const f = fixture();
  await f.start();
  await f.start();
  assert.equal(f.children[0].killed, true);
  assert.equal(await f.active(), 2);
  close(f.children[0]);
  assert.equal(await f.active(), 1, "old completion cannot release the replacement");
  close(f.children[1]);
  assert.equal(await f.active(), 0);
});

test("clearing the login map does not hide a child still terminating", async () => {
  const f = fixture();
  await f.start();
  await f.bind();
  assert.equal(f.children[0].killed, true);
  assert.equal(await f.active(), 1);
  close(f.children[0]);
  assert.equal(await f.active(), 0);
});

test("login timeout requests termination but update still waits for close", async () => {
  const f = fixture();
  await f.start();
  const timeout = [...f.timers.values()].find(timer => timer.delay === 120_000);
  assert.ok(timeout);
  timeout.fn();
  assert.equal(f.children[0].killed, true);
  assert.equal(await f.active(), 1);
  close(f.children[0]);
  assert.equal(await f.active(), 0);
});

test("failed login spawn and duplicate close cannot leak or underflow work", async () => {
  const f = fixture();
  f.failSpawn();
  await f.start();
  assert.equal(await f.active(), 0);
  const g = fixture();
  await g.start();
  close(g.children[0]);
  close(g.children[0]);
  assert.equal(await g.active(), 0);
});

test("update pause refuses new login without spawning a child", async () => {
  const f = fixture();
  const paused = await f.pause();
  assert.equal(paused.active, 0);
  await assert.rejects(f.start(), { code: "update_preparing" });
  assert.equal(f.children.length, 0);
});
