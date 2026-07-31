const fs = require("node:fs");
const path = require("node:path");

const readyStatuses = new Set(["ready", "running"]);
const defaultCapabilities = {
  home: ["process-supervisor", "local-proxy", "auth-service", "frontend"],
  ui: ["process-supervisor", "local-proxy", "auth-service", "core", "frontend"],
  chat: ["process-supervisor", "local-proxy", "auth-service", "core", "frontend", "chat"],
  parser: [
    "process-supervisor",
    "core",
    "lazyllm-doc-server",
    "lazyllm-parse-server",
    "lazyllm-parse-worker",
    "lazyllm-algo",
  ],
};

function runtimeCapabilityReady(
  status,
  capability,
  capabilities = defaultCapabilities,
) {
  const requiredServices = capabilities[capability] || [];
  const services = status?.services || {};
  return requiredServices.length > 0 &&
    requiredServices.every((service) => readyStatuses.has(services[service]?.status));
}

function metricKey(name) {
  return name.endsWith("Ms") ? name : `${name}Ms`;
}

function createStartupMetricsRecorder({
  metadata = {},
  elapsedMs = () => Math.round(process.uptime() * 1000),
  startedAt = new Date(Date.now() - process.uptime() * 1000).toISOString(),
  capabilities = defaultCapabilities,
} = {}) {
  const metrics = {
    schemaVersion: 1,
    startedAt,
    ...metadata,
    milestones: {
      appProcessStartedMs: 0,
    },
    services: {},
    capabilities: {},
  };
  let completed = false;

  function elapsed() {
    return Math.max(0, Math.round(elapsedMs()));
  }

  function mark(name) {
    const key = metricKey(name);
    if (metrics.milestones[key] === undefined) {
      metrics.milestones[key] = elapsed();
    }
    return metrics.milestones[key];
  }

  function observeStatus(status) {
    const services = status?.services || {};
    for (const [name, service] of Object.entries(services)) {
      if (readyStatuses.has(service?.status) && metrics.services[name]?.readyMs === undefined) {
        metrics.services[name] = { readyMs: elapsed() };
      }
    }
    for (const [name, requiredServices] of Object.entries(capabilities)) {
      const key = metricKey(`${name}Ready`);
      if (
        metrics.capabilities[key] === undefined &&
        requiredServices.every((service) => readyStatuses.has(services[service]?.status))
      ) {
        metrics.capabilities[key] = elapsed();
      }
    }
  }

  function finish(outcome, failureCode) {
    if (completed) {
      return null;
    }
    completed = true;
    metrics.outcome = outcome;
    metrics.completedMs = elapsed();
    if (failureCode) {
      metrics.failureCode = String(failureCode);
    }
    return JSON.parse(JSON.stringify(metrics));
  }

  function snapshot() {
    return JSON.parse(JSON.stringify(metrics));
  }

  return {
    finish,
    mark,
    observeStatus,
    snapshot,
  };
}

function readMetricHistory(historyPath) {
  try {
    return fs.readFileSync(historyPath, "utf8")
      .split(/\r?\n/)
      .filter(Boolean)
      .flatMap((line) => {
        try {
          return [JSON.parse(line)];
        } catch {
          return [];
        }
      });
  } catch (error) {
    if (error?.code === "ENOENT") {
      return [];
    }
    throw error;
  }
}

function writeStartupMetrics(historyPath, metrics, maxEntries = 50) {
  fs.mkdirSync(path.dirname(historyPath), { recursive: true });
  const history = [...readMetricHistory(historyPath), metrics].slice(-maxEntries);
  fs.writeFileSync(historyPath, `${history.map((entry) => JSON.stringify(entry)).join("\n")}\n`);
  const latestPath = path.join(path.dirname(historyPath), "startup-metrics-latest.json");
  fs.writeFileSync(latestPath, `${JSON.stringify(metrics, null, 2)}\n`);
}

module.exports = {
  createStartupMetricsRecorder,
  defaultCapabilities,
  readMetricHistory,
  runtimeCapabilityReady,
  writeStartupMetrics,
};
