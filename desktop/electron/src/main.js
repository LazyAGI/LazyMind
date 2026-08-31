const { app, BrowserWindow, ipcMain, shell, dialog, clipboard, Menu, Tray, session, net } = require("electron");
const { spawn, execFile } = require("node:child_process");
const { createHmac, randomBytes, randomUUID } = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { resolveWindowsDesktopPaths } = require("./desktop-paths");
const { resolveRuntimeLocalFile } = require("./runtime-local-file");
const {
  desktopRuntimeReady,
  isRuntimeOwnershipConflict,
  runtimeExitFailureMessage,
  statusFailureMessage,
} = require("./runtime-status");
const {
  createStartupMetricsRecorder,
  runtimeCapabilityReady,
  writeStartupMetrics,
} = require("./startup-metrics");
const {
  installerWarmupWebPreferences,
  macWarmupCompleted,
  macWarmupMarkerPath,
  markMacWarmupCompleted,
  runInstallerWarmupLifecycle,
} = require("./installer-warmup");
const { clearFrontendCaches } = require("./frontend-cache");
const { installExternalNavigationHandler } = require("./external-navigation");
const {
  collapseRoots,
  containsPath,
  discoverRecommendedFolders,
  loadAccessState,
  recommendationsForExactFolders,
  resolveExistingDirectories,
  saveAccessState,
} = require("./local-folder-access");

const isWindows = process.platform === "win32";
const isMac = process.platform === "darwin";
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
const desktopCredentialIdentityPath = path.join(app.getPath("userData"), "credential-device.json");
const localFolderAccessStatePath = path.join(app.getPath("userData"), "local-folder-access.json");
const cursorWorkspaceStorageRoot = path.join(
  app.getPath("appData"),
  "Cursor",
  "User",
  "workspaceStorage",
);
const startupLogPath = path.join(desktopLogsDir, "desktop-startup.log");
const sidecarPath = process.env.LAZYMIND_DESKTOP_SIDECAR ||
  path.join(runtimeResourcesRoot, "bin", `local-runtime-manager${isWindows ? ".exe" : ""}`);
const editablePptDependencyConfigPath = path.join(
  runtimeResourcesRoot,
  "config",
  "editable-ppt-dependencies.json",
);
const agentConnectorPath = process.env.LAZYMIND_DESKTOP_AGENT_CONNECTOR ||
  path.join(runtimeResourcesRoot, "bin", `lazymind${isWindows ? ".exe" : ""}`);
const maxStartupLogEntries = 1200;
const maxSidecarFailureBytes = 32 * 1024;
const desktopShutdownTimeout = process.env.LAZYMIND_DESKTOP_SHUTDOWN_TIMEOUT || "20s";
const forceExitDelayMs = 1500;
const rendererReadyTimeoutMs = 120 * 1000;
const runtimeOwnershipHandoffTimeoutMs = 30 * 1000;
const agentHostRestartMaxDelayMs = 30 * 1000;
const agentHostStableAfterMs = 60 * 1000;
const agentConnectorActionTimeoutMs = 15 * 1000;
const agentConnectorBindingTimeoutMs = 30 * 1000;
const macInstallationWarmupMarker = macWarmupMarkerPath(app.getPath("userData"));
const startupMetricsHistoryPath = path.join(desktopLogsDir, "startup-metrics.jsonl");
const startupMetricsRecorder = createStartupMetricsRecorder({
  metadata: {
    launchId: randomUUID(),
    launchKind: isInstallerWarmup
      ? "installer-warmup"
      : (
        isMac && isPackaged && !macWarmupCompleted(macInstallationWarmupMarker, app.getVersion())
          ? "first-launch"
          : "normal"
      ),
    appVersion: app.getVersion(),
    platform: process.platform,
    arch: process.arch,
    packaged: isPackaged,
  },
});

let mainWindow;
let startupWindow;
let windowCreationPromise;
let frontendOpeningAllowed = false;
let tray;
let rendererReadyWait;
let runtimeProcess;
let agentHostProcess;
let agentHostRestartTimer;
let agentHostStableTimer;
let agentHostRestartAttempts = 0;
const agentLoginProcesses = new Map();
let runtimeProcessExit = null;
let sidecarStderrTail = "";
let sidecarStructuredFailure = "";
let sidecarEventBuffer = "";
let homeReadyStatus = null;
let homeReadyWaiters = [];
let guardProcess;
let guardPID = 0;
let guardWatchTimer;
let currentStatus = null;
let ownerReleaseRetries = 0;
let isQuitting = false;
let allowWindowClose = false;
let windowHiddenByUser = false;
let startupLogEntries = [];
let startupLogWriteFailed = false;
let lastStartupError = null;
let loggedPortResolutions = new Set();
let startupState = {
  status: "starting",
  phase: "Initializing",
  message: "Starting local desktop runtime...",
  progress: null,
  startedAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

function loadEditablePptDependencyConfig() {
  try {
    const config = JSON.parse(fs.readFileSync(editablePptDependencyConfigPath, "utf8"));
    return {
      windowsX64: config?.windowsX64 || {},
      darwinArm64: config?.darwinArm64 || {},
    };
  } catch (error) {
    if (error?.code !== "ENOENT") {
      appendStartupLog("desktop", `editable PPT dependency config unavailable: ${error.message}`);
    }
    return { windowsX64: {}, darwinArm64: {} };
  }
}

const editablePptDependencyConfig = loadEditablePptDependencyConfig();

function loadOrCreateDesktopCredentialIdentity() {
  fs.mkdirSync(path.dirname(desktopCredentialIdentityPath), { recursive: true });
  try {
    const parsed = JSON.parse(fs.readFileSync(desktopCredentialIdentityPath, "utf8"));
    if (parsed?.version === 1 && parsed?.deviceId && parsed?.deviceSecret) {
      return parsed;
    }
  } catch (error) {
    if (error?.code !== "ENOENT") {
      throw new Error(`Failed to read desktop credential identity: ${error.message}`);
    }
  }
  const identity = {
    version: 1,
    deviceId: randomUUID(),
    deviceSecret: randomBytes(32).toString("base64url"),
  };
  fs.writeFileSync(desktopCredentialIdentityPath, `${JSON.stringify(identity)}\n`, { mode: 0o600, flag: "wx" });
  return identity;
}

function deriveDesktopCredentialKey(identity, purpose) {
  return createHmac("sha256", Buffer.from(identity.deviceSecret, "base64url"))
    .update(`lazymind/desktop/${identity.deviceId}/${purpose}/v1`)
    .digest("base64url");
}

const desktopCredentialIdentity = loadOrCreateDesktopCredentialIdentity();

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
  const localFolderAccess = loadAccessState(localFolderAccessStatePath);
  const env = {
    ...process.env,
    LAZYMIND_RUNTIME_PROFILE: "desktop",
    LAZYMIND_RUNTIME_OWNER_TOKEN: ownerToken,
    LAZYMIND_DESKTOP_APP_VERSION: app.getVersion(),
    LAZYMIND_DESKTOP_OWNER_PID: String(process.pid),
    LAZYMIND_RUNTIME_RESOURCES_ROOT: runtimeResourcesRoot,
    LAZYMIND_LOCAL_NETWORK_PROFILE: "localhost",
    LAZYMIND_LOCAL_PORTS_PINNED: "false",
    LAZYMIND_LOCAL_PROXY_ADDRESS: "127.0.0.1",
    LAZYMIND_LOCAL_AUTO_LOGIN_ALLOW_LAN: "false",
    LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED: "false",
    LAZYMIND_NODE_EXECUTABLE: process.execPath,
    LAZYMIND_NODE_RUN_AS_NODE: "true",
    VITE_LAZYMIND_MODE: "desktop",
    PYTHONDONTWRITEBYTECODE: "1",
    LAZYMIND_FILE_WATCHER_EXTRA_ALLOWED_ROOTS_JSON: JSON.stringify(localFolderAccess.allowedRoots),
  };
  env.LAZYMIND_MODEL_PROVIDER_SECRET_KEY ||= deriveDesktopCredentialKey(desktopCredentialIdentity, "model-provider");
  env.LAZYMIND_MCP_SECRET_KEY ||= deriveDesktopCredentialKey(desktopCredentialIdentity, "mcp");
  env.LAZYMIND_AUTH_CLOUD_SECRET_KEY ||= deriveDesktopCredentialKey(desktopCredentialIdentity, "cloud-oauth");
  env.LAZYMIND_EDITABLE_PPT_WINDOWS_X64_URL ||= editablePptDependencyConfig.windowsX64.url || "";
  env.LAZYMIND_EDITABLE_PPT_WINDOWS_X64_SHA256 ||= editablePptDependencyConfig.windowsX64.sha256 || "";
  env.LAZYMIND_EDITABLE_PPT_DARWIN_ARM64_URL ||= editablePptDependencyConfig.darwinArm64.url || "";
  env.LAZYMIND_EDITABLE_PPT_DARWIN_ARM64_SHA256 ||= editablePptDependencyConfig.darwinArm64.sha256 || "";
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
    return 1800;
  }
  const value = Number.parseInt(argv[index + 1], 10);
  return Number.isFinite(value) && value > 0 ? value : 1800;
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

