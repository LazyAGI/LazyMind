const { execFileSync } = require("node:child_process");
const { targetComponentRevisions } = require("./update-runtime-client");

function guardDesktopStartup({ isPackaged, sidecarPath, args, env, invoke = execFileSync }) {
  if (!isPackaged) return;
  try {
    if (args?.[0] !== "startup-guard") throw new Error("invalid_guard");
    const output = invoke(sidecarPath, args, {
      env, encoding: "utf8", shell: false, timeout: 180_000,
      maxBuffer: 64 * 1024, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
    });
    if (JSON.parse(output)?.status !== "allowed") throw new Error("blocked");
  } catch {
    throw new Error("update_startup_blocked");
  }
}
module.exports = { guardDesktopStartup };

const startupFailure = code => Object.assign(new Error(code), { code });
async function runUpdateBootstrap(parameters, adopt) {
  if (!parameters.isPackaged) return { allowed: true, runtimeStarted: false };
  const { invoke, ownerToken, platform = process.platform } = parameters;
  const call = async (action, tx, timeout = 180_000) => {
    const raw = await invoke("update-bootstrap", ["--action", action, ...(tx ? ["--transaction-id", tx] : [])], { timeout });
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024 * 1024) throw startupFailure("update_record_read");
    let value;
    try { value = JSON.parse(raw); } catch { throw startupFailure("update_record_read"); }
    if (value?.status === "blocked") throw startupFailure(/^[a-z_]{1,64}$/.test(value.reason || "") ? value.reason : "update_incomplete");
    return value;
  };
  let record = await call("inspect");
  if (record.status === "allowed") return { allowed: true, runtimeStarted: false, ...(record.runtimeRoot ? { runtimeRoot: record.runtimeRoot } : {}) };
  const tx = record.transactionId;
  if (!/^[a-f0-9]{32}$/.test(tx || "") || record.owner !== ownerToken) throw startupFailure("update_incomplete");
  if (!adopt && record.status === "repair") return { allowed: false, repair: true, transactionId: tx };
  if (adopt) {
    const adopted = await call("repair-adopt-current", tx, 180_000);
    if (adopted.status !== "adopted" || adopted.transactionId !== tx || adopted.owner !== ownerToken) throw startupFailure("repair_target_not_accepted");
    record = { ...record, ...adopted, status: "maintenance_required" };
    await call("prepare-components", tx, 40 * 60_000);
  }
  const expectedRelease = parameters.currentReleaseId || record.currentReleaseId;
  const expectedComponents = targetComponentRevisions(record);
  if (record.status !== "maintenance_required" || !expectedRelease || record.targetReleaseId !== expectedRelease
    || record.targetVersion !== parameters.currentVersion || !expectedComponents) throw startupFailure("update_incomplete");
  // Windows simple installation also expands and relocates target Python here.
  const activated = await call("activate", tx, platform === "win32" ? 30 * 60_000 : 360_000);
  if (activated.status !== "activated" || activated.transactionId !== tx || activated.owner !== ownerToken) throw startupFailure("update_incomplete");
  const started = await call("up", tx, 180_000);
  if (started.status !== "ready" || started.transactionId !== tx || started.owner !== ownerToken || started.bootstrap !== true) throw startupFailure("update_startup_failed");
  try {
    await parameters.onRuntimeStarted?.(record.runtimeRoot);
    const proof = await call("verify", tx, 180_000);
    if (proof.status !== "ready" || proof.transactionId !== tx || proof.owner !== ownerToken
      || proof.releaseId !== expectedRelease || proof.version !== parameters.currentVersion
      || proof.dirty !== false || proof.dataProfile !== record.dataProfile
      || !proof.components || typeof proof.components !== "object" || Array.isArray(proof.components)
      || Object.keys(proof.components).length !== Object.keys(expectedComponents).length
      || Object.entries(expectedComponents).some(([id, revision]) => proof.components[id] !== revision)) throw startupFailure("update_startup_failed");
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
module.exports.prepareDesktopStartup = parameters => runUpdateBootstrap(parameters, false);
module.exports.adoptDesktopRepair = parameters => runUpdateBootstrap(parameters, true);
