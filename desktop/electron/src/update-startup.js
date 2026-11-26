const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const { targetComponentRevisions } = require("./update-runtime-client");

function guardDesktopStartup({ isPackaged, sidecarPath, args, env, invoke = execFileSync }) {
  if (!isPackaged) return;
  try {
    if (args?.[0] !== "startup-guard") throw new Error("invalid_guard");
    const output = invoke(sidecarPath, args, {
      env, encoding: "utf8", shell: false, timeout: 180_000,
      maxBuffer: 64 * 1024, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
    });
    const view = JSON.parse(output);
    if (view?.status !== "allowed") throw Object.assign(new Error("blocked"), { code: view?.reason });
  } catch (cause) {
    const reported = String(cause.stderr || "").trim();
    const reason = cause.code === "ETIMEDOUT" ? "update_guard_timeout" :
      [cause.code, reported].find(value => typeof value === "string" && /^[a-z_]{1,64}$/.test(value));
    throw Object.assign(new Error("update_startup_blocked"), { code: reason || "update_startup_blocked" });
  }
}
module.exports = { guardDesktopStartup };

const startupFailure = code => Object.assign(new Error(code), { code });
function matchesComponentProof(proof, revisions, unavailable = []) {
  const active = proof.components;
  const pending = proof.pendingComponents === undefined ? {} : proof.pendingComponents;
  const missing = proof.unavailableComponents === undefined ? [] : proof.unavailableComponents;
  if (!Array.isArray(missing) || missing.length !== unavailable.length || new Set(missing).size !== missing.length
    || missing.some(id => !unavailable.includes(id))) return false;
  if (![active, pending].every(map => map && typeof map === "object" && !Array.isArray(map))) return false;
  if (Object.keys(active).length + Object.keys(pending).length !== Object.keys(revisions).length) return false;
  return Object.entries(revisions).every(([id, revision]) => Object.hasOwn(active, id) !== Object.hasOwn(pending, id)
    && (Object.hasOwn(active, id) ? active[id] : pending[id]) === revision);
}