function finishStartupMetrics(outcome, failureCode) {
  const metrics = startupMetricsRecorder.finish(outcome, failureCode);
  if (!metrics) {
    return;
  }
  try {
    writeStartupMetrics(startupMetricsHistoryPath, metrics);
  } catch (error) {
    console.error("Failed to write LazyMind startup metrics:", error);
  }
}

function resetStartupLogsForRun() {
  startupLogEntries = [];
  startupLogWriteFailed = false;
  lastStartupError = null;
  loggedPortResolutions = new Set();
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

function publishHomeReady(frontendPort) {
  if (homeReadyStatus || !Number.isInteger(frontendPort) || frontendPort <= 0) {
    return;
  }
  homeReadyStatus = { config: { frontendPort } };
  startupMetricsRecorder.mark("homeReadySignal");
  const waiters = homeReadyWaiters;
  homeReadyWaiters = [];
  waiters.forEach((resolve) => resolve(homeReadyStatus));
}

function waitForHomeReadySignal() {
  if (homeReadyStatus) {
    return Promise.resolve(homeReadyStatus);
  }
  return new Promise((resolve) => homeReadyWaiters.push(resolve));
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
      if (event?.event === "capability.ready" && event?.capability === "home") {
        publishHomeReady(Number(event.frontendPort));
      }
      if (event?.event === "phase.progress" && event?.phase === "python-payload") {
        const progress = {
          stage: String(event.stage || "extracting"),
          completedFiles: Number(event.completedFiles || 0),
          totalFiles: Number(event.totalFiles || 0),
          completedBytes: Number(event.completedBytes || 0),
          totalBytes: Number(event.totalBytes || 0),
          completedRoots: Number(event.completedRoots || 0),
          totalRoots: Number(event.totalRoots || 0),
          retryAttempt: Number(event.retryAttempt || 0),
          retryElapsedMs: Number(event.retryElapsedMs || 0),
        };
        const waiting = progress.stage === "waiting";
        const finalizing = progress.stage === "installing";
        const opening = progress.stage === "opening";
        updateStartupState({
          status: "starting",
          phase: waiting
            ? "Waiting for Windows"
            : (finalizing
              ? "Finalizing Python"
              : (opening ? "Reading Python archive" : "Preparing Python")),
          message: waiting
            ? "Python files are ready. Waiting for Windows to release them..."
            : (finalizing
              ? "Installing the bundled Python runtime..."
              : (opening
                ? "Reading the bundled Python archive..."
                : "Preparing the bundled Python runtime for first use...")),
          progress,
        });
      }
      if (["phase.completed", "phase.failed"].includes(event?.event) && event?.phase === "python-payload") {
        updateStartupState({ progress: null });
      }
      if (event?.phase === "history-injection-payload" && event?.event === "phase.started") {
        updateStartupState({
          status: "starting",
          phase: "Preparing sample conversations",
          message: "Verifying and unpacking the bundled sample conversations...",
          progress: null,
        });
      }
      if (event?.phase === "history-injection-payload" && event?.event === "phase.completed") {
        updateStartupState({
          status: "starting",
          phase: "Sample conversations ready",
          message: "Starting the local services and importing sample conversations...",
          progress: null,
        });
      }
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
    progress: null,
    error: lastStartupError,
  });
  finishStartupMetrics("failed", "runtime-startup");
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
      startupMetricsHistoryPath,
    },
    status: currentStatus,
    metrics: startupMetricsRecorder.snapshot(),
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

function runAgentConnector(agent, action) {
  const allowedActions = {
    all: new Set(["status"]),
    codex: new Set(["connect", "status", "disconnect", "login"]),
    cursor: new Set(["connect", "status", "disconnect", "login"]),
    workbuddy: new Set(["connect", "status", "disconnect"]),
    raccoon: new Set(["connect", "status", "disconnect"]),
    traework: new Set(["connect", "status", "disconnect"]),
    "deepseek-harness": new Set(["connect", "status", "disconnect"]),
  };
  if (!allowedActions[agent]?.has(action)) {
    return Promise.reject(new Error(`Unsupported external Agent action: ${agent}/${action}`));
  }
  if (action === "login") {
    return startAgentLogin(agent);
  }
  return runConnectorJSON(
    ["internal", "agent", agent, action],
    agentConnectorActionTimeoutMs,
  );
}

