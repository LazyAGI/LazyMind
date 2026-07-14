const path = require("node:path");

function resolveWindowsDesktopPaths(env, homeDir) {
  const configuredLocalAppData = String(env.LOCALAPPDATA || "").trim();
  const localAppData = configuredLocalAppData || path.win32.join(homeDir, "AppData", "Local");
  const runtimeRoot = path.win32.join(localAppData, "LazyMind");

  return {
    runtimeRoot,
    profileDir: path.win32.join(runtimeRoot, "Desktop"),
    logsDir: path.win32.join(runtimeRoot, "Logs", "desktop"),
    crashDumpsDir: path.win32.join(runtimeRoot, "Logs", "crash-dumps"),
  };
}

module.exports = { resolveWindowsDesktopPaths };
