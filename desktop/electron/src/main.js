const { app, BrowserWindow, ipcMain, shell, dialog, clipboard } = require("electron");
const { spawn, execFile } = require("node:child_process");
const { randomUUID } = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { resolveWindowsDesktopPaths } = require("./desktop-paths");
const { desktopRuntimeReady, runtimeExitFailureMessage, statusFailureMessage } = require("./runtime-status");
const { runInstallerWarmupLifecycle } = require("./installer-warmup");

const isWindows = process.platform === "win32";
const isInstallerWarmup = isWindows && process.argv.includes("--installer-warmup");
const windowsDesktopPaths = isWindows
  ? resolveWindowsDesktopPaths(process.env, app.getPath("home"))
  : null;
if (windowsDesktopPaths) {
  for (const dir of [
    windowsDesktopPaths.profileDir,
    windowsDesktopPaths.logsDir,
    windowsDesktopPaths.crashDumpsDir,
  ]) {
    fs.mkdirSync(dir, { recursive: true });
  }
  app.setPath("userData", windowsDesktopPaths.profileDir);
  app.setPath("sessionData", windowsDesktopPaths.profileDir);
  app.setPath("crashDumps", windowsDesktopPaths.crashDumpsDir);
  app.setAppLogsPath(windowsDesktopPaths.logsDir);
}

const isPackaged = app.isPackaged;
const desktopTarget = isWindows ? "windows-x64" : "darwin-arm64";
const ownerToken = randomUUID();
const runtimeResourcesRoot = process.env.LAZYMIND_DESKTOP_RESOURCES_ROOT ||
  (isPackaged
    ? path.join(process.resourcesPath, "runtime")
    : path.resolve(__dirname, "..", "..", "build", desktopTarget, "runtime"));
const repoRoot = process.env.LAZYMIND_DESKTOP_REPO_ROOT ||
  (isPackaged ? path.join(runtimeResourcesRoot, "app") : path.resolve(__dirname, "..", "..", ".."));
const explicitRuntimeRoot = process.env.LAZYMIND_DESKTOP_RUNTIME_ROOT || "";
const desktopLogsDir = app.getPath("logs");
const startupLogPath = path.join(desktopLogsDir, "desktop-startup.log");
const sidecarPath = process.env.LAZYMIND_DESKTOP_SIDECAR ||
  path.join(runtimeResourcesRoot, "bin", `local-runtime-manager${isWindows ? ".exe" : ""}`);
const maxStartupLogEntries = 1200;
const maxSidecarFailureBytes = 32 * 1024;
const desktopShutdownTimeout = process.env.LAZYMIND_DESKTOP_SHUTDOWN_TIMEOUT || "20s";
const forceExitDelayMs = 1500;
const rendererReadyTimeoutMs = 30 * 1000;

