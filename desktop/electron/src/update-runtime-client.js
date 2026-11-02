const path = require("node:path");
const failure = code => Object.assign(new Error(code), { code });
function createUpdateRuntimeClient({ runSidecar, localWork, setAdmission, stopAuxiliary, stopGuard, verifyStartup }) {
  let epoch;
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
    async verifyComponents(release, receipt) {
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
      const result = await invoke("stop", value);
      // The unsigned helper shares startup admission with the resident guard.
      // Retire that guard only after services are confirmed stopped.
      await stopGuard?.();
      return result;
    },
    verifyStartup,
  };
}
module.exports = { createUpdateRuntimeClient };
