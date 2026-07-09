const { app, BrowserWindow, ipcMain, shell, dialog, clipboard } = require("electron");
const { spawn, execFile } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const isPackaged = app.isPackaged;
const runtimeResourcesRoot = process.env.LAZYMIND_DESKTOP_RESOURCES_ROOT ||
  (isPackaged
    ? path.join(process.resourcesPath, "runtime")
    : path.resolve(__dirname, "..", "..", "dist", "runtime"));
const repoRoot = process.env.LAZYMIND_DESKTOP_REPO_ROOT ||
  (isPackaged ? path.join(runtimeResourcesRoot, "app") : path.resolve(__dirname, "..", "..", ".."));
const runtimeRoot = process.env.LAZYMIND_DESKTOP_RUNTIME_ROOT ||
  path.join(app.getPath("userData"), "runtime");
const dataDir = path.join(runtimeRoot, "data");
const logsDir = path.join(runtimeRoot, "logs");
const startupLogPath = path.join(logsDir, "desktop-startup.log");
const sidecarPath = process.env.LAZYMIND_DESKTOP_SIDECAR ||
  path.join(runtimeResourcesRoot, "bin", "local-runtime-manager");
const maxStartupLogEntries = 1200;
const desktopShutdownTimeout = process.env.LAZYMIND_DESKTOP_SHUTDOWN_TIMEOUT || "20s";
const forceExitDelayMs = 1500;

let mainWindow;
let runtimeProcess;
let runtimeProcessExit = null;
let guardProcess;
let currentStatus = null;
let isQuitting = false;
let allowWindowClose = false;
let startupLogEntries = [];
let startupLogWriteFailed = false;
let lastStartupError = null;
let desktopAdminSessionPromise = null;
let startupState = {
  status: "starting",
  phase: "Initializing",
  message: "Starting local desktop runtime...",
  startedAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

function sidecarArgs(command, extra = []) {
  return [
    command,
    "--profile", "desktop",
    "--repo-root", repoRoot,
    "--runtime-root", runtimeRoot,
    "--resources-root", runtimeResourcesRoot,
    ...extra,
  ];
}

function sidecarEnv() {
  return {
    ...process.env,
    LAZYMIND_RUNTIME_PROFILE: "desktop",
    LAZYMIND_RUNTIME_ROOT: runtimeRoot,
    LAZYMIND_RUNTIME_RESOURCES_ROOT: runtimeResourcesRoot,
    VITE_LAZYMIND_MODE: "desktop",
    PYTHONDONTWRITEBYTECODE: "1",
  };
}

function sidecarShutdownEnv() {
  return {
    ...sidecarEnv(),
    LAZYMIND_LOCAL_DOWN_TIMEOUT: desktopShutdownTimeout,
  };
}

function ensureRuntimeDirs() {
  fs.mkdirSync(logsDir, { recursive: true });
  fs.mkdirSync(dataDir, { recursive: true });
  fs.mkdirSync(path.join(runtimeRoot, "run"), { recursive: true });
}

function resetStartupLogsForRun() {
  startupLogEntries = [];
  startupLogWriteFailed = false;
  lastStartupError = null;
  try {
    ensureRuntimeDirs();
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
      ensureRuntimeDirs();
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
      runtimeRoot,
      dataDir,
      logsDir,
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
  if (!mainWindow || mainWindow.isDestroyed()) {
    return;
  }
  mainWindow.webContents.send("lazymind:startupDiagnosticsUpdate", {
    ...startupDiagnosticsSnapshot(),
    ...extra,
  });
}

function runSidecar(command, extra = []) {
  return new Promise((resolve, reject) => {
    execFile(sidecarPath, sidecarArgs(command, extra), { env: sidecarEnv() }, (error, stdout, stderr) => {
      if (error) {
        error.message = `${error.message}\n${stderr || ""}`;
        reject(error);
        return;
      }
      resolve(stdout);
    });
  });
}

function startGuard() {
  if (guardProcess || !fs.existsSync(sidecarPath)) {
    return;
  }
  ensureRuntimeDirs();
  const shutdownLog = path.join(logsDir, "desktop-shutdown.log");
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
  try {
    guardProcess = spawn(sidecarPath, sidecarArgs("guard", ["--owner-pid", String(process.pid)]), {
      env: sidecarShutdownEnv(),
      stdio: ["ignore", "ignore", errFd],
      detached: true,
    });
  } catch (error) {
    appendStartupLog("error", `failed to start desktop runtime guard: ${serializeError(error)}`);
    return;
  } finally {
    if (typeof errFd === "number") {
      fs.closeSync(errFd);
    }
  }
  guardProcess.once("exit", () => {
    guardProcess = null;
  });
  guardProcess.unref();
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
    proc.kill("SIGTERM");
  } catch (error) {
    appendStartupLog("error", `failed to stop desktop runtime monitor: ${serializeError(error)}`);
  }
  proc.unref?.();
}

function spawnDetachedShutdownHelper(reason) {
  if (!fs.existsSync(sidecarPath)) {
    return false;
  }
  ensureRuntimeDirs();
  const shutdownLog = path.join(logsDir, "desktop-shutdown.log");
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

function unwrapApiResponse(payload) {
  if (payload && typeof payload === "object" && "data" in payload) {
    return payload.data;
  }
  return payload;
}

function desktopAuthBaseUrl(status) {
  const authPort = status?.config?.authService?.Port || status?.config?.authService?.port;
  if (!authPort) {
    throw new Error("Desktop auth-service port is not available");
  }
  return `http://127.0.0.1:${authPort}/api/authservice`;
}

async function fetchJson(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    signal: options.signal || AbortSignal.timeout(15000),
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
  });
  const text = await response.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = text;
    }
  }
  if (!response.ok) {
    const detail = payload?.detail || payload?.message || text || response.statusText;
    throw new Error(`Desktop auth request failed (${response.status}): ${detail}`);
  }
  return unwrapApiResponse(payload);
}