let mainWindow;
let startupWindow;
let rendererReadyWait;
let runtimeProcess;
let runtimeProcessExit = null;
let sidecarStderrTail = "";
let sidecarStructuredFailure = "";
let sidecarEventBuffer = "";
let guardProcess;
let guardPID = 0;
let guardWatchTimer;
let currentStatus = null;
let ownerReleaseRetries = 0;
let isQuitting = false;
let allowWindowClose = false;
let startupLogEntries = [];
let startupLogWriteFailed = false;
let lastStartupError = null;
let startupState = {
  status: "starting",
  phase: "Initializing",
  message: "Starting local desktop runtime...",
  startedAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

function sidecarArgs(command, extra = []) {
  const args = [
    command,
    "--profile", "desktop",
    "--repo-root", repoRoot,
    "--resources-root", runtimeResourcesRoot,
    "--owner-token", ownerToken,
  ];
  if (explicitRuntimeRoot) {
    args.push("--runtime-root", explicitRuntimeRoot);
  }
  return [...args, ...extra];
}

function sidecarEnv() {
  const env = {
    ...process.env,
    LAZYMIND_RUNTIME_PROFILE: "desktop",
    LAZYMIND_RUNTIME_OWNER_TOKEN: ownerToken,
    LAZYMIND_DESKTOP_OWNER_PID: String(process.pid),
    LAZYMIND_RUNTIME_RESOURCES_ROOT: runtimeResourcesRoot,
    LAZYMIND_LOCAL_NETWORK_PROFILE: "localhost",
    LAZYMIND_LOCAL_PROXY_ADDRESS: "127.0.0.1",
    LAZYMIND_LOCAL_AUTO_LOGIN_ALLOW_LAN: "false",
    VITE_LAZYMIND_MODE: "desktop",
    PYTHONDONTWRITEBYTECODE: "1",
  };
  if (explicitRuntimeRoot) {
    env.LAZYMIND_RUNTIME_ROOT = explicitRuntimeRoot;
  } else {
    delete env.LAZYMIND_RUNTIME_ROOT;
  }
  return env;
}

function sidecarShutdownEnv() {
  return {
    ...sidecarEnv(),
    LAZYMIND_LOCAL_DOWN_TIMEOUT: desktopShutdownTimeout,
  };
}

function installerWarmupTimeoutSeconds(argv = process.argv) {
  const index = argv.indexOf("--timeout-seconds");
  if (index < 0 || index + 1 >= argv.length) {
    return 900;
  }
  const value = Number.parseInt(argv[index + 1], 10);
  return Number.isFinite(value) && value > 0 ? value : 900;
}

function currentRuntimeRoot() {
  return currentStatus?.runtimeRoot || explicitRuntimeRoot || "";
}

function currentDataDir() {
  return currentStatus?.dataDir || (currentRuntimeRoot() ? path.join(currentRuntimeRoot(), "data") : "");
}

function currentRuntimeLogsDir() {
  return currentStatus?.logsDir || "";
}

function ensureDesktopLogDirs() {
  fs.mkdirSync(desktopLogsDir, { recursive: true });
}

function resetStartupLogsForRun() {
  startupLogEntries = [];
  startupLogWriteFailed = false;
  lastStartupError = null;
  try {
    ensureDesktopLogDirs();
    fs.writeFileSync(startupLogPath, "");
  } catch (error) {
    startupLogWriteFailed = true;
    console.error("Failed to reset LazyMind startup log:", error);
  }
}

function serializeError(error) {
  if (!error) {
    return "";
  }
  if (error.stack) {
    return String(error.stack);
  }
  if (error.message) {
    return String(error.message);
  }
  return String(error);
}

function trimLogLine(line) {
  return String(line || "").replace(/\s+$/, "");
}

function appendStartupLog(source, line) {
  const text = trimLogLine(line);
  if (!text) {
    return;
  }
  const entry = {
    ts: new Date().toISOString(),
    source,
    text,
  };
  startupLogEntries.push(entry);
  if (startupLogEntries.length > maxStartupLogEntries) {
    startupLogEntries = startupLogEntries.slice(-maxStartupLogEntries);
  }
  if (!startupLogWriteFailed) {
    try {
      ensureDesktopLogDirs();
      fs.appendFileSync(startupLogPath, `[${entry.ts}] [${source}] ${text}\n`);
    } catch (error) {
      startupLogWriteFailed = true;
      console.error("Failed to write LazyMind startup log:", error);
    }
  }
  broadcastStartupDiagnostics({ append: entry });
}

function appendStartupChunk(source, chunk) {
  String(chunk).split(/\r?\n/).forEach((line) => appendStartupLog(source, line));
}

function captureSidecarChunk(source, chunk) {
  const text = String(chunk);
  appendStartupChunk(source, text);
  if (source === "sidecar.stderr") {
    sidecarStderrTail = `${sidecarStderrTail}${text}`.slice(-maxSidecarFailureBytes);
  }
  if (source !== "sidecar.stdout") {
    return;
  }
  const eventText = `${sidecarEventBuffer}${text}`;
  const lines = eventText.split(/\r?\n/);
  sidecarEventBuffer = lines.pop() || "";
  for (const line of lines) {
    const marker = "[startup-event] ";
    const markerIndex = line.indexOf(marker);
    if (markerIndex < 0) {
      continue;
    }
    try {
      const event = JSON.parse(line.slice(markerIndex + marker.length));
      if (["phase.failed", "startup.failed"].includes(event?.event) && event?.error) {
        sidecarStructuredFailure = String(event.error);
      }
    } catch {
      // Keep the raw line in desktop-startup.log; stderr remains the fallback.
    }
  }
}

function sidecarFailureDetail() {
  return sidecarStructuredFailure.trim() || sidecarStderrTail.trim();
}

function updateStartupState(patch) {
  startupState = {
    ...startupState,
    ...patch,
    updatedAt: new Date().toISOString(),
  };
  broadcastStartupDiagnostics();
}

function setStartupFailure(error, message = "Desktop runtime failed to start") {
  const detail = serializeError(error);
  lastStartupError = detail || message;
  appendStartupLog("error", lastStartupError);
  updateStartupState({
    status: "failed",
    phase: "Failed",
    message,
    error: lastStartupError,
  });
}

function startupDiagnosticsSnapshot() {
  return {
    startup: startupState,
    logs: startupLogEntries,
    paths: {
      sidecarPath,
      resourcesRoot: runtimeResourcesRoot,
      repoRoot,
      runtimeRoot: currentRuntimeRoot(),
      dataDir: currentDataDir(),
      logsDir: currentRuntimeLogsDir(),
      desktopLogsDir,
      startupLogPath,
    },
    status: currentStatus,
    runtimeProcess: runtimeProcess
      ? { pid: runtimeProcess.pid, exited: false }
      : { pid: null, exited: Boolean(runtimeProcessExit), exit: runtimeProcessExit },
    lastStartupError,
  };
}

function broadcastStartupDiagnostics(extra = {}) {
  if (!startupWindow || startupWindow.isDestroyed()) {
    return;
  }
  startupWindow.webContents.send("lazymind:startupDiagnosticsUpdate", {
    ...startupDiagnosticsSnapshot(),
    ...extra,
  });
}

function runSidecar(command, extra = [], options = {}) {
  return new Promise((resolve, reject) => {
    execFile(sidecarPath, sidecarArgs(command, extra), {
      env: options.env || sidecarEnv(),
      timeout: options.timeout,
      windowsHide: isWindows,
    }, (error, stdout, stderr) => {
      if (error) {
        error.message = `${error.message}\n${stderr || ""}`;
        reject(error);
        return;
      }
      resolve(stdout);
    });
  });
}

async function runInstallerWarmup() {
  const timeoutSeconds = installerWarmupTimeoutSeconds();
  const maintenanceArgs = ["--maintenance", "installer-warmup"];
  const warmupLogPath = path.join(desktopLogsDir, "installer-warmup.log");
  const log = (message) => {
    fs.mkdirSync(desktopLogsDir, { recursive: true });
    fs.appendFileSync(warmupLogPath, `[${new Date().toISOString()}] ${message}\n`);
  };
  log(`starting offline installer warmup with timeout ${timeoutSeconds}s`);
  await runInstallerWarmupLifecycle({
    startRuntime: () => runSidecar("up", maintenanceArgs, {
      timeout: timeoutSeconds * 1000,
    }),
    readStatus,
    createRenderer: () => new BrowserWindow({
      show: false,
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
      },
    }),
    loadRenderer: async (warmupWindow, status) => {
      warmupWindow.webContents.session.webRequest.onBeforeRequest((details, callback) => {
        try {
          const url = new URL(details.url);
          const allowed = url.protocol === "data:" ||
            ((url.protocol === "http:" || url.protocol === "ws:") &&
              (url.hostname === "127.0.0.1" || url.hostname === "localhost"));
          callback({ cancel: !allowed });
        } catch {
          callback({ cancel: true });
        }
      });
      await warmupWindow.loadURL(`http://127.0.0.1:${status.config.frontendPort}`);
    },
    stopRuntime: () => runSidecar("down", maintenanceArgs, {
      env: { ...sidecarEnv(), LAZYMIND_LOCAL_DOWN_TIMEOUT: "120s" },
      timeout: 130000,
    }),
    disposeRenderer: (warmupWindow) => {
      if (!warmupWindow.isDestroyed()) {
        warmupWindow.destroy();
      }
    },
    log,
    formatError: serializeError,
  });
}

