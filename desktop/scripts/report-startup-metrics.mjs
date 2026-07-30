#!/usr/bin/env node

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

function defaultMetricsPath() {
  if (process.platform === "darwin") {
    return path.join(os.homedir(), "Library", "Logs", "lazymind-desktop", "startup-metrics.jsonl");
  }
  if (process.platform === "win32") {
    const localAppData = process.env.LOCALAPPDATA || path.join(os.homedir(), "AppData", "Local");
    return path.join(localAppData, "LazyMind", "Logs", "desktop", "startup-metrics.jsonl");
  }
  return path.join(os.homedir(), ".config", "lazymind-desktop", "startup-metrics.jsonl");
}

function percentile(values, percentileValue) {
  if (values.length === 0) {
    return null;
  }
  const sorted = [...values].sort((left, right) => left - right);
  const index = Math.max(0, Math.ceil(percentileValue * sorted.length) - 1);
  return sorted[index];
}

function metricValue(record, metric) {
  if (metric.startsWith("service:")) {
    return record.services?.[metric.slice("service:".length)]?.readyMs;
  }
  if (metric.startsWith("capability:")) {
    return record.capabilities?.[`${metric.slice("capability:".length)}ReadyMs`];
  }
  return record.milestones?.[`${metric}Ms`];
}

const args = process.argv.slice(2);
const kindIndex = args.indexOf("--kind");
const requestedKind = kindIndex >= 0 ? args[kindIndex + 1] : "";
const positional = args.filter((arg, index) => (
  arg !== "--kind" && (kindIndex < 0 || index !== kindIndex + 1)
));
const metricsPath = path.resolve(positional[0] || defaultMetricsPath());
const selectedRecords = fs.readFileSync(metricsPath, "utf8")
  .split(/\r?\n/)
  .filter(Boolean)
  .map((line) => JSON.parse(line))
  .filter((record) => !requestedKind || record.launchKind === requestedKind);
const records = selectedRecords.filter((record) => record.outcome === "success");

if (selectedRecords.length === 0) {
  throw new Error(`No matching startup metric records found in ${metricsPath}`);
}
if (records.length === 0) {
  throw new Error(`No successful startup metric records found in ${metricsPath}`);
}

const metrics = [
  "windowVisible",
  "capability:home",
  "capability:ui",
  "capability:chat",
  "runtimeReady",
  "rendererReady",
  "mainWindowVisible",
];
const serviceNames = new Set(records.flatMap((record) => Object.keys(record.services || {})));
for (const service of serviceNames) {
  metrics.push(`service:${service}`);
}

console.log(`Startup metrics: ${metricsPath}`);
console.log(
  `Runs: ${selectedRecords.length}; successful: ${records.length}; failed/cancelled: ${selectedRecords.length - records.length}` +
  `${requestedKind ? ` (${requestedKind})` : ""}`,
);
console.log("");
console.log("metric".padEnd(34), "p50".padStart(8), "p95".padStart(8), "latest".padStart(8));
for (const metric of metrics) {
  const values = records.map((record) => metricValue(record, metric)).filter(Number.isFinite);
  if (values.length === 0) {
    continue;
  }
  console.log(
    metric.padEnd(34),
    `${percentile(values, 0.5)}ms`.padStart(8),
    `${percentile(values, 0.95)}ms`.padStart(8),
    `${values.at(-1)}ms`.padStart(8),
  );
}
