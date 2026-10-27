const { execFileSync } = require("node:child_process");

function guardDesktopStartup({ isPackaged, sidecarPath, args, env, invoke = execFileSync }) {
  if (!isPackaged) return;
  try {
    if (args?.[0] !== "startup-guard") throw new Error("invalid_guard");
    const output = invoke(sidecarPath, args, {
      env, encoding: "utf8", shell: false, timeout: 15000,
      maxBuffer: 64 * 1024, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
    });
    if (JSON.parse(output)?.status !== "allowed") throw new Error("blocked");
  } catch {
    throw new Error("update_startup_blocked");
  }
}
module.exports = { guardDesktopStartup };