function startGuard() {
  if (guardProcess || guardPID || !fs.existsSync(sidecarPath)) {
    return;
  }
  ensureDesktopLogDirs();
  const shutdownLog = path.join(desktopLogsDir, "desktop-shutdown.log");
  let errFd = "ignore";
  try {
    errFd = fs.openSync(shutdownLog, "a");
    fs.appendFileSync(
      shutdownLog,
      `[${new Date().toISOString()}] [desktop] guard started for owner pid ${process.pid}; timeout=${desktopShutdownTimeout}\n`,
    );
  } catch (error) {
    if (typeof errFd === "number") {
      fs.closeSync(errFd);
    }
    appendStartupLog("error", `failed to open desktop runtime guard log: ${serializeError(error)}`);
    errFd = "ignore";
  }
  if (isWindows) {
    if (typeof errFd === "number") {
      fs.closeSync(errFd);
    }
    startWindowsGuard();
    return;
  }
  try {
    guardProcess = spawn(sidecarPath, sidecarArgs("guard", ["--owner-pid", String(process.pid)]), {
      env: sidecarShutdownEnv(),
      stdio: ["ignore", "ignore", errFd],
      detached: true,
      windowsHide: isWindows,
    });
  } catch (error) {
    appendStartupLog("error", `failed to start desktop runtime guard: ${serializeError(error)}`);
    return;
  } finally {
    if (typeof errFd === "number") {
      fs.closeSync(errFd);
    }
  }
  guardProcess.once("exit", (code, signal) => {
    appendStartupLog(
      "desktop",
      `runtime guard exited with code ${code ?? "null"} signal ${signal ?? "null"}`,
    );
    guardProcess = null;
  });
  guardProcess.unref();
}

