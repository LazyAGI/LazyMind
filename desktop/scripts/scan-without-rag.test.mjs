import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const { desktopRuntimeReady } = require("../electron/src/runtime-status.js");

function collectorDesktopStatus() {
  return {
    overallStatus: "ready",
    ownerMatched: true,
    config: {
      frontendPort: 8090,
      algorithm: { RAGDisabled: true },
      modeProfile: { VectorStore: { ManagedProcess: false } },
    },
    services: Object.fromEntries([
      "process-supervisor", "sqlite-server", "local-proxy", "auth-service",
      "channel-gateway", "core", "frontend", "chat", "scan-control-plane", "file-watcher",
    ].map((name) => [name, { status: "running" }])),
  };
}

test("Desktop accepts collectors without optional RAG services", () => {
  assert.equal(desktopRuntimeReady(collectorDesktopStatus(), true), true);
});

for (const service of ["scan-control-plane", "file-watcher"]) {
  test(`Desktop without RAG still requires ${service}`, () => {
    const status = collectorDesktopStatus();
    delete status.services[service];
    assert.equal(desktopRuntimeReady(status, true), false);
  });

  test(`Desktop without RAG waits for ${service} readiness`, () => {
    const status = collectorDesktopStatus();
    status.services[service].status = "starting";
    assert.equal(desktopRuntimeReady(status, true), false);
    status.services[service].status = "ready";
    assert.equal(desktopRuntimeReady(status, true), true);
  });
}
