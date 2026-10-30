const path = require("node:path");

// All privileged work stays in Electron's main process. The local renderer can
// only confirm/dismiss the request currently displayed by this controller.
function createAgentRecovery({ BrowserWindow, ipcMain, screen, wait, dismiss, reconnect, check, finishView, platform = process.platform, locale = () => "en", log = () => {}, retryMs = 5000, successMs = 1200, checkMs = 1000 }) {
  let running = false;
  let generation = 0;
  let lifetime;
  let window;
  let current;
  let currentSignal;
  let successTimer;
  let listenerUnavailable = false;
  const state = () => current && ({ ...current, language: /^zh/i.test(locale()) ? "zh" : "en" });
  const publish = () => {
    if (window && !window.isDestroyed()) window.webContents.send("lazymind:agentRecoveryState", state());
  };
  const authorized = event => window && !window.isDestroyed()
    && event.sender === window.webContents && event.senderFrame === window.webContents.mainFrame;
  function succeeded(request,owned) {
    if(current!==request||window!==owned||request.stage==="connected")return;
    request.stage="connected";request.busy=false;request.error="";publish();
    successTimer=setTimeout(()=>{if(window===owned)owned.close();},successMs);
  }

  ipcMain.handle("lazymind:agentRecoveryState", event => {
    if (!authorized(event)) throw new Error("RECOVERY_WINDOW_REQUIRED");
    return state();
  });
  ipcMain.handle("lazymind:agentRecoveryDismiss", event => {
    if (!authorized(event)) throw new Error("RECOVERY_WINDOW_REQUIRED");
    if (!current?.busy) window.close();
  });
  ipcMain.handle("lazymind:agentRecoveryConfirm", async event => {
    if (!authorized(event)) throw new Error("RECOVERY_WINDOW_REQUIRED");
    if (!current?.can_restart || current.busy || current.stage === "connected") return;
    const request = current;
    const owned = window;
    const revision = generation;
    request.busy = true;
    request.error = "";
    request.stage = "checking";
    publish();
    try {
      await reconnect(request.id, stage => {
        if (revision !== generation || current !== request || request.stage==="connected") return;
        if (["closing", "starting", "connecting", "restoring", "waiting-chat", "waiting-idle", "showing-panel", "resuming"].includes(stage)) {
          if(stage==="resuming")request.view_only=false;
          request.stage = stage; publish();
        }
      }, currentSignal);
      if (revision !== generation || current !== request) return;
      succeeded(request,owned);
    } catch (error) {
      if (revision !== generation || current !== request || request.stage==="connected") return;
      request.busy = false;
      request.stage = "failed";
      request.error = String(error.message || error).slice(0, 1200);
      log(`recovery ${request.id} failed: ${request.error}`);
      publish();
    }
  });

  async function show(request, revision) {
    if (revision !== generation || !running) return;
    current = { id: request.id, code: request.code, can_restart: request.can_restart,
	  view_only: request.view_only === true,
      has_task: Boolean(request.thread_id || request.intents?.length), busy: false, stage: "confirm", error: "" };
    window = new BrowserWindow({
      width: 400, height: 240, useContentSize: true, show: false, resizable: false, minimizable: false,
      maximizable: false, fullscreenable: false, skipTaskbar: platform !== "win32",
      autoHideMenuBar: true, title: "LazyMind · Codex",
      icon: path.join(__dirname, "lazymind-logo.png"),
      webPreferences: {
        preload: path.join(__dirname, "agent-recovery-preload.js"),
        contextIsolation: true, sandbox: true, nodeIntegration: false,
      },
    });
    const owned = window;
    const viewRequest=current;
    const closedSignal=new AbortController();
    const signal=AbortSignal.any([lifetime.signal,closedSignal.signal]);
    currentSignal=signal;
    let checkTimer, refreshTimer;
    async function refreshRequest() {
      if (signal.aborted || current !== viewRequest || viewRequest.stage === "connected") return;
      try {
        // The outer listener waits for this window to close. Keep its existing
        // request current while later failures are merged by Assistant Bridge.
        const { request: latest } = await wait(signal);
        if (signal.aborted || current !== viewRequest || viewRequest.stage === "connected") return;
        if (latest?.id === viewRequest.id) {
          const changed = viewRequest.code !== latest.code || viewRequest.can_restart !== latest.can_restart;
          viewRequest.code = latest.code;
          viewRequest.can_restart = latest.can_restart;
          viewRequest.view_only = latest.view_only === true;
          viewRequest.has_task = Boolean(latest.thread_id || latest.intents?.length);
          if (changed && !viewRequest.busy) {
            viewRequest.stage = "confirm";
            viewRequest.error = "";
          }
          publish();
        }
      } catch { /* A transient read failure must not replace the current diagnosis. */ }
      if (!signal.aborted) refreshTimer = setTimeout(refreshRequest, checkMs);
    }
    async function observePresentation() {
      if(signal.aborted||current!==viewRequest||viewRequest.stage==="connected")return;
      try {
        const result=viewRequest.view_only ? await check(request.id,signal) : undefined;
        if(!signal.aborted&&current===viewRequest&&result?.ready){
          // Atomically refuse completion if an execution was queued meanwhile.
          await finishView(request.id);
          if(!signal.aborted&&current===viewRequest){
            log(`recovery ${request.id} presentation confirmed`);
            succeeded(viewRequest,owned);return;
          }
        }
      } catch { /* Observation never restarts Codex or executes a Workflow. */ }
      if(!signal.aborted)checkTimer=setTimeout(observePresentation,checkMs);
    }
    let resolveClosed;
    const closed = new Promise(resolve => { resolveClosed = resolve; });
    owned.on("close", event => { if (current?.busy && running) event.preventDefault(); });
    owned.once("closed", () => {
      closedSignal.abort();clearTimeout(checkTimer);clearTimeout(refreshTimer);
      if (window === owned) { clearTimeout(successTimer); window = undefined; current = undefined; }
      resolveClosed();
    });
    owned.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    owned.webContents.on("will-navigate", event => event.preventDefault());
    const display = screen.getDisplayNearestPoint(screen.getCursorScreenPoint());
    const area = display.workArea;
    owned.setPosition(Math.round(area.x + (area.width - 400) / 2), Math.round(area.y + (area.height - 240) / 2));
    const topmostLevel = platform === "win32" ? "pop-up-menu" : "floating";
    owned.setAlwaysOnTop(true, topmostLevel);
    // skipTransformProcessType avoids changing LazyMind's Dock identity.
    if (platform === "darwin") owned.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true, skipTransformProcessType: true });
    try {
      log(`recovery ${request.id} loading window on ${platform}`);
      await owned.loadFile(path.join(__dirname, "agent-recovery.html"));
      log(`recovery ${request.id} window loaded`);
      if (revision === generation && !owned.isDestroyed()) {
        // Windows can leave an inactive recovery window unpainted/behind Codex.
        // This window follows an explicit Workflow operation and needs attention.
        if (platform === "win32") {
          owned.show();
          // Avoid Windows' below-taskbar positioning used by "floating".
          owned.setAlwaysOnTop(true, topmostLevel);
          owned.moveTop();
          owned.focus();
          owned.flashFrame(true);
          owned.once("focus", () => { if (!owned.isDestroyed()) owned.flashFrame(false); });
        }
        else owned.showInactive();
        log(`recovery ${request.id} window shown: visible=${owned.isVisible()} topmost=${owned.isAlwaysOnTop()}`);
      }
      void refreshRequest();
      if(check&&finishView)void observePresentation();
      await closed;
    } finally {
      if (!owned.isDestroyed()) owned.destroy();
      // Ignore account changes/disabled pairings; they must not affect a new request.
      await dismiss(request.id).catch(() => {});
    }
  }

  async function poll(revision) {
    while (running && revision === generation) {
      try {
        const result = await wait(lifetime.signal);
        listenerUnavailable = false;
        if (result?.request) await show(result.request, revision);
      } catch (error) {
        if (!running || revision !== generation) break;
        // No extra UI or fallback. The next user operation can request recovery.
        if (!listenerUnavailable) log("recovery listener unavailable");
        listenerUnavailable = true;
        await new Promise(resolve => {
          const signal = lifetime.signal;
          const done = () => { clearTimeout(timer); signal.removeEventListener("abort", done); resolve(); };
          const timer = setTimeout(done, retryMs);
          signal.addEventListener("abort", done, { once: true });
          if (signal.aborted) done();
        });
      }
    }
  }
  function start() {
    if (running) return;
    running = true;
    lifetime = new AbortController();
    void poll(++generation);
  }
  function stop() {
    const request = current;
    running = false;
    generation += 1;
    lifetime?.abort();
    clearTimeout(successTimer);
    window?.destroy();
    // Logout/disable callers await this while the old pairing is still valid.
    return request ? dismiss(request.id).catch(() => {}) : Promise.resolve();
  }
  return { start, stop };
}

