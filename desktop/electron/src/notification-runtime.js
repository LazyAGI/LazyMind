const { stat } = require("node:fs/promises");
const path = require("node:path");

async function runtimeStateVersion(root) {
  if (!root) return null;
  try {
    const state = await stat(path.join(root, "state", "runtime-state.json"), { bigint: true });
    if (!state.isFile()) return null;
    return `${state.dev}:${state.ino}:${state.size}:${state.mtimeNs}:${state.ctimeNs}`;
  } catch {
    return null;
  }
}

function createNotificationRuntimeReader({ readRuntime, getMonitor, getRuntimeRoot, now = Date.now }) {
  let cached;
  return async () => {
    const monitor = getMonitor();
    const root = getRuntimeRoot();
    const version = monitor ? await runtimeStateVersion(root) : null;
    if (monitor !== getMonitor() || root !== getRuntimeRoot()) return null;
    if (version && cached?.monitor === monitor && cached.root === root
      && cached.version === version && now() < cached.expiresAt) return cached.runtime;

    cached = undefined;
    const runtime = await readRuntime();
    if (monitor !== getMonitor()) return null;
    if (!runtime?.ready || !version || root !== getRuntimeRoot()) return runtime;

    // A status check performs service probes. Reuse its binding only while the
    // monitor and state file stay current. The feed still authenticates each poll.
    const currentVersion = await runtimeStateVersion(root);
    if (monitor !== getMonitor() || root !== getRuntimeRoot() || version !== currentVersion) return null;
    cached = { monitor, root, version, runtime, expiresAt: now() + 60000 };
    return runtime;
  };
}

module.exports = { createNotificationRuntimeReader };