async function runUpdateBootstrap(parameters, adopt, observe) {
  if (!parameters.isPackaged) return { allowed: true, runtimeStarted: false };
  const { invoke, ownerToken, platform = process.platform } = parameters;
  const call = async (action, tx, timeout = 180_000) => {
    observe(action);
    const raw = await invoke("update-bootstrap", ["--action", action, ...(tx ? ["--transaction-id", tx] : [])], { timeout });
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024 * 1024) throw startupFailure("update_record_read");
    let value;
    try { value = JSON.parse(raw); } catch { throw startupFailure("update_record_read"); }
    if (value?.status === "blocked") throw startupFailure(/^[a-z_]{1,64}$/.test(value.reason || "") ? value.reason : "update_incomplete");
    return value;
  };
  let record = await call("inspect");
  observe("inspect", record);
  if (record.status === "allowed") return { allowed: true, runtimeStarted: false, ...(record.runtimeRoot ? { runtimeRoot: record.runtimeRoot } : {}) };
  const tx = record.transactionId;
  if (!/^[a-f0-9]{32}$/.test(tx || "") || record.owner !== ownerToken) throw startupFailure("update_incomplete");
  const currentRelease = parameters.currentReleaseId || record.currentReleaseId;
  if (record.phase === "activated" && ((currentRelease && record.targetReleaseId && currentRelease !== record.targetReleaseId)
    || (displayVersion(record.targetVersion) && record.targetVersion !== parameters.currentVersion))) {
    const error = startupFailure("update_original_target_required");
    error.targetVersion = displayVersion(record.targetVersion);
    throw error;
  }
  if (!adopt && record.status === "repair") return { allowed: false, repair: true, transactionId: tx };
  if (adopt) {
    const adopted = await call("repair-adopt-current", tx, 180_000);
    if (adopted.status !== "adopted" || adopted.transactionId !== tx || adopted.owner !== ownerToken
      || adopted.targetReleaseId !== currentRelease || adopted.targetVersion !== parameters.currentVersion) throw startupFailure("repair_target_not_accepted");
    record = { ...record, ...adopted, phase: "repair", status: "maintenance_required" };
    observe("repair-adopt-current", record);
    await call("prepare-components", tx, 40 * 60_000);
  }
  const expectedRelease = parameters.currentReleaseId || record.currentReleaseId;
  const expectedComponents = targetComponentRevisions(record);
  if (record.status !== "maintenance_required" || !expectedRelease || record.targetReleaseId !== expectedRelease
    || record.targetVersion !== parameters.currentVersion || !expectedComponents) throw startupFailure("update_incomplete");
  // Windows simple installation also expands and relocates target Python here.
  const activated = await call("activate", tx, platform === "win32" ? 30 * 60_000 : 360_000);
  if (activated.status !== "activated" || activated.transactionId !== tx || activated.owner !== ownerToken) throw startupFailure("update_incomplete");
  observe("activate", { phase: "activated", targetVersion: record.targetVersion });
  const started = await call("up", tx, 180_000);
  if (started.status !== "ready" || started.transactionId !== tx || started.owner !== ownerToken || started.bootstrap !== true) throw startupFailure("update_startup_failed");
  try {
    await parameters.onRuntimeStarted?.(record.runtimeRoot);
    const proof = await call("verify", tx, 180_000);
    if (proof.status !== "ready" || proof.transactionId !== tx || proof.owner !== ownerToken
      || proof.releaseId !== expectedRelease || proof.version !== parameters.currentVersion
      || proof.dirty !== false || proof.dataProfile !== record.dataProfile
      || !matchesComponentProof(proof, expectedComponents, record.unavailableComponents)) throw startupFailure("update_startup_failed");
    const peers = record.expectedPeers || ["core", "scan"];
    if (!Array.isArray(peers) || !peers.includes("core") || peers.some(name => !["core", "scan"].includes(name))) throw startupFailure("update_startup_failed");
    for (const name of peers) {
      const peer = proof.peers?.[name];
      if (!peer?.ready || !peer.sealed || peer.transactionId !== tx || peer.owner !== ownerToken) throw startupFailure("update_startup_failed");
    }
  } catch (error) {
    await invoke("down", ["--update-bootstrap", tx, "--bootstrap-owner", ownerToken], { timeout: 90_000 }).catch(() => {});
    throw error;
  }
  const complete = await call("complete", tx, 180_000);
  if (complete.status !== "completed" || complete.transactionId !== tx || complete.owner !== ownerToken
    || (record.expectedPeers || ["core", "scan"]).some(name => complete.peersReleased?.[name] !== true)) throw startupFailure("update_release_failed");
  return { allowed: true, runtimeStarted: true, transactionId: tx, runtimeRoot: record.runtimeRoot };
}
const displayVersion = value => typeof value === "string" && /^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.+-]+)?$/.test(value) && value.length <= 80 ? value : undefined;
function recordUpdateStartup(getDirectory, event) {
  try {
    const dir = getDirectory();
    fs.mkdirSync(dir, { recursive: true });
    fs.appendFileSync(path.join(dir, "desktop-update-recovery.log"), JSON.stringify({ time: new Date().toISOString(), ...event }) + "\n", { mode: 0o600 });
  } catch { /* A diagnostic write must not change recovery behavior. */ }
}
async function observedBootstrap(parameters, adopt) {
  let action = "inspect", phase, targetVersion;
  const observe = (step, record) => {
    if (record) {
      phase = ["pending", "installing", "activated", "repair"].includes(record.phase) ? record.phase : undefined;
      targetVersion = displayVersion(record.targetVersion);
    }
    if (step !== "down") action = step;
  };
  const report = reason => {
    try {
      parameters.onRecovery?.({ action, reason, phase, targetVersion, installedVersion: displayVersion(parameters.currentVersion) });
    } catch { /* Diagnostics may be unavailable before Electron resolves its logs path. */ }
  };
  try {
    const result = await runUpdateBootstrap(parameters, adopt, observe);
    if (!result.allowed) report("update_needs_repair");
    return result;
  } catch (error) {
    report(/^[a-z_]{1,64}$/.test(error?.code || "") ? error.code : "update_startup_failed");
    throw error;
  }
}
module.exports.recordUpdateStartup = recordUpdateStartup;
module.exports.prepareDesktopStartup = parameters => observedBootstrap(parameters, false);
module.exports.adoptDesktopRepair = parameters => observedBootstrap(parameters, true);
