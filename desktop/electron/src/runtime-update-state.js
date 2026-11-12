async function readRuntimeUpdateState(runSidecar, systemVersion) {
  try {
    const parts = String(systemVersion).split(".");
    while (parts.length < 3) parts.push("0");
    if (parts.length !== 3 || parts.some(value => !/^\d+$/.test(value))) throw new Error();
    const raw = await runSidecar("update-state", [], { timeout: 30000 });
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 1024 * 1024) throw new Error();
    const state = JSON.parse(raw);
    if (typeof state?.dirty !== "boolean" || typeof state.dataProfile !== "string"
      || (!state.dirty && !/^[A-Za-z0-9._-]{1,128}$/.test(state.dataProfile))
      || (state.dirty && state.dataProfile !== "")
      || !state.components || Array.isArray(state.components) || typeof state.components !== "object"
      || Object.keys(state.components).length > 32
      || Object.entries(state.components).some(([key, value]) => !/^[A-Za-z0-9._-]{1,64}$/.test(key) || typeof value !== "string" || !/^[A-Za-z0-9._-]{1,128}$/.test(value))) throw new Error();
    const metadata = state.componentMetadata;
    if (metadata !== undefined && (!metadata || Array.isArray(metadata) || typeof metadata !== "object"
      || Object.keys(metadata).length > 32
      || Object.entries(metadata).some(([key, value]) => !value || key !== value.id
        || value.revision !== state.components[key] || !/^[a-f0-9]{64}$/.test(value.sha256 || "")
        || !/^[a-f0-9]{64}$/.test(value.baseFingerprint || "") || !/^[a-f0-9]{64}$/.test(value.tag || "")
        || typeof value.pythonAbi !== "string" || !value.packages || Array.isArray(value.packages)
        || Object.entries(value.packages).length > 256
        || Object.entries(value.packages).some(([name, version]) => !name || typeof version !== "string" || !version)))) throw new Error();
    return { systemVersion: parts.join("."), dataProfile: state.dataProfile, dirty: state.dirty, components: state.components, ...(metadata !== undefined ? { componentMetadata: metadata } : {}) };
  } catch {
    // Runtime errors may contain local paths or service configuration. Expose
    // only the stable reason, and never substitute cached/app-version state.
    throw Object.assign(new Error("data_unknown"), { code: "data_unknown" });
  }
}
module.exports = { readRuntimeUpdateState };
