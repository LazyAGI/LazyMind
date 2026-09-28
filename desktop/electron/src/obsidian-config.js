const fs = require("node:fs");
const path = require("node:path");

const CONFIG_VERSION = 1;

function emptyObsidianConfig() {
  return {
    version: CONFIG_VERSION,
    root: "",
    updatedAt: "",
  };
}

function normalizeRoot(root) {
  return typeof root === "string" ? root.trim() : "";
}

function loadObsidianConfig(configPath) {
  try {
    const parsed = JSON.parse(fs.readFileSync(configPath, "utf8"));
    return {
      version: CONFIG_VERSION,
      root: normalizeRoot(parsed?.root),
      updatedAt: typeof parsed?.updatedAt === "string" ? parsed.updatedAt : "",
    };
  } catch {
    return emptyObsidianConfig();
  }
}

function saveObsidianConfig(configPath, root) {
  const state = {
    version: CONFIG_VERSION,
    root: normalizeRoot(root),
    updatedAt: new Date().toISOString(),
  };
  const temporaryPath = `${configPath}.${process.pid}.tmp`;
  fs.mkdirSync(path.dirname(configPath), { recursive: true });
  fs.writeFileSync(temporaryPath, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
  if (process.platform !== "win32") {
    fs.chmodSync(temporaryPath, 0o600);
  }
  fs.renameSync(temporaryPath, configPath);
  return state;
}

function clearObsidianConfig(configPath) {
  return saveObsidianConfig(configPath, "");
}

function isExistingDirectory(root) {
  if (!root) return false;
  try {
    return fs.statSync(root).isDirectory();
  } catch {
    return false;
  }
}

function resolveRuntimeRoot(root, disabledRoot) {
  return isExistingDirectory(root) ? root : disabledRoot;
}

function buildObsidianRuntimeEnv(root, disabledRoot) {
  const runtimeRoot = resolveRuntimeRoot(root, disabledRoot);
  return {
    LAZYLLM_OBSIDIAN_VAULT_PATH: runtimeRoot,
    LAZYLLM_OBSIDIAN_HOST_ROOT: runtimeRoot,
  };
}

module.exports = {
  buildObsidianRuntimeEnv,
  clearObsidianConfig,
  emptyObsidianConfig,
  isExistingDirectory,
  loadObsidianConfig,
  resolveRuntimeRoot,
  saveObsidianConfig,
};
