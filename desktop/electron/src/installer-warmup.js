function serializeWarmupError(error) {
  if (error instanceof Error) {
    return error.stack || error.message;
  }
  return String(error);
}

function assertMaintenanceRuntimeReady(status) {
  if (status?.overallStatus !== "ready" || !status?.config?.frontendPort) {
    throw new Error(`maintenance runtime did not become ready (status=${status?.overallStatus || "unknown"})`);
  }
}

function assertMaintenanceRuntimeStopped(status) {
  const activeServices = Object.entries(status?.services || {})
    .filter(([, service]) => service?.status !== "stopped")
    .map(([name, service]) => `${name}=${service?.status || "unknown"}`);
  if (status?.overallStatus !== "stopped" || status?.ownerMatched !== true || activeServices.length > 0) {
    const details = activeServices.length > 0 ? `; services=${activeServices.join(",")}` : "";
    throw new Error(
      `maintenance runtime did not stop cleanly (status=${status?.overallStatus || "unknown"}, ownerMatched=${Boolean(status?.ownerMatched)}${details})`,
    );
  }
}

async function runInstallerWarmupLifecycle({
  startRuntime,
  readStatus,
  createRenderer,
  loadRenderer,
  stopRuntime,
  disposeRenderer,
  log,
  formatError = serializeWarmupError,
}) {
  let renderer;
  let primaryError = null;
  let cleanupError = null;

  try {
    await startRuntime();
    const status = await readStatus();
    assertMaintenanceRuntimeReady(status);
    renderer = createRenderer();
    await loadRenderer(renderer, status);
    log("runtime and renderer warmup completed");
  } catch (error) {
    primaryError = error;
    log(`warmup failed: ${formatError(error)}`);
  }

  try {
    log("stopping maintenance runtime");
    await stopRuntime();
    const stoppedStatus = await readStatus();
    assertMaintenanceRuntimeStopped(stoppedStatus);
    log("maintenance runtime stopped and verified");
  } catch (error) {
    cleanupError = error;
    log(`maintenance runtime shutdown failed: ${formatError(error)}`);
  } finally {
    if (renderer) {
      disposeRenderer(renderer);
      log("warmup renderer disposed");
    }
  }

  if (primaryError) {
    throw primaryError;
  }
  if (cleanupError) {
    throw cleanupError;
  }
}

module.exports = {
  assertMaintenanceRuntimeReady,
  assertMaintenanceRuntimeStopped,
  runInstallerWarmupLifecycle,
};
