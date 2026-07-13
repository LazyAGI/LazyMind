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

module.exports = { runtimeExitFailureMessage, statusFailureMessage };