// Reconnect emits progress as NDJSON. Only its final successful exit counts as
// recovery; receiving a 'connected' line alone cannot hide a later failure.
function runRecoveryCommand({ spawn, executable, env, id, onStage, signal, timeoutMs = 155000 }) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, ["internal", "codex-recovery", "reconnect", id], {
      env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true,
    });
    let buffer = "", error = "", done = false;
    let finished = false;
    const timer = setTimeout(() => { child.kill(); complete(new Error("Codex recovery timed out")); }, timeoutMs);
    const abort = () => { child.kill(); complete(new Error("Codex recovery cancelled")); };
    function complete(failure) {
      if (done) return;
      done = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
      failure ? reject(failure) : resolve();
    }
    child.stdout.on("data", chunk => {
      buffer += String(chunk);
      if (buffer.length > 16384) { child.kill(); complete(new Error("Invalid recovery progress")); return; }
      let newline;
      while ((newline = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, newline); buffer = buffer.slice(newline + 1);
        try { const value = JSON.parse(line); if (value.stage === "done") finished = true; else onStage(value.stage); }
        catch { child.kill(); complete(new Error("Invalid recovery progress")); return; }
      }
    });
    child.stderr.on("data", chunk => { error = (error + String(chunk)).slice(-4096); });
    child.once("error", complete);
    child.once("close", code => complete(code === 0 && finished ? undefined : new Error(error.trim() || "Codex recovery did not complete")));
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) abort();
  });
}

module.exports = { createAgentRecovery, runRecoveryCommand };