function startAgentLogin(agent) {
  agentLoginProcesses.get(agent)?.kill();
  const child = spawn(agentConnectorPath, ["internal", "agent", agent, "login"], {
    env: sidecarEnv(),
    stdio: ["ignore", "ignore", "pipe"],
    detached: false,
    windowsHide: isWindows,
  });
  agentLoginProcesses.set(agent, child);
  child.stderr?.on("data", (chunk) => appendStartupLog(`agent-login:${agent}`, chunk));
  child.once("error", (error) => {
    appendStartupLog(`agent-login:${agent}`, serializeError(error));
  });
  child.once("close", (code, signal) => {
    if (agentLoginProcesses.get(agent) === child) {
      agentLoginProcesses.delete(agent);
    }
    appendStartupLog(`agent-login:${agent}`, `exited with code ${code ?? "null"} signal ${signal ?? "null"}`);
    if (!isQuitting) {
      restartAgentHost();
    }
  });
  return runConnectorJSON(
    ["internal", "agent", agent, "status"],
    agentConnectorActionTimeoutMs,
  );
}

async function runExecutorConnector(provider, action) {
  const allowedActions = {
    all: new Set(["status"]),
    codex: new Set(["status", "enable", "disable"]),
    cursor: new Set(["status", "enable", "disable"]),
    workbuddy: new Set(["status", "enable", "disable"]),
  };
  if (!allowedActions[provider]?.has(action)) {
    throw new Error(`Unsupported external executor action: ${provider}/${action}`);
  }
  const result = await runConnectorJSON(
    ["internal", "executor", provider, action],
    agentConnectorActionTimeoutMs,
  );
  if (action !== "status") {
    restartAgentHost();
  }
  return result;
}

const agentBindingTargets = new Set([
  "codex-cli", "codex-desktop", "cursor-cli", "codebuddy-cli", "cursor-desktop",
  "workbuddy-desktop", "raccoon-desktop", "traework-desktop",
]);
const agentBindingActions = new Set(["status", "set", "clear"]);

async function runAgentBinding(target, action, executablePath = "") {
  if (!agentBindingTargets.has(target) || !agentBindingActions.has(action)) {
    throw new Error(`Unsupported external Agent binding action: ${target}/${action}`);
  }
  const args = ["internal", "binding", target, action];
  if (action === "set") {
    args.push("--path", executablePath);
  }
  const result = await runConnectorJSON(args, agentConnectorBindingTimeoutMs);
  if (action !== "status" && target.endsWith("-cli")) {
    restartAgentHost();
  }
  return result;
}

function readAgentBindings() {
  return runConnectorJSON(["internal", "binding", "all", "status"], agentConnectorActionTimeoutMs);
}

function restartAgentHost() {
  agentHostRestartAttempts = 0;
  if (agentHostProcess) {
    agentHostProcess.kill();
  } else {
    startAgentHost();
  }
}

function runConnectorJSON(args, timeout) {
  return new Promise((resolve, reject) => {
    execFile(agentConnectorPath, args, {
      env: sidecarEnv(),
      timeout,
      windowsHide: isWindows,
    }, (error, stdout, stderr) => {
      if (error) {
        error.message = String(stderr || stdout || error.message).trim();
        reject(error);
        return;
      }
      try {
        resolve(JSON.parse(stdout));
      } catch (parseError) {
        reject(new Error(`LazyMind connector returned invalid JSON: ${parseError.message}`));
      }
    });
  });
}

function scheduleAgentHostRestart() {
  if (isQuitting || isInstallerWarmup || agentHostRestartTimer) {
    return;
  }
  const delay = Math.min(1000 * (2 ** Math.min(agentHostRestartAttempts, 5)), agentHostRestartMaxDelayMs);
  agentHostRestartAttempts += 1;
  appendStartupLog("agent-host", `restarting external Agent host in ${delay}ms`);
  agentHostRestartTimer = setTimeout(() => {
    agentHostRestartTimer = undefined;
    startAgentHost();
  }, delay);
  agentHostRestartTimer.unref?.();
}

function startAgentHost() {
  if (agentHostProcess || isQuitting || isInstallerWarmup || !fs.existsSync(agentConnectorPath)) {
    return;
  }
  clearTimeout(agentHostRestartTimer);
  agentHostRestartTimer = undefined;
  const child = spawn(agentConnectorPath, ["agent", "host", "run", "--provider", "all"], {
    env: sidecarEnv(),
    stdio: ["ignore", "ignore", "pipe"],
    detached: false,
    windowsHide: isWindows,
  });
  agentHostProcess = child;
  clearTimeout(agentHostStableTimer);
  agentHostStableTimer = setTimeout(() => {
    agentHostRestartAttempts = 0;
  }, agentHostStableAfterMs);
  agentHostStableTimer.unref?.();
  child.stderr?.on("data", (chunk) => appendStartupLog("agent-host", chunk));
  child.once("error", (error) => {
    appendStartupLog("agent-host", `could not start external Agent host: ${serializeError(error)}`);
  });
  child.once("close", (code, signal) => {
    appendStartupLog("agent-host", `external Agent host exited with code ${code ?? "null"} signal ${signal ?? "null"}`);
    clearTimeout(agentHostStableTimer);
    agentHostStableTimer = undefined;
    if (agentHostProcess === child) {
      agentHostProcess = undefined;
    }
    scheduleAgentHostRestart();
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
      webPreferences: installerWarmupWebPreferences(),
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
        warmupWindow.webContents.session.webRequest.onBeforeRequest(null);
        warmupWindow.destroy();
      }
    },
    log,
    formatError: serializeError,
  });
}