async function createDesktopAdminSession() {
  const status = currentStatus || await readStatus();
  const baseUrl = desktopAuthBaseUrl(status);
  const username = String(process.env.LAZYMIND_BOOTSTRAP_ADMIN_USERNAME || "admin");
  const password = String(process.env.LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD || "admin");

  const login = await fetchJson(`${baseUrl}/auth/login`, {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
  if (!login?.access_token) {
    throw new Error("Desktop admin login did not return an access token");
  }

  const currentUser = await fetchJson(`${baseUrl}/auth/me`, {
    headers: {
      Authorization: `Bearer ${login.access_token}`,
    },
  });

  return {
    token: login.access_token,
    refreshToken: login.refresh_token,
    username: currentUser?.username || username,
    userId: currentUser?.user_id,
    role: currentUser?.role || login.role,
    email: currentUser?.email || undefined,
    displayName: currentUser?.display_name || undefined,
    tenantId: currentUser?.tenant_id || login.tenant_id || undefined,
    dynamic: currentUser?.dynamic === true,
    chatUnlikeSwitch: currentUser?.chat_unlike_switch === true,
    timestamp: Date.now(),
  };
}

function ensureDesktopAdminSession() {
  if (!desktopAdminSessionPromise) {
    desktopAdminSessionPromise = createDesktopAdminSession().finally(() => {
      desktopAdminSessionPromise = null;
    });
  }
  return desktopAdminSessionPromise;
}

function logStartupContext() {
  appendStartupLog("desktop", `sidecar: ${sidecarPath}`);
  appendStartupLog("desktop", `resources: ${runtimeResourcesRoot}`);
  appendStartupLog("desktop", `repo: ${repoRoot}`);
  appendStartupLog("desktop", `runtime directory: ${runtimeRoot}`);
  appendStartupLog("desktop", `data directory: ${dataDir}`);
  appendStartupLog("desktop", `logs directory: ${logsDir}`);
}

function startRuntime() {
  startGuard();
  if (runtimeProcess) {
    return;
  }
  resetStartupLogsForRun();
  ensureRuntimeDirs();
  runtimeProcessExit = null;
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
  });
  runtimeProcess.stdout?.on("data", (chunk) => appendStartupChunk("sidecar", chunk));
  runtimeProcess.stderr?.on("data", (chunk) => appendStartupChunk("sidecar", chunk));
  runtimeProcess.once("error", (error) => {
    runtimeProcessExit = { error: serializeError(error) };
    runtimeProcess = null;
    setStartupFailure(error, "Could not start desktop runtime sidecar");
  });
  runtimeProcess.once("exit", (code, signal) => {
    runtimeProcessExit = { code, signal, at: new Date().toISOString() };
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
  const guardWillCleanUp = Boolean(guardProcess);
  if (!guardWillCleanUp) {
    spawnDetachedShutdownHelper(reason);
  }
  detachRuntimeMonitor();
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.destroy();
  }
  setTimeout(() => {
    app.exit(0);
  }, forceExitDelayMs).unref();
  app.quit();
}

function statusFailureMessage(status) {
  const summary = status?.overallStatus ? `Runtime status is ${status.overallStatus}` : "Runtime did not become ready";
  const failedServices = Object.entries(status?.services || {})
    .filter(([, service]) => ["failed", "stale", "stopped"].includes(service?.status))
    .map(([name, service]) => `${name}:${service.status}`)
    .slice(0, 8);
  return failedServices.length ? `${summary}; services: ${failedServices.join(", ")}` : summary;
}

async function waitForRuntimeReady() {
  startRuntime();
  const deadline = Date.now() + 30 * 60 * 1000;
  let nextStatusErrorLogAt = 0;
  while (Date.now() < deadline) {
    try {
      const status = await readStatus();
      const phase = status.overallStatus === "ready" ? "Ready" : `Waiting (${status.overallStatus || "unknown"})`;
      updateStartupState({
        status: status.overallStatus || "starting",
        phase,
        message: status.overallStatus === "ready"
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
      if (status.overallStatus === "ready" && status.config?.frontendPort) {
        updateStartupState({ status: "ready", phase: "Ready", message: "Opening LazyMind..." });
        return status;
      }
      if (["failed"].includes(status.overallStatus)) {
        throw new Error(statusFailureMessage(status));
      }
      if (runtimeProcessExit && ["stopped", "stale", "unknown", ""].includes(status.overallStatus || "")) {
        throw new Error(statusFailureMessage(status));
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

async function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 1120,
    minHeight: 760,
    title: "LazyMind",
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });
  mainWindow.on("close", (event) => {
    if (allowWindowClose) {
      return;
    }
    event.preventDefault();
    beginFastQuit("window close");
  });
  await mainWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(loadingHTML())}`);
  broadcastStartupDiagnostics();
  try {
    const status = await waitForRuntimeReady();
    await mainWindow.loadURL(`http://127.0.0.1:${status.config.frontendPort}`);
  } catch (error) {
    setStartupFailure(error);
  }
}

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
  fs.mkdirSync(logsDir, { recursive: true });
  await shell.openPath(logsDir);
});
ipcMain.handle("lazymind:openDataDir", async () => {
  fs.mkdirSync(dataDir, { recursive: true });
  await shell.openPath(dataDir);
});
ipcMain.handle("lazymind:selectFolder", async () => {
  const result = await dialog.showOpenDialog(mainWindow, { properties: ["openDirectory"] });
  return result.canceled ? null : result.filePaths[0];
});
ipcMain.handle("lazymind:startupDiagnostics", () => startupDiagnosticsSnapshot());
ipcMain.handle("lazymind:desktopAdminSession", () => ensureDesktopAdminSession());
ipcMain.handle("lazymind:copyStartupLogs", () => {
  const text = startupLogEntries
    .map((entry) => `[${entry.ts}] [${entry.source}] ${entry.text}`)
    .join("\n");
  clipboard.writeText(text);
  return true;
});
ipcMain.handle("lazymind:exportDiagnostics", async () => {
  const status = currentStatus || await readStatus();
  const out = path.join(logsDir, "desktop-diagnostics.json");
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, JSON.stringify({
    status,
    runtimeResourcesRoot,
    repoRoot,
    runtimeRoot,
    dataDir,
    logsDir,
    desktopStartupLog: startupLogPath,
    lastStartupError,
  }, null, 2));
  return out;
});

app.whenReady().then(createWindow);
app.on("window-all-closed", () => {
  app.quit();
});
app.on("before-quit", (event) => {
  if (!isQuitting) {
    event.preventDefault();
    beginFastQuit("app quit");
  }
});
