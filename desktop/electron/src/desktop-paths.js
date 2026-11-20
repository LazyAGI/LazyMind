const path = require("node:path");

function resolveWindowsDesktopPaths(env, homeDir) {
  const configuredLocalAppData = String(env.LOCALAPPDATA || "").trim();
  const localAppData = configuredLocalAppData || path.win32.join(homeDir, "AppData", "Local");
  let runtimeRoot = path.win32.join(localAppData, "LazyMind");
  if (Object.prototype.hasOwnProperty.call(env, "LAZYMIND_DESKTOP_DATA_ROOT")) {
    const configuredRoot = String(env.LAZYMIND_DESKTOP_DATA_ROOT || "").trim();
    if (!/^[a-z]:[\\/]/i.test(configuredRoot) || !path.win32.isAbsolute(configuredRoot)) {
      throw new Error("LazyMind DataRoot must be an absolute local drive path");
    }
    runtimeRoot = path.win32.normalize(configuredRoot);
  }

  return {
    runtimeRoot,
    profileDir: path.win32.join(runtimeRoot, "Desktop"),
    logsDir: path.win32.join(runtimeRoot, "Logs", "desktop"),
    crashDumpsDir: path.win32.join(runtimeRoot, "Logs", "crash-dumps"),
  };
}

module.exports = { resolveWindowsDesktopPaths };