async function createInstallationWarmupWindow() {
  const isChinese = app.getLocale().toLowerCase().startsWith("zh");
  updateStartupState({
    status: "starting",
    phase: isChinese ? "首次启动准备" : "First-launch preparation",
    message: isChinese
      ? "正在准备本地运行环境，完成后将自动打开 LazyMind…"
      : "Preparing the local runtime. LazyMind will open automatically when it is ready…",
    error: null,
  });
  appendStartupLog("desktop", "showing first-launch preparation window");
  const window = new BrowserWindow(browserWindowOptions(true));
  startupWindow = window;
  window.once("closed", () => {
    if (startupWindow === window) {
      startupWindow = undefined;
    }
  });
  attachManagedClose(window);
  await window.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(loadingHTML())}`);
  broadcastStartupDiagnostics();
  return window;
}

function disposeInstallationWarmupWindow(window) {
  if (window && !window.isDestroyed()) {
    window.destroy();
  }
  if (startupWindow === window) {
    startupWindow = undefined;
  }
}

async function runMacInstallationWarmupIfNeeded() {
  const version = app.getVersion();
  if (!isMac || !isPackaged || macWarmupCompleted(macInstallationWarmupMarker, version)) {
    return;
  }
  startupMetricsRecorder.mark("macWarmupStarted");
  const warmupWindow = await createInstallationWarmupWindow();
  try {
    await runInstallerWarmup();
    markMacWarmupCompleted(macInstallationWarmupMarker, version);
    startupMetricsRecorder.mark("macWarmupCompleted");
  } finally {
    disposeInstallationWarmupWindow(warmupWindow);
  }
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
  startupMetricsRecorder.observeStatus(currentStatus);
  return currentStatus;
}

function localFolderAccessSnapshot() {
  const state = loadAccessState(localFolderAccessStatePath);
  return {
    ...state,
    available: true,
    items: recommendationsForExactFolders(state.allowedRoots),
  };
}

function localFolderDiscoveryExcludedRoots() {
  const roots = [];
  for (const name of ["desktop", "documents", "downloads", "music", "pictures", "videos"]) {
    try {
      roots.push(app.getPath(name));
    } catch {
      // Older Electron/platform combinations may not expose every known path.
    }
  }
  return collapseRoots(roots);
}

function runtimeAllowedRoots(status) {
  const watcher = status?.config?.fileWatcher || {};
  return collapseRoots([
    ...(Array.isArray(watcher.allowedRoots) ? watcher.allowedRoots : []),
    watcher.watchHostDir,
  ]);
}

function fileWatcherAgentToken() {
  return process.env.LAZYMIND_FILE_WATCHER_AGENT_TOKEN ||
    process.env.LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN ||
    "my-secret-token";
}

async function replaceFileWatcherAllowedRoots(status, roots) {
  const port = Number(status?.config?.fileWatcher?.port || 0);
  if (!Number.isInteger(port) || port <= 0) {
    throw new Error("LazyMind file-watcher port is unavailable");
  }
  const response = await fetch(`http://127.0.0.1:${port}/api/v1/desktop/fs/allowed-roots`, {
    method: "PUT",
    headers: {
      Authorization: `Bearer ${fileWatcherAgentToken()}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ roots }),
    signal: AbortSignal.timeout(10_000),
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload?.message || `Could not update local folder access (HTTP ${response.status})`);
  }
  return collapseRoots(payload?.roots || roots);
}

function resolveRequestedLocalFolder(folderPath, status, accessState) {
  const requested = String(folderPath || "").trim();
  if (!requested) {
    throw new Error("Folder path is required");
  }

  const discoveryRoots = accessState.discoveryRoots || [];
  const allowedRoots = runtimeAllowedRoots(status);
  const requestedExists = fs.existsSync(requested);
  if (requestedExists && [...discoveryRoots, ...allowedRoots].some((root) => containsPath(root, requested))) {
    return resolveExistingDirectories([requested])[0];
  }

  const watchHostDir = status?.config?.fileWatcher?.watchHostDir;
  if (watchHostDir) {
    const virtualSuffix = requested.replace(/^[/\\]+/u, "");
    const mapped = virtualSuffix ? path.join(watchHostDir, virtualSuffix) : watchHostDir;
    if (fs.existsSync(mapped) && allowedRoots.some((root) => containsPath(root, mapped))) {
      return resolveExistingDirectories([mapped])[0];
    }
  }

  const resolved = resolveExistingDirectories([requested])[0];
  if (![...discoveryRoots, ...allowedRoots].some((root) => containsPath(root, resolved))) {
    throw new Error(`Folder is outside the authorized discovery locations: ${resolved}`);
  }
  return resolved;
}

async function restartRuntimeAfterFolderAccessChange() {
  await runSidecar("down");
  detachRuntimeMonitor();
  startRuntime();
  return waitForRuntimeReady();
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
  startupMetricsRecorder.mark("runtimeStartRequested");
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
    progress: null,
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
  finishStartupMetrics("cancelled", "app-quit-during-startup");
  appendStartupLog("desktop", `quitting LazyMind Desktop (${reason}); runtime cleanup continues in background`);
  clearTimeout(agentHostRestartTimer);
  clearTimeout(agentHostStableTimer);
  agentHostRestartTimer = undefined;
  agentHostStableTimer = undefined;
  agentHostProcess?.kill();
  agentHostProcess = undefined;
  for (const child of agentLoginProcesses.values()) {
    child.kill();
  }
  agentLoginProcesses.clear();
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

function enterBackgroundMode(reason, { discoverable }) {
  if (isInstallerWarmup || isQuitting) {
    return;
  }
  windowHiddenByUser = true;
  finishStartupMetrics("cancelled", "frontend-closed-to-background");
  rendererReadyWait?.cancel();
  rendererReadyWait = undefined;
  const windows = [mainWindow, startupWindow];
  mainWindow = undefined;
  startupWindow = undefined;
  for (const window of windows) {
    if (window && !window.isDestroyed()) {
      window.removeAllListeners("close");
      window.destroy();
    }
  }
  if (discoverable) {
    ensureWindowsTray();
  } else {
    if (isMac) {
      app.hide();
      if (app.dock) {
        app.dock.hide();
      }
    }
    destroyWindowsTray();
  }
  appendStartupLog(
    "desktop",
    `frontend closed (${reason}); Electron and runtime continue in ${discoverable ? "discoverable" : "hidden"} background mode`,
  );
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

async function waitForRuntimeReady(options = {}) {
  const targetCapability = options.capability || "";
  startRuntime();
  const deadline = Date.now() + 30 * 60 * 1000;
  let nextStatusErrorLogAt = 0;
  let ownershipConflictStartedAt = 0;
  while (Date.now() < deadline) {
    try {
      const status = await readStatus();
      const belongsToDesktop = status.profile === "desktop" &&
        sameRuntimePath(status.resourcesRoot, runtimeResourcesRoot);
      if (status.overallStatus === "ready" && !belongsToDesktop) {
        throw new Error(`A ${status.profile || "different"} LazyMind runtime is already running. Stop it before opening Desktop.`);
      }
      const ownedReady = targetCapability
        ? (
          belongsToDesktop &&
          status.ownerMatched &&
          status.config?.frontendPort &&
          runtimeCapabilityReady(status, targetCapability)
        )
        : desktopRuntimeReady(status, belongsToDesktop);
      const observedStatus = runtimeProcess && !status.ownerMatched &&
        ["failed", "stale", "stopped"].includes(status.overallStatus)
        ? "starting"
        : (status.overallStatus || "starting");
      const activeProgress = startupState.progress;
      const phase = ownedReady
        ? (targetCapability ? "Interface ready" : "Ready")
        : (activeProgress ? startupState.phase : `Waiting (${observedStatus})`);
      updateStartupState({
        status: ownedReady && !targetCapability
          ? "ready"
          : observedStatus,
        phase,
        message: activeProgress
          ? startupState.message
          : (ownedReady
          ? (
            targetCapability
              ? "Opening LazyMind while AI services continue to initialize..."
              : "Desktop runtime is ready."
          )
          : "Starting local desktop runtime..."),
      });
      if (status.config?.portResolutions?.length) {
        for (const resolution of status.config.portResolutions) {
          const key = [
            resolution.name,
            resolution.envName,
            resolution.requestedPort,
            resolution.resolvedPort,
            resolution.reason,
          ].join(":");
          if (loggedPortResolutions.has(key)) {
            continue;
          }
          loggedPortResolutions.add(key);
          appendStartupLog(
            "status",
            `port moved: ${resolution.name} ${resolution.requestedPort} -> ${resolution.resolvedPort} (${resolution.reason})`,
          );
        }
      }
      if (ownedReady && status.config?.frontendPort) {
        if (!targetCapability) {
          startupMetricsRecorder.mark("runtimeReady");
          startGuard();
          updateStartupState({ status: "ready", phase: "Ready", message: "LazyMind is ready." });
        }
        return status;
      }
      if (runtimeProcessExit && belongsToDesktop && !status.ownerMatched && status.overallStatus === "stopped" && ownerReleaseRetries < 1) {
        ownerReleaseRetries += 1;
        runtimeProcessExit = null;
        ownershipConflictStartedAt = 0;
        appendStartupLog("desktop", "previous Desktop instance finished cleanup; retrying runtime startup");
        startRuntime();
        continue;
      }
      const ownershipConflict = isRuntimeOwnershipConflict(runtimeProcessExit);
      if (ownershipConflict && ownershipConflictStartedAt === 0) {
        ownershipConflictStartedAt = Date.now();
        appendStartupLog("desktop", "previous Desktop instance is still cleaning up; waiting for runtime ownership release");
      }
      const waitingForOwnershipRelease =
        ownershipConflict &&
        Date.now() - ownershipConflictStartedAt < runtimeOwnershipHandoffTimeoutMs;
      if (waitingForOwnershipRelease) {
        updateStartupState({
          status: "starting",
          phase: "Waiting for previous instance",
          message: "Finishing cleanup from the previous LazyMind window...",
        });
      }
      const exitFailure = runtimeExitFailureMessage(status, belongsToDesktop, runtimeProcessExit);
      if (exitFailure && !waitingForOwnershipRelease) {
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

function waitForDesktopHomeReady() {
  return Promise.race([
    waitForHomeReadySignal(),
    waitForRuntimeReady({ capability: "home" }),
  ]);
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
    ::selection { background: #bfdbfe; color: #172554; }
    main {
      height: 100vh;
      display: grid;
      place-items: center;
      padding-bottom: 76px;
    }
    body.drawer-open main { padding-bottom: 450px; }
    section { width: min(500px, calc(100vw - 64px)); }
    h1 { font-size: 24px; font-weight: 650; margin: 0 0 12px; letter-spacing: 0; }
    p { font-size: 14px; line-height: 1.6; color: #4b5563; margin: 0; }
    .bar { height: 4px; background: #dbeafe; overflow: hidden; margin-top: 22px; border-radius: 2px; }
    .bar-fill {
      width: 100%;
      height: 100%;
      background: #2563eb;
      border-radius: inherit;
      transform-origin: left center;
    }
    .bar.indeterminate .bar-fill {
      width: 42%;
      animation: move 1.2s infinite ease-in-out;
    }
    .bar.determinate .bar-fill {
      transform: scaleX(0);
      transition: transform 180ms cubic-bezier(0.16, 1, 0.3, 1);
    }
    body.failed .bar { background: #fee2e2; }
    body.failed .bar-fill { background: #dc2626; animation: none; transform: scaleX(1) !important; width: 100%; }
    @keyframes move { 0% { transform: translateX(-100%); } 100% { transform: translateX(240%); } }
    .progress-meta {
      display: flex;
      justify-content: space-between;
      gap: 20px;
      margin-top: 9px;
      color: #64748b;
      font-size: 12px;
      line-height: 1.4;
      font-variant-numeric: tabular-nums;
    }
    .progress-meta[hidden] { display: none; }
    .progress-value { flex: 0 0 auto; color: #475569; }
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
    @media (prefers-reduced-motion: reduce) {
      .bar.indeterminate .bar-fill { animation-duration: 2.4s; }
      .bar.determinate .bar-fill { transition: none; }
      .drawer { transition: none; }
    }
  </style>
</head>
<body>
  <main>
    <section>
      <h1>LazyMind</h1>
      <p id="message">Starting local desktop runtime...</p>
      <div id="progressBar" class="bar indeterminate" role="progressbar" aria-label="Startup progress">
        <div id="progressFill" class="bar-fill"></div>
      </div>
      <div id="progressMeta" class="progress-meta" aria-live="polite" hidden>
        <span id="progressLabel">Preparing Python</span>
        <span id="progressValue" class="progress-value"></span>
      </div>
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
      progressBar: document.getElementById("progressBar"),
      progressFill: document.getElementById("progressFill"),
      progressMeta: document.getElementById("progressMeta"),
      progressLabel: document.getElementById("progressLabel"),
      progressValue: document.getElementById("progressValue"),
      phase: document.getElementById("phase"),
      log: document.getElementById("log"),
      steps: document.getElementById("steps"),
      copyLogs: document.getElementById("copyLogs"),
      openLogs: document.getElementById("openLogs"),
      openData: document.getElementById("openData"),
    };
    let expanded = false;
    let snapshot = null;
    const numberFormat = new Intl.NumberFormat();
    const stepNames = [
      ["process-supervisor", "Process supervisor"],
      ["local-proxy", "Local gateway"],
      ["auth-service", "Auth service"],
      ["channel-gateway", "Channel gateway"],
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
    function renderProgress(progress) {
      if (!progress) {
        els.progressBar.classList.remove("determinate");
        els.progressBar.classList.add("indeterminate");
        els.progressBar.removeAttribute("aria-valuenow");
        els.progressBar.removeAttribute("aria-valuemin");
        els.progressBar.removeAttribute("aria-valuemax");
        els.progressFill.style.transform = "";
        els.progressMeta.hidden = true;
        return;
      }
      const totalFiles = Math.max(0, Number(progress.totalFiles || 0));
      const completedFiles = Math.max(0, Number(progress.completedFiles || 0));
      const ratio = totalFiles > 0 ? completedFiles / totalFiles : 0;
      const complete = totalFiles > 0 && completedFiles >= totalFiles;
      const percent = complete
        ? 100
        : Math.max(0, Math.min(99, Math.floor(ratio * 100)));
      els.progressBar.classList.remove("indeterminate");
      els.progressBar.classList.add("determinate");
      els.progressBar.setAttribute("aria-valuemin", "0");
      els.progressBar.setAttribute("aria-valuemax", "100");
      els.progressBar.setAttribute("aria-valuenow", String(percent));
      els.progressFill.style.transform = "scaleX(" + (percent / 100) + ")";
      els.progressMeta.hidden = false;
      if (progress.stage === "waiting") {
        els.progressLabel.textContent = "Waiting for Windows to release Python files";
        els.progressValue.textContent = percent + "% · " + (progress.retryAttempt > 0
          ? "Retry " + numberFormat.format(progress.retryAttempt)
          : "Finalizing");
        return;
      }
      if (progress.stage === "installing") {
        els.progressLabel.textContent = "Finalizing Python runtime";
        els.progressValue.textContent = percent + "% · " + (progress.totalRoots > 0
          ? numberFormat.format(progress.completedRoots) + " / " + numberFormat.format(progress.totalRoots) + " steps"
          : "Finalizing");
        return;
      }
      if (progress.stage === "opening") {
        els.progressLabel.textContent = "Reading bundled Python archive";
        els.progressValue.textContent = "0%";
        return;
      }
      els.progressLabel.textContent = "Extracting bundled Python";
      els.progressValue.textContent = percent + "% · " +
        numberFormat.format(completedFiles) + " / " + numberFormat.format(totalFiles) + " files";
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
      renderProgress(startup.progress);
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
    icon: windowsDesktopIconPath(),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
      backgroundThrottling: false,
    },
  };
}

function attachExternalNavigationHandler(window) {
  installExternalNavigationHandler(
    window.webContents,
    (url) => shell.openExternal(url),
    (error) => appendStartupLog("error", `failed to open external URL: ${serializeError(error)}`),
  );
}

function windowsDesktopIconPath() {
  if (!isWindows) {
    return undefined;
  }
  return isPackaged
    ? path.join(process.resourcesPath, "LazyMind.ico")
    : process.env.LAZYMIND_DESKTOP_WINDOWS_ICON;
}

function destroyWindowsTray() {
  if (!tray) {
    return;
  }
  tray.destroy();
  tray = undefined;
}

function ensureWindowsTray() {
  if (!isWindows || tray) {
    return;
  }
  const iconPath = windowsDesktopIconPath();
  if (!iconPath) {
    appendStartupLog("desktop", "Windows tray icon is unavailable");
    return;
  }
  try {
    tray = new Tray(iconPath);
    tray.setToolTip("LazyMind");
    tray.on("click", () => {
      void showActiveWindow();
    });
    tray.setContextMenu(Menu.buildFromTemplate([
      {
        label: "Open LazyMind",
        click: () => {
          void showActiveWindow();
        },
      },
      { type: "separator" },
      {
        label: "Exit",
        click: () => {
          enterBackgroundMode("tray exit", { discoverable: false });
        },
      },
    ]));
  } catch (error) {
    tray = undefined;
    appendStartupLog("desktop", `could not create Windows tray icon: ${serializeError(error)}`);
  }
}

function attachManagedClose(window) {
  window.on("close", (event) => {
    if (allowWindowClose) {
      return;
    }
    event.preventDefault();
    enterBackgroundMode("window close", { discoverable: true });
  });
}

function activeWindow() {
  if (startupWindow && !startupWindow.isDestroyed()) {
    return startupWindow;
  }
  if (mainWindow && !mainWindow.isDestroyed()) {
    return mainWindow;
  }
  return undefined;
}

function showActiveWindow() {
  windowHiddenByUser = false;
  if (isMac) {
    app.show();
    if (app.dock) {
      void app.dock.show();
    }
  }
  if (!frontendOpeningAllowed) {
    return Promise.resolve();
  }
  const window = activeWindow();
  if (window && !window.isDestroyed()) {
    if (window.isMinimized()) {
      window.restore();
    }
    window.show();
    window.focus();
    if (window === mainWindow) {
      startupMetricsRecorder.mark("mainWindowVisible");
    }
    return Promise.resolve();
  }

  if (windowCreationPromise) {
    return windowCreationPromise;
  }
  appendStartupLog(
    "desktop",
    runtimeProcess
      ? "opening frontend window from resident runtime"
      : "opening frontend window and starting runtime",
  );
  const creation = createWindow();
  windowCreationPromise = creation;
  void creation
    .catch((error) => {
      if (!windowHiddenByUser && !isQuitting) {
        setStartupFailure(error);
      }
    })
    .finally(() => {
      if (windowCreationPromise === creation) {
        windowCreationPromise = undefined;
      }
      if (!windowHiddenByUser && !isQuitting && !activeWindow()) {
        void showActiveWindow();
      }
    });
  return creation;
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
    rejectPromise(
      new Error(`LazyMind Chat did not render within ${rendererReadyTimeoutMs / 1000} seconds`),
    );
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
  const nextStartupWindow = new BrowserWindow(browserWindowOptions(true));
  let nextMainWindow;
  let nextRendererReadyWait;
  startupWindow = nextStartupWindow;
  nextStartupWindow.once("closed", () => {
    if (startupWindow === nextStartupWindow) {
      startupWindow = undefined;
    }
  });
  startupMetricsRecorder.mark("startupWindowCreated");
  if (nextStartupWindow.isVisible()) {
    startupMetricsRecorder.mark("windowVisible");
  } else {
    nextStartupWindow.once("show", () => startupMetricsRecorder.mark("windowVisible"));
  }
  attachManagedClose(nextStartupWindow);
  startRuntime();
  await nextStartupWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(loadingHTML())}`);
  startupMetricsRecorder.mark("startupPageLoaded");
  broadcastStartupDiagnostics();
  try {
    const status = await waitForDesktopHomeReady();
    if (isQuitting || windowHiddenByUser || nextStartupWindow.isDestroyed()) {
      return;
    }
    nextMainWindow = new BrowserWindow(browserWindowOptions(false));
    startAgentHost();
    attachExternalNavigationHandler(nextMainWindow);
    mainWindow = nextMainWindow;
    nextMainWindow.once("closed", () => {
      if (mainWindow === nextMainWindow) {
        mainWindow = undefined;
      }
    });
    startupMetricsRecorder.mark("mainWindowCreated");
    attachManagedClose(nextMainWindow);
    nextRendererReadyWait = createRendererReadyWait(nextMainWindow);
    rendererReadyWait = nextRendererReadyWait;
    startupMetricsRecorder.mark("frontendLoadStarted");
    await Promise.all([
      nextMainWindow.loadURL(`http://127.0.0.1:${status.config.frontendPort}/agent/chat/home`),
      nextRendererReadyWait.promise,
    ]);
    nextRendererReadyWait.cancel();
    if (rendererReadyWait === nextRendererReadyWait) {
      rendererReadyWait = undefined;
    }
    if (isQuitting || windowHiddenByUser || nextMainWindow.isDestroyed()) {
      return;
    }
    nextStartupWindow.removeAllListeners("close");
    nextStartupWindow.hide();
    nextMainWindow.show();
    startupMetricsRecorder.mark("mainWindowVisible");
    nextMainWindow.focus();
    appendStartupLog("desktop", "frontend window ready");
    nextStartupWindow.destroy();
    void waitForRuntimeReady().then(
      () => finishStartupMetrics("success"),
      (error) => {
        if (!isQuitting) {
          setStartupFailure(error);
        }
      },
    );
  } catch (error) {
    nextRendererReadyWait?.cancel();
    if (rendererReadyWait === nextRendererReadyWait) {
      rendererReadyWait = undefined;
    }
    if (nextMainWindow && !nextMainWindow.isDestroyed()) {
      nextMainWindow.removeAllListeners("close");
      nextMainWindow.destroy();
    }
    if (mainWindow === nextMainWindow) {
      mainWindow = undefined;
    }
    if (windowHiddenByUser && !isQuitting) {
      return;
    }
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
  startupMetricsRecorder.mark("rendererReady");
  rendererReadyWait.notify();
});

