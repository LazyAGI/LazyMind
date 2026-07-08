const { app, BrowserWindow, ipcMain, shell, dialog } = require("electron");
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
const sidecarPath = process.env.LAZYMIND_DESKTOP_SIDECAR ||
  path.join(runtimeResourcesRoot, "bin", "local-runtime-manager");

let mainWindow;
let runtimeProcess;
let guardProcess;
let currentStatus = null;
let shutdownPromise = null;
let isQuitting = false;
let allowWindowClose = false;

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
  };
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
  guardProcess = spawn(sidecarPath, sidecarArgs("guard", ["--owner-pid", String(process.pid)]), {
    env: sidecarEnv(),
    stdio: "ignore",
    detached: true,
  });
  guardProcess.once("exit", () => {
    guardProcess = null;
  });
  guardProcess.unref();
}

function stopGuard() {
  if (!guardProcess) {
    return;
  }
  guardProcess.kill();
  guardProcess = null;
}

async function readStatus() {
  const stdout = await runSidecar("status", ["--json"]);
  currentStatus = JSON.parse(stdout);
  return currentStatus;
}

function startRuntime() {
  startGuard();
  if (runtimeProcess) {
    return;
  }
  runtimeProcess = spawn(sidecarPath, sidecarArgs("up"), {
    env: sidecarEnv(),
    stdio: "ignore",
    detached: false,
  });
  runtimeProcess.once("exit", () => {
    runtimeProcess = null;
  });
}

function shutdownRuntime() {
  if (shutdownPromise) {
    return shutdownPromise;
  }
  shutdownPromise = (async () => {
    if (!fs.existsSync(sidecarPath)) {
      stopGuard();
      return;
    }
    let downSucceeded = false;
    try {
      await runSidecar("down");
      downSucceeded = true;
    } catch (error) {
      console.error("Failed to stop LazyMind desktop runtime:", error);
    }
    if (downSucceeded) {
      stopGuard();
    }
  })().finally(() => {
    shutdownPromise = null;
  });
  return shutdownPromise;
}

async function requestQuit() {
  if (isQuitting) {
    return;
  }
  isQuitting = true;
  await shutdownRuntime();
  allowWindowClose = true;
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.destroy();
  }
  app.quit();
}

async function waitForRuntimeReady() {
  startRuntime();
  const deadline = Date.now() + 30 * 60 * 1000;
  while (Date.now() < deadline) {
    try {
      const status = await readStatus();
      if (status.overallStatus === "ready" && status.config?.frontendPort) {
        return status;
      }
    } catch (_) {
      // The sidecar may still be creating its state file.
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
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f7f8fa; color: #1f2937; }
    main { height: 100vh; display: grid; place-items: center; }
    section { width: 420px; }
    h1 { font-size: 24px; font-weight: 650; margin: 0 0 12px; }
    p { font-size: 14px; line-height: 1.6; color: #4b5563; margin: 0; }
    .bar { height: 4px; background: #dbeafe; overflow: hidden; margin-top: 22px; border-radius: 2px; }
    .bar::before { content: ""; display: block; width: 42%; height: 100%; background: #2563eb; animation: move 1.2s infinite ease-in-out; }
    @keyframes move { 0% { transform: translateX(-100%); } 100% { transform: translateX(240%); } }
  </style>
</head>
<body><main><section><h1>LazyMind</h1><p>Starting local desktop runtime...</p><div class="bar"></div></section></main></body>
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
    requestQuit();
  });
  await mainWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(loadingHTML())}`);
  try {
    const status = await waitForRuntimeReady();
    await mainWindow.loadURL(`http://127.0.0.1:${status.config.frontendPort}`);
  } catch (error) {
    await mainWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(`<pre>${String(error.stack || error)}</pre>`)}`);
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
  const target = path.join(runtimeRoot, "logs");
  fs.mkdirSync(target, { recursive: true });
  await shell.openPath(target);
});
ipcMain.handle("lazymind:openDataDir", async () => {
  const target = path.join(runtimeRoot, "data");
  fs.mkdirSync(target, { recursive: true });
  await shell.openPath(target);
});
ipcMain.handle("lazymind:selectFolder", async () => {
  const result = await dialog.showOpenDialog(mainWindow, { properties: ["openDirectory"] });
  return result.canceled ? null : result.filePaths[0];
});
ipcMain.handle("lazymind:exportDiagnostics", async () => {
  const status = currentStatus || await readStatus();
  const out = path.join(runtimeRoot, "logs", "desktop-diagnostics.json");
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, JSON.stringify({ status, runtimeResourcesRoot, repoRoot, runtimeRoot }, null, 2));
  return out;
});

app.whenReady().then(createWindow);
app.on("window-all-closed", () => {
  app.quit();
});
app.on("before-quit", (event) => {
  if (!isQuitting) {
    event.preventDefault();
    requestQuit();
  }
});
