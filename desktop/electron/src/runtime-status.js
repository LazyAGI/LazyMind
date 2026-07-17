const requiredDesktopServices = [
  "process-supervisor",
  "local-proxy",
  "auth-service",
  "core",
  "scan-control-plane",
  "file-watcher",
  "lazyllm-doc-server",
  "lazyllm-parse-server",
  "lazyllm-parse-worker",
  "lazyllm-algo",
  "chat",
  "frontend",
];

function desktopRuntimeReady(status, belongsToDesktop) {
  if (
    status?.overallStatus !== "ready" ||
    !belongsToDesktop ||
    !status.ownerMatched ||
    !status.config?.frontendPort
  ) {
    return false;
  }
  const services = status.services || {};
  const vectorStore = status.config?.modeProfile?.VectorStore || status.config?.modeProfile?.vectorStore;
  const managedVectorStore = vectorStore?.ManagedProcess ?? vectorStore?.managedProcess;
  const required = managedVectorStore
    ? [...requiredDesktopServices, "milvus-lite"]
    : requiredDesktopServices;
  return required.every((name) => ["running", "ready"].includes(services[name]?.status));
}

function statusFailureMessage(status) {
  const summary = status?.overallStatus ? `Runtime status is ${status.overallStatus}` : "Runtime did not become ready";
  const failedServices = Object.entries(status?.services || {})
    .filter(([, service]) => ["failed", "stale", "stopped"].includes(service?.status))
    .map(([name, service]) => `${name}:${service.status}`)
    .slice(0, 8);
  return failedServices.length ? `${summary}; services: ${failedServices.join(", ")}` : summary;
}

function runtimeExitFailureMessage(status, belongsToDesktop, runtimeProcessExit) {
  if (!runtimeProcessExit) {
    return "";
  }
  const sidecarDetail = String(runtimeProcessExit.detail || runtimeProcessExit.error || "").trim();
  if (sidecarDetail && (runtimeProcessExit.code !== 0 || runtimeProcessExit.error)) {
    return sidecarDetail;
  }
  if (status?.overallStatus === "ready" && belongsToDesktop && !status.ownerMatched) {
    return "Another LazyMind Desktop instance owns the running local runtime. Close it before opening Desktop again.";
  }
  if (["stopped", "stale", "unknown", ""].includes(status?.overallStatus || "")) {
    return statusFailureMessage(status);
  }
  if (status?.overallStatus === "failed") {
    return statusFailureMessage(status);
  }
  return "";
}

module.exports = { desktopRuntimeReady, requiredDesktopServices, runtimeExitFailureMessage, statusFailureMessage };
