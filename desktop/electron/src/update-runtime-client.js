const path = require("node:path");
const failure = code => Object.assign(new Error(code), { code });
function createUpdateRuntimeClient({ runSidecar, localWork, setAdmission, stopAuxiliary, verifyStartup }) {
  let epoch, transactionId, preparation;
  async function bootstrap(action, extra = [], timeout = 180_000) {
    let value;
    try {
      const raw = await runSidecar("update-bootstrap", ["--action", action,
        ...(transactionId ? ["--transaction-id", transactionId] : []), ...extra], { timeout, onSpawn: action === "prepare-components" ? child => { preparation = child; } : undefined });
      if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024 * 1024) throw new Error();
      value = JSON.parse(raw);
    } catch { throw failure("component_maintenance_failed"); }
    if (value?.status === "blocked") throw failure(/^[a-z_]{1,64}$/.test(value.reason || "") ? value.reason : "component_maintenance_failed");
    return value;
  }
  async function invoke(action, requestedEpoch) {
    let value;
    try {
      const raw = await runSidecar("update-control", ["--action", action, ...(requestedEpoch ? ["--epoch", String(requestedEpoch)] : [])], { timeout: action === "stop" ? 90_000 : 30_000 });
      if (typeof raw !== "string" || Buffer.byteLength(raw) > 4096) throw new Error();
      value = JSON.parse(raw);
      if (action === "stop") { if (value.status !== "stopped") throw new Error(); }
      else if (!Number.isSafeInteger(value.active) || value.active < 0 || typeof value.paused !== "boolean" || !Number.isSafeInteger(value.epoch) || value.epoch < 0 || value.paused !== (value.epoch !== 0)
        || (action === "pause" && (!value.paused || value.epoch !== requestedEpoch)) || (action === "resume" && value.paused)) throw new Error();
      return value;
    } catch { throw failure(action === "stop" ? "runtime_stop_failed" : "task_state_unknown"); }
  }
  return {
    async prepareComponents(release, receipt) {
      const value = await bootstrap("prepare-components", ["--release-id", release.releaseId,
        "--package", receipt.packagePath, "--components", receipt.componentsPath, "--control-stdin"], 40 * 60_000);
      if (!/^[a-f0-9]{32}$/.test(value?.transactionId || "")) throw failure("component_state_unknown");
      preparation = undefined;
      transactionId = value.transactionId;
      return value;
    },
    async cancelPreparation() { preparation?.stdin?.end("cancel\n"); },
    async verifyComponents(release, receipt) {
      if (transactionId) {
        const result = await bootstrap("verify-components");
        if (result?.status !== "compatible" || !/^[A-Za-z0-9._-]{1,128}$/.test(result.dataProfile || "")) throw failure("component_state_unknown");
        return result;
      }
      const artifact = release?.components, filename = receipt?.componentsPath;
      if (typeof filename !== "string" || !path.isAbsolute(filename) || filename.includes("\0")
        || !/^[a-f0-9]{64}$/.test(artifact?.sha256 || "") || !Number.isSafeInteger(artifact?.size)
        || artifact.size <= 0 || artifact.size > 1024 * 1024) throw failure("component_update_incompatible");
      let value;
      try {
        const raw = await runSidecar("update-components-check", ["--components", filename, "--sha256", artifact.sha256, "--size", String(artifact.size)], { timeout: 30000 });
        if (typeof raw !== "string" || Buffer.byteLength(raw) > 4096) throw new Error();
        value = JSON.parse(raw);
      } catch { throw failure("data_unknown"); }
      if (value?.status === "blocked" && /^[a-z_]{1,64}$/.test(value.reason || "")) throw failure(value.reason);
      if (value?.status !== "compatible" || !/^[A-Za-z0-9._-]{1,128}$/.test(value.dataProfile || "")) throw failure("data_unknown");
      return { dataProfile: value.dataProfile };
    },
    async status() { const remote = await invoke("status"); if (remote.paused) throw failure("task_state_unknown"); return { ...remote, active: remote.active + localWork() }; },
    async pause(value) {
      if (!Number.isSafeInteger(value) || value <= 0 || epoch && epoch !== value) throw failure("update_control_invalid");
      epoch = value; setAdmission(false);
      const remote = await invoke("pause", value);
      return { ...remote, active: remote.active + localWork() };
    },
    async resume(value) {
      if (epoch && epoch !== value) throw failure("update_control_invalid");
      await invoke("resume", value);
      epoch = undefined; setAdmission(true);
    },
    async stop(value) {
      if (epoch !== value || localWork() !== 0) throw failure("tasks_active");
      // Stop browser/agent auxiliary owners first, then the service supervisor.
      await stopAuxiliary();
      return invoke("stop", value);
    },
    verifyStartup,
  };
}
module.exports = { createUpdateRuntimeClient };