function quoteWindowsArgument(value) {
  const input = String(value);
  if (input && !/[\s"]/u.test(input)) {
    return input;
  }
  let output = '"';
  let backslashes = 0;
  for (const char of input) {
    if (char === "\\") {
      backslashes += 1;
      continue;
    }
    if (char === '"') {
      output += "\\".repeat(backslashes * 2 + 1) + '"';
      backslashes = 0;
      continue;
    }
    output += "\\".repeat(backslashes) + char;
    backslashes = 0;
  }
  return output + "\\".repeat(backslashes * 2) + '"';
}

function startWindowsGuard() {
  const commandLine = [sidecarPath, ...sidecarArgs("guard", ["--owner-pid", String(process.pid)])]
    .map(quoteWindowsArgument)
    .join(" ");
  const encodedCommandLine = Buffer.from(commandLine, "utf8").toString("base64");
  const script = [
    `$commandLine = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('${encodedCommandLine}'))`,
    "$result = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = $commandLine }",
    "if ($result.ReturnValue -ne 0) { throw \"Win32_Process.Create failed with code $($result.ReturnValue)\" }",
    "$result.ProcessId",
  ].join("; ");
  const encodedScript = Buffer.from(script, "utf16le").toString("base64");
  guardProcess = execFile(
    "powershell.exe",
    ["-NoProfile", "-NonInteractive", "-EncodedCommand", encodedScript],
    { windowsHide: true },
    (error, stdout, stderr) => {
      guardProcess = null;
      if (error) {
        appendStartupLog("error", `failed to launch Windows runtime guard: ${serializeError(error)} ${stderr || ""}`);
        return;
      }
      const createdPID = Number.parseInt(String(stdout).trim(), 10);
      if (!Number.isInteger(createdPID) || createdPID <= 0) {
        appendStartupLog("error", `Windows runtime guard returned an invalid pid: ${String(stdout).trim()}`);
        return;
      }
      guardPID = createdPID;
      appendStartupLog("desktop", `Windows runtime guard running as pid ${guardPID}`);
      guardWatchTimer = setInterval(() => {
        try {
          process.kill(guardPID, 0);
        } catch {
          appendStartupLog("desktop", `Windows runtime guard pid ${guardPID} exited`);
          guardPID = 0;
          clearInterval(guardWatchTimer);
          guardWatchTimer = undefined;
        }
      }, 2000);
      guardWatchTimer.unref();
    },
  );
}

function detachRuntimeMonitor() {
  const proc = runtimeProcess;
  if (!proc) {
    return;
  }
  runtimeProcess = null;
  proc.stdout?.removeAllListeners("data");
  proc.stderr?.removeAllListeners("data");
  proc.removeAllListeners("exit");
  proc.removeAllListeners("error");
  proc.stdout?.destroy();
  proc.stderr?.destroy();
  try {
    if (isWindows) {
      proc.kill();
    } else {
      proc.kill("SIGTERM");
    }
  } catch (error) {
    appendStartupLog("error", `failed to stop desktop runtime monitor: ${serializeError(error)}`);
  }
  proc.unref?.();
}

function spawnDetachedShutdownHelper(reason) {
  if (!fs.existsSync(sidecarPath)) {
    return false;
  }
  ensureDesktopLogDirs();
  const shutdownLog = path.join(desktopLogsDir, "desktop-shutdown.log");
  const outFd = fs.openSync(shutdownLog, "a");
  const errFd = fs.openSync(shutdownLog, "a");
  try {
    fs.appendFileSync(
      shutdownLog,
      `[${new Date().toISOString()}] [desktop] detached shutdown requested: ${reason}; timeout=${desktopShutdownTimeout}\n`,
    );
    const child = spawn(sidecarPath, sidecarArgs("down"), {
      env: sidecarShutdownEnv(),
      stdio: ["ignore", outFd, errFd],
      detached: true,
      windowsHide: isWindows,
    });
    child.once("error", (error) => {
      appendStartupLog("error", `failed to spawn detached desktop shutdown: ${serializeError(error)}`);
    });
    child.unref();
    return true;
  } finally {
    fs.closeSync(outFd);
    fs.closeSync(errFd);
  }
}

async function readStatus() {
  const stdout = await runSidecar("status", ["--json"]);
  currentStatus = JSON.parse(stdout);
  return currentStatus;
}

function logStartupContext() {
  appendStartupLog("desktop", `sidecar: ${sidecarPath}`);
  appendStartupLog("desktop", `resources: ${runtimeResourcesRoot}`);
  appendStartupLog("desktop", `repo: ${repoRoot}`);
  appendStartupLog("desktop", explicitRuntimeRoot
    ? `runtime directory override: ${explicitRuntimeRoot}`
    : "runtime directory: delegated to local-runtime-manager");
  appendStartupLog("desktop", `desktop logs directory: ${desktopLogsDir}`);
}

function startRuntime() {
  if (runtimeProcess) {
    return;
  }
  resetStartupLogsForRun();
  ensureDesktopLogDirs();
  runtimeProcessExit = null;
  sidecarStderrTail = "";
  sidecarStructuredFailure = "";
  sidecarEventBuffer = "";
  updateStartupState({
    status: "starting",
    phase: "Starting sidecar",
    message: "Starting local desktop runtime...",
    error: null,
  });
  logStartupContext();
  appendStartupLog("desktop", `running: ${sidecarPath} ${sidecarArgs("up").join(" ")}`);
  runtimeProcess = spawn(sidecarPath, sidecarArgs("up"), {
    env: sidecarEnv(),
    stdio: ["ignore", "pipe", "pipe"],
    detached: false,
    windowsHide: isWindows,
  });
  runtimeProcess.stdout?.on("data", (chunk) => captureSidecarChunk("sidecar.stdout", chunk));
  runtimeProcess.stderr?.on("data", (chunk) => captureSidecarChunk("sidecar.stderr", chunk));
  runtimeProcess.once("error", (error) => {
    runtimeProcessExit = { error: serializeError(error), detail: serializeError(error) };
    runtimeProcess = null;
    setStartupFailure(error, "Could not start desktop runtime sidecar");
  });
  // `close` fires after stdout/stderr are drained, so the final Go error cannot
  // race with ownership/status handling below.
  runtimeProcess.once("close", (code, signal) => {
    const detail = sidecarFailureDetail() || runtimeProcessExit?.detail || "";
    runtimeProcessExit = { code, signal, at: new Date().toISOString(), detail };
    appendStartupLog("sidecar", `local-runtime-manager exited with code ${code ?? "null"} signal ${signal ?? "null"}`);
    runtimeProcess = null;
    if (startupState.status !== "ready" && startupState.status !== "failed") {
      updateStartupState({
        status: "exited",
        phase: "Sidecar exited",
        message: "Desktop runtime sidecar exited before the frontend became ready.",
      });
    }
  });
}

function beginFastQuit(reason = "quit") {
  if (isQuitting) {
    return;
  }
  isQuitting = true;
  allowWindowClose = true;
  appendStartupLog("desktop", `quitting LazyMind Desktop (${reason}); runtime cleanup continues in background`);
  const guardWillCleanUp = Boolean(guardPID || (!isWindows && guardProcess));
  if (!guardWillCleanUp) {
    spawnDetachedShutdownHelper(reason);
  }
  detachRuntimeMonitor();
  rendererReadyWait?.cancel();
  rendererReadyWait = undefined;
  for (const window of [mainWindow, startupWindow]) {
    if (window && !window.isDestroyed()) {
      window.destroy();
    }
  }
  setTimeout(() => {
    app.exit(0);
  }, forceExitDelayMs).unref();
  app.quit();
}

function sameRuntimePath(left, right) {
  if (!left || !right) {
    return false;
  }
  const normalizedLeft = path.resolve(String(left));
  const normalizedRight = path.resolve(String(right));
  return isWindows
    ? normalizedLeft.toLowerCase() === normalizedRight.toLowerCase()
    : normalizedLeft === normalizedRight;
}

async function waitForRuntimeReady() {
  startRuntime();
  const deadline = Date.now() + 30 * 60 * 1000;
  let nextStatusErrorLogAt = 0;
  while (Date.now() < deadline) {
    try {
      const status = await readStatus();
      const belongsToDesktop = status.profile === "desktop" &&
        sameRuntimePath(status.resourcesRoot, runtimeResourcesRoot);
      if (status.overallStatus === "ready" && !belongsToDesktop) {
        throw new Error(`A ${status.profile || "different"} LazyMind runtime is already running. Stop it before opening Desktop.`);
      }
      const ownedReady = desktopRuntimeReady(status, belongsToDesktop);
      const phase = ownedReady ? "Ready" : `Waiting (${status.overallStatus || "unknown"})`;
      updateStartupState({
        status: ownedReady ? "ready" : (status.overallStatus || "starting"),
        phase,
        message: ownedReady
          ? "Desktop runtime is ready."
          : "Starting local desktop runtime...",
      });
      if (status.config?.portResolutions?.length) {
        for (const resolution of status.config.portResolutions) {
          appendStartupLog(
            "status",
            `port moved: ${resolution.name} ${resolution.requestedPort} -> ${resolution.resolvedPort} (${resolution.reason})`,
          );
        }
      }
      if (ownedReady && status.config?.frontendPort) {
        startGuard();
        updateStartupState({ status: "ready", phase: "Ready", message: "Opening LazyMind..." });
        return status;
      }
      if (runtimeProcessExit && belongsToDesktop && !status.ownerMatched && status.overallStatus === "stopped" && ownerReleaseRetries < 1) {
        ownerReleaseRetries += 1;
        runtimeProcessExit = null;
        appendStartupLog("desktop", "previous Desktop instance finished cleanup; retrying runtime startup");
        startRuntime();
        continue;
      }
      const exitFailure = runtimeExitFailureMessage(status, belongsToDesktop, runtimeProcessExit);
      if (exitFailure) {
        throw new Error(exitFailure);
      }
    } catch (error) {
      if (Date.now() >= nextStatusErrorLogAt) {
        appendStartupLog("status", `status check failed: ${serializeError(error)}`);
        nextStatusErrorLogAt = Date.now() + 5000;
      }
      if (runtimeProcessExit) {
        throw error;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error("LazyMind desktop runtime did not become ready in time");
}

function loadingHTML() {
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>LazyMind</title>
  <style>
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f7f8fa;
      color: #1f2937;
      overflow: hidden;
    }
    main {
      height: 100vh;
      display: grid;
      place-items: center;
      padding-bottom: 76px;
      transition: padding-bottom 180ms ease;
    }
    body.drawer-open main { padding-bottom: 450px; }
    section { width: min(500px, calc(100vw - 64px)); }
    h1 { font-size: 24px; font-weight: 650; margin: 0 0 12px; letter-spacing: 0; }
    p { font-size: 14px; line-height: 1.6; color: #4b5563; margin: 0; }
    .bar { height: 4px; background: #dbeafe; overflow: hidden; margin-top: 22px; border-radius: 2px; }
    .bar::before {
      content: "";
      display: block;
      width: 42%;
      height: 100%;
      background: #2563eb;
      animation: move 1.2s infinite ease-in-out;
    }
    body.failed .bar { background: #fee2e2; }
    body.failed .bar::before { background: #dc2626; animation: none; width: 100%; }
    @keyframes move { 0% { transform: translateX(-100%); } 100% { transform: translateX(240%); } }
    .details-button {
      margin-top: 16px;
      border: 0;
      padding: 0;
      background: transparent;
      color: #2563eb;
      font: inherit;
      font-size: 13px;
      cursor: pointer;
    }
    .details-button:focus-visible,
    .icon-button:focus-visible {
      outline: 2px solid #93c5fd;
      outline-offset: 3px;
      border-radius: 4px;
    }
    .drawer {
      position: fixed;
      left: 0;
      right: 0;
      bottom: 0;
      height: 440px;
      background: #ffffff;
      border-top: 1px solid #d9dee7;
      transform: translateY(100%);
      transition: transform 180ms ease;
      display: grid;
      grid-template-rows: 48px 1fr;
      box-shadow: 0 -1px 0 rgba(15, 23, 42, 0.02);
    }
    .drawer.open { transform: translateY(0); }
    .drawer-header {
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 0 24px;
      border-bottom: 1px solid #edf0f5;
      min-width: 0;
    }
    .drawer-title { font-weight: 620; font-size: 14px; }
    .phase { color: #64748b; font-size: 13px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .spacer { flex: 1; min-width: 16px; }
    .icon-button {
      border: 1px solid #d8dee8;
      background: #fff;
      color: #334155;
      min-width: 32px;
      height: 28px;
      padding: 0 9px;
      border-radius: 6px;
      font-size: 12px;
      cursor: pointer;
    }
    .icon-button:hover { background: #f8fafc; }
    .drawer-body {
      display: grid;
      grid-template-columns: 360px 1fr;
      min-height: 0;
    }
    .summary {
      border-right: 1px solid #edf0f5;
      padding: 16px 20px;
      min-width: 0;
      overflow: auto;
    }
    .steps { display: grid; gap: 7px; }
    .step { display: flex; align-items: center; gap: 8px; color: #475569; font-size: 12px; min-width: 0; }
    .dot { width: 7px; height: 7px; border-radius: 50%; background: #cbd5e1; flex: 0 0 auto; }
    .step.running .dot { background: #2563eb; }
    .step.ready .dot { background: #16a34a; }
    .step.failed .dot { background: #dc2626; }
    .step-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .log {
      margin: 0;
      padding: 14px 18px;
      overflow: auto;
      min-width: 0;
      white-space: pre-wrap;
      word-break: break-word;
      color: #1f2937;
      background: #fbfcfe;
      font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    }
    .empty { color: #64748b; }
  </style>
</head>
<body>
  <main>
    <section>
      <h1>LazyMind</h1>
      <p id="message">Starting local desktop runtime...</p>
      <div class="bar"></div>
      <button id="toggleDetails" class="details-button" type="button">Show startup log</button>
    </section>
  </main>
  <aside id="drawer" class="drawer" aria-label="Startup log">
    <div class="drawer-header">
      <div class="drawer-title">Startup log</div>
      <div id="phase" class="phase">Initializing</div>
      <div class="spacer"></div>
      <button id="copyLogs" class="icon-button" type="button">Copy logs</button>
      <button id="openLogs" class="icon-button" type="button">Open logs</button>
      <button id="openData" class="icon-button" type="button">Open data</button>
      <button id="collapse" class="icon-button" type="button">Collapse</button>
    </div>
    <div class="drawer-body">
      <div class="summary">
        <div id="steps" class="steps"></div>
      </div>
      <pre id="log" class="log empty">Waiting for startup output...</pre>
    </div>
  </aside>
  <script>
    const bridge = window.lazymindDesktop || {};
    const els = {
      body: document.body,
      drawer: document.getElementById("drawer"),
      toggle: document.getElementById("toggleDetails"),
      collapse: document.getElementById("collapse"),
      message: document.getElementById("message"),
      phase: document.getElementById("phase"),
      log: document.getElementById("log"),
      steps: document.getElementById("steps"),
      copyLogs: document.getElementById("copyLogs"),
      openLogs: document.getElementById("openLogs"),
      openData: document.getElementById("openData"),
    };
    let expanded = false;
    let snapshot = null;
    const stepNames = [
      ["process-supervisor", "Process supervisor"],
      ["local-proxy", "Local gateway"],
      ["auth-service", "Auth service"],
      ["core", "Core"],
      ["scan-control-plane", "Scan control"],
      ["file-watcher", "File watcher"],
      ["milvus-lite", "Milvus Lite"],
      ["lazyllm-doc-server", "Doc server"],
      ["lazyllm-parse-server", "Processor server"],
      ["lazyllm-parse-worker", "Processor worker"],
      ["lazyllm-algo", "LazyLLM algo"],
      ["chat", "Chat router"],
      ["frontend", "Frontend"],
    ];
    function setExpanded(next) {
      expanded = next;
      els.drawer.classList.toggle("open", expanded);
      els.body.classList.toggle("drawer-open", expanded);
      els.toggle.textContent = expanded ? "Hide startup log" : "Show startup log";
    }
    function serviceClass(status) {
      if (status === "running" || status === "ready") return "ready";
      if (status === "failed" || status === "stale") return "failed";
      if (status === "starting") return "running";
      return "";
    }
    function render(next) {
      snapshot = next || snapshot;
      if (!snapshot) return;
      const startup = snapshot.startup || {};
      const status = snapshot.status || {};
      els.body.classList.toggle("failed", startup.status === "failed");
      if (startup.status === "failed" || startup.status === "exited") {
        setExpanded(true);
      }
      els.message.textContent = startup.message || "Starting local desktop runtime...";
      els.phase.textContent = startup.phase || startup.status || "Starting";
      const services = status.services || {};
      els.steps.innerHTML = stepNames.map(([key, label]) => {
        const serviceStatus = services[key]?.status || "pending";
        const klass = serviceClass(serviceStatus);
        return "<div class='step " + klass + "'><span class='dot'></span><span class='step-name'>" +
          label + " · " + serviceStatus + "</span></div>";
      }).join("");
      const logs = (snapshot.logs || []).map((entry) => {
        return "[" + (entry.ts || "").replace("T", " ").replace("Z", "") + "] [" + entry.source + "] " + entry.text;
      }).join("\\n");
      els.log.textContent = logs || "Waiting for startup output...";
      els.log.classList.toggle("empty", !logs);
      els.log.scrollTop = els.log.scrollHeight;
    }
    els.toggle.addEventListener("click", () => setExpanded(!expanded));
    els.collapse.addEventListener("click", () => setExpanded(false));
    els.copyLogs.addEventListener("click", async () => { if (bridge.copyStartupLogs) await bridge.copyStartupLogs(); });
    els.openLogs.addEventListener("click", async () => { if (bridge.openLogsDir) await bridge.openLogsDir(); });
    els.openData.addEventListener("click", async () => { if (bridge.openDataDir) await bridge.openDataDir(); });
    if (bridge.startupDiagnostics) {
      bridge.startupDiagnostics().then(render).catch(() => {});
    }
    if (bridge.onStartupDiagnosticsUpdate) {
      bridge.onStartupDiagnosticsUpdate(render);
    }
  </script>
</body>
</html>`;
}

function browserWindowOptions(show = true) {
  return {
    width: 1440,
    height: 960,
    minWidth: 1120,
    minHeight: 760,
    show,
    backgroundColor: "#f7f8fa",
    title: "LazyMind",
    icon: isWindows
      ? (isPackaged ? path.join(process.resourcesPath, "LazyMind.ico") : process.env.LAZYMIND_DESKTOP_WINDOWS_ICON)
      : undefined,
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  };
}

function attachManagedClose(window) {
  window.on("close", (event) => {
    if (allowWindowClose) {
      return;
    }
    event.preventDefault();
    beginFastQuit("window close");
  });
}

function activeWindow() {
  if (startupWindow && !startupWindow.isDestroyed() && startupWindow.isVisible()) {
    return startupWindow;
  }
  if (mainWindow && !mainWindow.isDestroyed()) {
    return mainWindow;
  }
  return startupWindow;
}

function createRendererReadyWait(window) {
  let settled = false;
  let resolvePromise;
  let rejectPromise;
  const promise = new Promise((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  const timer = setTimeout(() => {
    if (settled) return;
    settled = true;
    rejectPromise(new Error("LazyMind Chat did not render within 30 seconds"));
  }, rendererReadyTimeoutMs);
  return {
    window,
    promise,
    notify() {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolvePromise();
    },
    cancel() {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolvePromise();
    },
  };
}

async function createWindow() {
  startupWindow = new BrowserWindow(browserWindowOptions(true));
  attachManagedClose(startupWindow);
  await startupWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(loadingHTML())}`);
  broadcastStartupDiagnostics();
  try {
    const status = await waitForRuntimeReady();
    if (isQuitting) {
      return;
    }
    mainWindow = new BrowserWindow(browserWindowOptions(false));
    attachManagedClose(mainWindow);
    rendererReadyWait = createRendererReadyWait(mainWindow);
    await Promise.all([
      mainWindow.loadURL(`http://127.0.0.1:${status.config.frontendPort}/agent/chat/home`),
      rendererReadyWait.promise,
    ]);
    rendererReadyWait.cancel();
    rendererReadyWait = undefined;
    if (isQuitting) {
      return;
    }
    startupWindow.removeAllListeners("close");
    startupWindow.hide();
    mainWindow.show();
    mainWindow.focus();
    startupWindow.destroy();
    startupWindow = undefined;
  } catch (error) {
    rendererReadyWait?.cancel();
    rendererReadyWait = undefined;
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.removeAllListeners("close");
      mainWindow.destroy();
    }
    mainWindow = undefined;
    setStartupFailure(error);
  }
}

ipcMain.on("lazymind:renderer-ready", (event) => {
  if (!rendererReadyWait || rendererReadyWait.window.isDestroyed()) {
    return;
  }
  if (event.sender !== rendererReadyWait.window.webContents) {
    return;
  }
  rendererReadyWait.notify();
});

ipcMain.handle("lazymind:runtimeStatus", () => readStatus());
ipcMain.handle("lazymind:restartRuntime", async () => {
  await runSidecar("down");
  startRuntime();
  return waitForRuntimeReady();
});
ipcMain.handle("lazymind:resetRuntime", async (_event, scope = "kb") => {
  await runSidecar("reset", ["--scope", scope]);
  return readStatus();
});
ipcMain.handle("lazymind:openLogsDir", async () => {
  try {
    await readStatus();
  } catch {
    // Keep diagnostics usable even when the sidecar cannot start.
  }
  const target = currentRuntimeLogsDir() || desktopLogsDir;
  fs.mkdirSync(target, { recursive: true });
  await shell.openPath(target);
});
ipcMain.handle("lazymind:openDataDir", async () => {
  await readStatus();
  const target = currentDataDir();
  if (!target) {
    throw new Error("LazyMind runtime data directory is not available");
  }
  fs.mkdirSync(target, { recursive: true });
  await shell.openPath(target);
});
ipcMain.handle("lazymind:selectFolder", async () => {
  const result = await dialog.showOpenDialog(activeWindow(), { properties: ["openDirectory"] });
  return result.canceled ? null : result.filePaths[0];
});
ipcMain.handle("lazymind:startupDiagnostics", () => startupDiagnosticsSnapshot());
ipcMain.handle("lazymind:copyStartupLogs", () => {
  const text = startupLogEntries
    .map((entry) => `[${entry.ts}] [${entry.source}] ${entry.text}`)
    .join("\n");
  clipboard.writeText(text);
  return true;
});
ipcMain.handle("lazymind:exportDiagnostics", async () => {
  const status = currentStatus || await readStatus();
  const out = path.join(desktopLogsDir, "desktop-diagnostics.json");
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, JSON.stringify({
    status,
    runtimeResourcesRoot,
    repoRoot,
    runtimeRoot: currentRuntimeRoot(),
    dataDir: currentDataDir(),
    logsDir: currentRuntimeLogsDir(),
    desktopLogsDir,
    desktopStartupLog: startupLogPath,
    lastStartupError,
  }, null, 2));
  return out;
});

const hasSingleInstanceLock = app.requestSingleInstanceLock();
if (!hasSingleInstanceLock) {
  if (isInstallerWarmup) {
    app.exit(1);
  } else {
    app.quit();
  }
} else {
  app.on("second-instance", () => {
    const window = activeWindow();
    if (!window || window.isDestroyed()) {
      return;
    }
    if (window.isMinimized()) {
      window.restore();
    }
    window.show();
    window.focus();
  });
  app.whenReady().then(() => {
    if (isWindows) {
      app.setAppUserModelId("ai.lazymind.desktop");
    }
    if (isInstallerWarmup) {
      return runInstallerWarmup().then(
        () => app.exit(0),
        (error) => {
          console.error("LazyMind installer warmup failed:", error);
          app.exit(1);
        },
      );
    }
    return createWindow();
  });
  app.on("window-all-closed", () => {
    if (isInstallerWarmup) {
      return;
    }
    app.quit();
  });
  app.on("before-quit", (event) => {
    if (isInstallerWarmup) {
      return;
    }
    if (!isQuitting) {
      event.preventDefault();
      beginFastQuit("app quit");
    }
  });
}
