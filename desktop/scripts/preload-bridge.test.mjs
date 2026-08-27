import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { createDesktopBridge, installDesktopBridge } = require("../electron/src/preload.js");

function fakeIPC() {
  const invocations = [];
  const sends = [];
  const listeners = new Map();
  return {
    invocations,
    sends,
    listeners,
    invoke: async (...args) => {
      invocations.push(args);
      return { channel: args[0], arguments: args.slice(1) };
    },
    send: (...args) => sends.push(args),
    on: (channel, listener) => listeners.set(channel, listener),
    removeListener: (channel, listener) => {
      if (listeners.get(channel) === listener) listeners.delete(channel);
    },
  };
}

test("installs only the named LazyMind bridge in the isolated world", () => {
  const exposed = [];
  const ipc = fakeIPC();
  const bridge = installDesktopBridge(
    { exposeInMainWorld: (...args) => exposed.push(args) },
    ipc,
  );
  assert.deepEqual(exposed, [["lazymindDesktop", bridge]]);
});

test("maps bridge methods to their exact IPC channels and arguments", async () => {
  const ipc = fakeIPC();
  const bridge = createDesktopBridge(ipc);
  const cases = [
    ["openLogsDir", [], "lazymind:openLogsDir", []],
    ["openDataDir", [], "lazymind:openDataDir", []],
    ["runtimeStatus", [], "lazymind:runtimeStatus", []],
    ["agentIntegrationStatuses", [], "lazymind:agentIntegrationStatuses", []],
    ["agentIntegrationAction", ["cursor", "connect"], "lazymind:agentIntegrationAction", ["cursor", "connect"]],
    ["executorIntegrationPolicies", [], "lazymind:executorIntegrationPolicies", []],
    ["executorIntegrationAction", ["codex", "disable"], "lazymind:executorIntegrationAction", ["codex", "disable"]],
    ["restartRuntime", [], "lazymind:restartRuntime", []],
    ["resetRuntime", ["all"], "lazymind:resetRuntime", ["all"]],
    ["selectFolder", [], "lazymind:selectFolder", []],
    ["selectExecutable", [], "lazymind:selectExecutable", []],
    ["exportDiagnostics", [], "lazymind:exportDiagnostics", []],
    ["showItemInFolder", [{ source: "/static-files/a.txt" }], "lazymind:showItemInFolder", [{ source: "/static-files/a.txt" }]],
    ["saveFileAs", [{ source: "https://x/a", filename: "a.txt" }], "lazymind:saveFileAs", [{ source: "https://x/a", filename: "a.txt" }]],
    ["downloadFile", [{ source: "https://x/a", filename: "a.txt" }], "lazymind:downloadFile", [{ source: "https://x/a", filename: "a.txt" }]],
    ["startupDiagnostics", [], "lazymind:startupDiagnostics", []],
    ["copyStartupLogs", [], "lazymind:copyStartupLogs", []],
  ];
  for (const [method, args, channel, expectedArgs] of cases) {
    const result = await bridge[method](...args);
    assert.deepEqual(result, { channel, arguments: expectedArgs });
  }
  assert.equal(bridge.platform, process.platform);
  bridge.notifyAppReady();
  assert.deepEqual(ipc.sends, [["lazymind:renderer-ready"]]);
});

test("forwards startup diagnostics and removes exactly its own listener", () => {
  const ipc = fakeIPC();
  const bridge = createDesktopBridge(ipc);
  const payloads = [];
  const unsubscribe = bridge.onStartupDiagnosticsUpdate((payload) => payloads.push(payload));
  const listener = ipc.listeners.get("lazymind:startupDiagnosticsUpdate");
  listener({}, { phase: "runtime" });
  assert.deepEqual(payloads, [{ phase: "runtime" }]);
  unsubscribe();
  assert.equal(ipc.listeners.has("lazymind:startupDiagnosticsUpdate"), false);
});

test("ignores invalid diagnostics listeners without touching IPC", () => {
  const ipc = fakeIPC();
  const bridge = createDesktopBridge(ipc);
  const unsubscribe = bridge.onStartupDiagnosticsUpdate(null);
  unsubscribe();
  assert.equal(ipc.listeners.size, 0);
});

test("propagates IPC failures so the renderer can report them", async () => {
  const expected = new Error("runtime unavailable");
  const bridge = createDesktopBridge({ invoke: async () => { throw expected; } });
  await assert.rejects(bridge.runtimeStatus(), expected);
});
