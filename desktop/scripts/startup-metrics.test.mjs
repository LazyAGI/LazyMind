import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const reportScript = path.join(scriptsDir, "report-startup-metrics.mjs");
const {
  createStartupMetricsRecorder,
  readMetricHistory,
  runtimeCapabilityReady,
  writeStartupMetrics,
} = require("../electron/src/startup-metrics.js");

function serviceStatus(readyServices) {
  const names = [
    "process-supervisor",
    "local-proxy",
    "auth-service",
    "core",
    "frontend",
    "lazyllm-algo",
    "chat",
    "lazyllm-doc-server",
    "lazyllm-parse-server",
    "lazyllm-parse-worker",
  ];
  return {
    services: Object.fromEntries(names.map((name) => [
      name,
      { status: readyServices.includes(name) ? "running" : "starting" },
    ])),
  };
}

test("records first service and capability readiness without changing later timestamps", () => {
  let now = 300;
  const recorder = createStartupMetricsRecorder({
    elapsedMs: () => now,
    startedAt: "2026-07-29T00:00:00.000Z",
  });
  const homeServices = ["process-supervisor", "local-proxy", "auth-service", "frontend"];
  const uiServices = ["process-supervisor", "local-proxy", "auth-service", "core", "frontend"];

  recorder.observeStatus(serviceStatus(homeServices));
  now = 500;
  recorder.observeStatus(serviceStatus(uiServices));
  now = 900;
  recorder.observeStatus(serviceStatus([...uiServices, "chat"]));

  const metrics = recorder.finish("success");
  assert.equal(metrics.capabilities.homeReadyMs, 300);
  assert.equal(metrics.capabilities.uiReadyMs, 500);
  assert.equal(metrics.capabilities.chatReadyMs, 900);
  assert.equal(metrics.services.frontend.readyMs, 300);
  assert.equal(metrics.services.chat.readyMs, 900);
});

test("checks runtime capabilities independently", () => {
  const preAuthServices = ["process-supervisor", "local-proxy", "frontend"];
  const homeServices = [...preAuthServices, "auth-service"];
  const uiServices = ["process-supervisor", "local-proxy", "auth-service", "core", "frontend"];
  const preAuthStatus = serviceStatus(preAuthServices);
  const homeStatus = serviceStatus(homeServices);
  const uiStatus = serviceStatus(uiServices);
  const chatStatus = serviceStatus([...uiServices, "chat"]);

  assert.equal(runtimeCapabilityReady(preAuthStatus, "home"), false);
  assert.equal(runtimeCapabilityReady(homeStatus, "home"), true);
  assert.equal(runtimeCapabilityReady(homeStatus, "ui"), false);
  assert.equal(runtimeCapabilityReady(uiStatus, "ui"), true);
  assert.equal(runtimeCapabilityReady(uiStatus, "chat"), false);
  assert.equal(runtimeCapabilityReady(chatStatus, "chat"), true);
  assert.equal(runtimeCapabilityReady(chatStatus, "parser"), false);
});

test("records milestones once and finishes once", () => {
  let now = 100;
  const recorder = createStartupMetricsRecorder({ elapsedMs: () => now });

  recorder.mark("windowVisible");
  now = 300;
  recorder.mark("windowVisible");

  const metrics = recorder.finish("failed", "runtime-startup");
  assert.equal(metrics.milestones.windowVisibleMs, 100);
  assert.equal(metrics.completedMs, 300);
  assert.equal(metrics.failureCode, "runtime-startup");
  assert.equal(recorder.finish("success"), null);
});

test("keeps only the configured number of local metric records", () => {
  const root = mkdtempSync(path.join(tmpdir(), "lazymind-startup-metrics-"));
  const historyPath = path.join(root, "startup-metrics.jsonl");

  writeStartupMetrics(historyPath, { run: 1 }, 2);
  writeStartupMetrics(historyPath, { run: 2 }, 2);
  writeStartupMetrics(historyPath, { run: 3 }, 2);

  assert.deepEqual(readMetricHistory(historyPath), [{ run: 2 }, { run: 3 }]);
});

test("local report summarizes successful runs and excludes failures", () => {
  const root = mkdtempSync(path.join(tmpdir(), "lazymind-startup-report-"));
  const historyPath = path.join(root, "startup-metrics.jsonl");
  const successfulRun = {
    launchKind: "normal",
    outcome: "success",
    milestones: { windowVisibleMs: 400, rendererReadyMs: 4000 },
    capabilities: { uiReadyMs: 2000, chatReadyMs: 3500 },
    services: { chat: { readyMs: 3500 } },
  };
  writeStartupMetrics(historyPath, successfulRun);
  writeStartupMetrics(historyPath, {
    ...successfulRun,
    outcome: "failed",
    milestones: { windowVisibleMs: 9000 },
  });

  const output = execFileSync(process.execPath, [reportScript, historyPath, "--kind", "normal"], {
    encoding: "utf8",
  });
  assert.match(output, /successful: 1; failed\/cancelled: 1/);
  assert.match(output, /windowVisible\s+400ms\s+400ms\s+400ms/);
  assert.match(output, /service:chat\s+3500ms/);
});