ipcMain.handle("lazymind:runtimeStatus", () => readStatus());
ipcMain.handle("lazymind:agentIntegrationStatuses", () => runAgentConnector("all", "status"));
ipcMain.handle("lazymind:agentIntegrationAction", (_event, agent, action) => runAgentConnector(agent, action));
ipcMain.handle("lazymind:executorIntegrationPolicies", () => runExecutorConnector("all", "status"));
ipcMain.handle("lazymind:executorIntegrationAction", (_event, provider, action) => runExecutorConnector(provider, action));
ipcMain.handle("lazymind:agentExecutableBindings", () => readAgentBindings());
ipcMain.handle("lazymind:agentExecutableBind", (_event, target, executablePath) => runAgentBinding(target, "set", executablePath));
ipcMain.handle("lazymind:agentExecutableClear", (_event, target) => runAgentBinding(target, "clear"));
ipcMain.handle("lazymind:restartRuntime", async () => {
  return restartRuntimeAfterFolderAccessChange();
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
ipcMain.handle("lazymind:localFolderAccessStatus", () => localFolderAccessSnapshot());
ipcMain.handle("lazymind:chooseLocalDiscoveryRoots", async () => {
  const isChinese = app.getLocale().toLowerCase().startsWith("zh");
  const previous = loadAccessState(localFolderAccessStatePath);
  const consent = await dialog.showMessageBox(activeWindow(), {
    type: "question",
    title: isChinese ? "允许查找推荐目录" : "Allow recommended folder discovery",
    message: isChinese
      ? "是否允许 LazyMind 在你随后选择的位置中查找可接入目录？"
      : "Allow LazyMind to look for folders you can connect inside the locations you choose next?",
    detail: isChinese
      ? "查找会限制递归深度和耗时，只识别 Cursor、Codex 等已知目录；所选父目录不会加入 allowed_roots，也不会读取或同步文件内容。桌面、文档、下载及媒体目录会被跳过。"
      : "Discovery is bounded by depth and time and only recognizes known locations such as Cursor and Codex folders. Parent locations are not added to allowed_roots, and file contents are not read or synced. Desktop, Documents, Downloads, and media folders are skipped.",
    buttons: [isChinese ? "继续选择" : "Choose locations", isChinese ? "取消" : "Cancel"],
    defaultId: 0,
    cancelId: 1,
    noLink: true,
  });
  if (consent.response !== 0) {
    const saved = saveAccessState(localFolderAccessStatePath, {
      ...previous,
      discoveryConsentGranted: false,
    });
    return { ...saved, available: true, canceled: true };
  }
  const result = await dialog.showOpenDialog(activeWindow(), {
    title: isChinese ? "选择用于查找推荐目录的位置" : "Choose locations to search",
    message: isChinese
      ? "可一次选择多个位置。LazyMind 只会在这些位置查找可接入目录。"
      : "You can choose multiple locations. LazyMind only searches them for folders you can connect.",
    defaultPath: app.getPath("home"),
    buttonLabel: isChinese ? "允许查找" : "Allow search",
    properties: ["openDirectory", "multiSelections"],
  });
  if (result.canceled || result.filePaths.length === 0) {
    const saved = saveAccessState(localFolderAccessStatePath, {
      ...previous,
      discoveryConsentGranted: false,
    });
    return { ...saved, available: true, canceled: true };
  }
  const excludedRoots = localFolderDiscoveryExcludedRoots();
  const discoveryRoots = resolveExistingDirectories(
    collapseRoots([
      ...previous.discoveryRoots,
      ...result.filePaths,
    ]).filter((candidate) =>
      !excludedRoots.some((excludedRoot) => containsPath(excludedRoot, candidate))),
  );
  const saved = saveAccessState(localFolderAccessStatePath, {
    ...previous,
    discoveryConsentGranted: true,
    discoveryRoots,
  });
  return { ...saved, available: true, canceled: false };
});
ipcMain.handle("lazymind:discoverLocalFolders", async () => {
  const access = loadAccessState(localFolderAccessStatePath);
  if (!access.discoveryConsentGranted || access.discoveryRoots.length === 0) {
    return {
      ...access,
      available: true,
      items: [],
      scannedEntries: 0,
      truncated: false,
      stoppedReason: "not_authorized",
      durationMs: 0,
    };
  }
  const result = await discoverRecommendedFolders({
    roots: access.discoveryRoots,
    cursorWorkspaceStorageRoots: [cursorWorkspaceStorageRoot],
    excludedRoots: localFolderDiscoveryExcludedRoots(),
  });
  return { ...access, available: true, ...result };
});
ipcMain.handle("lazymind:authorizeLocalFolders", async (_event, requestedPaths) => {
  const pathsToAuthorize = Array.isArray(requestedPaths) ? requestedPaths : [];
  if (pathsToAuthorize.length === 0) {
    return { granted: true, addedRoots: [], ...localFolderAccessSnapshot() };
  }

  const status = await readStatus();
  const previous = loadAccessState(localFolderAccessStatePath);
  const resolved = collapseRoots(
    pathsToAuthorize.map((folderPath) =>
      resolveRequestedLocalFolder(folderPath, status, previous)),
  );
  const existing = collapseRoots([
    ...runtimeAllowedRoots(status),
    ...previous.allowedRoots,
  ]);
  const addedRoots = resolved.filter((candidate) =>
    !existing.some((root) => containsPath(root, candidate)));
  if (addedRoots.length === 0) {
    return { granted: true, addedRoots: [], ...localFolderAccessSnapshot() };
  }

  const allowedRoots = await replaceFileWatcherAllowedRoots(status, [
    ...runtimeAllowedRoots(status),
    ...previous.allowedRoots,
    ...addedRoots,
  ]);
  try {
    const saved = saveAccessState(localFolderAccessStatePath, {
      ...previous,
      allowedRoots,
    });
    return { granted: true, canceled: false, addedRoots, ...saved, available: true };
  } catch (error) {
    try {
      await replaceFileWatcherAllowedRoots(status, [
        ...runtimeAllowedRoots(status),
        ...previous.allowedRoots,
      ]);
    } catch (rollbackError) {
      appendStartupLog("error", `failed to restore local folder access after persistence failure: ${serializeError(rollbackError)}`);
    }
    throw error;
  }
});
ipcMain.handle("lazymind:selectFolder", async () => {
  const result = await dialog.showOpenDialog(activeWindow(), { properties: ["openDirectory"] });
  return result.canceled ? null : result.filePaths[0];
});
ipcMain.handle("lazymind:selectExecutable", async (_event, target = "") => {
  const agentExecutable = agentBindingTargets.has(target);
  const result = await dialog.showOpenDialog(activeWindow(), {
    properties: ["openFile"],
    filters: process.platform === "win32"
      ? [agentExecutable
        ? { name: "Agent executable", extensions: ["exe", "cmd", "bat"] }
        : { name: "FFmpeg", extensions: ["exe"] }]
      : [{ name: "Executable", extensions: ["*"] }],
  });
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
function safeArtifactFilename(name) {
  const base = path.basename(String(name || "").replace(/[\\/]/g, "_").trim());
  return base || "download";
}

function uniqueFilePath(dir, filename) {
  const safe = safeArtifactFilename(filename);
  const ext = path.extname(safe);
  const stem = path.basename(safe, ext);
  let dest = path.join(dir, safe);
  let index = 1;
  while (fs.existsSync(dest)) {
    dest = path.join(dir, `${stem} (${index})${ext}`);
    index += 1;
  }
  return dest;
}

function toNodeBuffer(data) {
  if (data == null) {
    return null;
  }
  if (Buffer.isBuffer(data)) {
    return data;
  }
  if (data instanceof Uint8Array) {
    return Buffer.from(data);
  }
  if (data instanceof ArrayBuffer) {
    return Buffer.from(data);
  }
  return null;
}

function artifactSearchRoots() {
  const roots = [];
  const dataDir = currentDataDir();
  if (dataDir) {
    roots.push(dataDir);
  }
  const composeDataDir = repoRoot ? path.join(repoRoot, "data") : "";
  if (composeDataDir && composeDataDir !== dataDir) {
    roots.push(composeDataDir);
  }
  return roots;
}

async function resolveExistingArtifactPath(source) {
  try {
    await readStatus();
  } catch {
    // Keep local-file actions usable even when status refresh fails.
  }
  for (const root of artifactSearchRoots()) {
    const localPath = resolveRuntimeLocalFile(root, source);
    if (!localPath) {
      continue;
    }
    try {
      const info = await fs.promises.stat(localPath);
      if (info.isFile()) {
        return localPath;
      }
    } catch {
      // Try the next known data root.
    }
  }
  return "";
}

async function materializeArtifactFile(payload, destPath) {
  const buffer = toNodeBuffer(payload?.data);
  if (buffer) {
    await fs.promises.mkdir(path.dirname(destPath), { recursive: true });
    await fs.promises.writeFile(destPath, buffer);
    return destPath;
  }
  const source = String(payload?.source || "").trim();
  const localPath = await resolveExistingArtifactPath(source);
  if (localPath) {
    if (path.resolve(localPath) === path.resolve(destPath)) {
      return destPath;
    }
    await fs.promises.mkdir(path.dirname(destPath), { recursive: true });
    await fs.promises.copyFile(localPath, destPath);
    return destPath;
  }
  if (!/^https?:\/\//i.test(source)) {
    throw new Error("Artifact file is not available locally");
  }
  const response = await net.fetch(source);
  if (!response.ok) {
    throw new Error(`Failed to download artifact file (${response.status})`);
  }
  const bytes = Buffer.from(await response.arrayBuffer());
  await fs.promises.mkdir(path.dirname(destPath), { recursive: true });
  await fs.promises.writeFile(destPath, bytes);
  return destPath;
}

ipcMain.handle("lazymind:showItemInFolder", async (_event, payload) => {
  const source = typeof payload === "string" ? payload : String(payload?.source || "");
  const localPath = await resolveExistingArtifactPath(source);
  if (localPath) {
    shell.showItemInFolder(localPath);
    return { ok: true, path: localPath };
  }
  const dest = path.join(
    app.getPath("temp"),
    "lazymind-artifacts",
    safeArtifactFilename(typeof payload === "object" ? payload?.filename : ""),
  );
  await materializeArtifactFile(payload, dest);
  shell.showItemInFolder(dest);
  return { ok: true, path: dest };
});

ipcMain.handle("lazymind:saveFileAs", async (_event, payload) => {
  const filename = safeArtifactFilename(payload?.filename);
  const result = await dialog.showSaveDialog(activeWindow(), {
    defaultPath: filename,
  });
  if (result.canceled || !result.filePath) {
    return { canceled: true };
  }
  await materializeArtifactFile(payload, result.filePath);
  return { ok: true, path: result.filePath };
});

ipcMain.handle("lazymind:downloadFile", async (_event, payload) => {
  const dest = uniqueFilePath(app.getPath("downloads"), payload?.filename);
  await materializeArtifactFile(payload, dest);
  return { ok: true, path: dest };
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
    desktopStartupMetrics: startupMetricsHistoryPath,
    startupMetrics: startupMetricsRecorder.snapshot(),
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
    void showActiveWindow();
  });
  app.on("activate", () => {
    void showActiveWindow();
  });
  app.whenReady().then(async () => {
    startupMetricsRecorder.mark("electronReady");
    try {
      await clearFrontendCaches(session.defaultSession, (message) => appendStartupLog("desktop", message));
    } catch (error) {
      appendStartupLog("error", `failed to clear frontend caches: ${serializeError(error)}`);
    }
    if (isWindows) {
      app.setAppUserModelId("ai.lazymind.desktop");
    }
    if (isInstallerWarmup) {
      startupMetricsRecorder.mark("installerWarmupStarted");
      return runInstallerWarmup().then(
        () => {
          startupMetricsRecorder.mark("installerWarmupCompleted");
          finishStartupMetrics("success");
          app.exit(0);
        },
        (error) => {
          finishStartupMetrics("failed", "installer-warmup");
          console.error("LazyMind installer warmup failed:", error);
          app.exit(1);
        },
      );
    }
    return runMacInstallationWarmupIfNeeded().then(
      () => {
        frontendOpeningAllowed = true;
        if (windowHiddenByUser) {
          return undefined;
        }
        return showActiveWindow();
      },
      (error) => {
        finishStartupMetrics("failed", "mac-installation-warmup");
        console.error("LazyMind macOS installation warmup failed:", error);
        dialog.showErrorBox(
          "LazyMind installation warmup failed",
          `LazyMind could not prepare its local runtime. Reopen the app to retry.\n\n${serializeError(error)}`,
        );
        app.exit(1);
      },
    );
  });
  app.on("window-all-closed", () => {
    // Normal Desktop sessions stay resident without renderer processes.
    // Installer warmup owns its explicit app.exit lifecycle.
  });
  app.on("before-quit", (event) => {
    if (isInstallerWarmup) {
      return;
    }
    if (!isQuitting) {
      event.preventDefault();
      enterBackgroundMode("app quit", { discoverable: false });
    }
  });
}
