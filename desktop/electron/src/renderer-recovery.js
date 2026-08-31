async function waitForRendererWithRuntimeRecovery({
  startAttempt,
  runtimeReady,
  shouldRecover = () => true,
  onRecovery = () => {},
}) {
  let attempt = await startAttempt();
  const firstOutcome = await Promise.race([
    Promise.resolve(attempt.ready).then(
      () => ({ type: "renderer-ready" }),
      (error) => ({ type: "renderer-failed", error }),
    ),
    Promise.resolve(runtimeReady).then(
      (status) => ({ type: "runtime-ready", status }),
      (error) => ({ type: "runtime-failed", error }),
    ),
  ]);

  if (firstOutcome.type === "renderer-ready") {
    return attempt;
  }
  if (firstOutcome.type === "runtime-failed") {
    await attempt.dispose();
    throw firstOutcome.error;
  }

  let recoveryReason = "runtime-ready";
  if (firstOutcome.type === "renderer-failed") {
    recoveryReason = "renderer-failed";
    try {
      await runtimeReady;
    } catch (error) {
      await attempt.dispose();
      throw error;
    }
  } else if (!attempt.isPending()) {
    try {
      await attempt.ready;
      return attempt;
    } catch {
      recoveryReason = "renderer-failed";
    }
  }

  await attempt.dispose();
  if (!shouldRecover()) {
    return null;
  }
  await onRecovery(recoveryReason);
  attempt = await startAttempt();
  await attempt.ready;
  return attempt;
}

module.exports = { waitForRendererWithRuntimeRecovery };
