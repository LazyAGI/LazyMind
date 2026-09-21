function createDesktopBridge(ipcRenderer) {
  return {
    platform: process.platform,
    ...(process.platform === "darwin" ? {
      recordingNativeStart: () => ipcRenderer.invoke("lazymind:recordingNativeStart"),
      recordingNativeStop: (id) => ipcRenderer.invoke("lazymind:recordingNativeStop", id),
      recordingNativeCancel: () => ipcRenderer.invoke("lazymind:recordingNativeCancel"),
      recordingNativeSettings: () => ipcRenderer.invoke("lazymind:recordingNativeSettings"),
      onRecordingNativeEvent: (handler) => {
        const listener = (_event, message) => handler(message);
        ipcRenderer.on("lazymind:recordingNativeEvent", listener);
        return () => ipcRenderer.removeListener("lazymind:recordingNativeEvent", listener);
      },
    } : {}),
    recordingInputPermission: () => ipcRenderer.invoke("lazymind:recordingInputPermission"),
    recordingInputSettings: () => ipcRenderer.invoke("lazymind:recordingInputSettings"),
    recordingInputStart: (startedAt) => ipcRenderer.invoke("lazymind:recordingInputStart", startedAt),
    recordingInputStop: (id) => ipcRenderer.invoke("lazymind:recordingInputStop", id),
    recordingInputCancel: (id) => ipcRenderer.invoke("lazymind:recordingInputCancel", id),
    browserSessionSet: (value) => ipcRenderer.invoke("lazymind:browserSessionSet", value),
    browserStatus: () => ipcRenderer.invoke("lazymind:browserStatus"),
    browserSelect: (engine) => ipcRenderer.invoke("lazymind:browserSelect", engine),
    browserOpen: (url) => ipcRenderer.invoke("lazymind:browserOpen", url),
    openLogsDir: () => ipcRenderer.invoke("lazymind:openLogsDir"),
    openDataDir: () => ipcRenderer.invoke("lazymind:openDataDir"),
    openBrowserExtensionDir: () => ipcRenderer.invoke("lazymind:openBrowserExtensionDir"),
    runtimeStatus: () => ipcRenderer.invoke("lazymind:runtimeStatus"),
    agentIntegrationStatuses: () => ipcRenderer.invoke("lazymind:agentIntegrationStatuses"),
    agentIntegrationAction: (agent, action) => ipcRenderer.invoke("lazymind:agentIntegrationAction", agent, action),
    executorIntegrationPolicies: () => ipcRenderer.invoke("lazymind:executorIntegrationPolicies"),
    executorIntegrationAction: (provider, action) => ipcRenderer.invoke("lazymind:executorIntegrationAction", provider, action),
    ankiIntegrationStatus: () => ipcRenderer.invoke("lazymind:ankiIntegrationStatus"),
    openAnki: () => ipcRenderer.invoke("lazymind:openAnki"),
    agentExecutableBindings: () => ipcRenderer.invoke("lazymind:agentExecutableBindings"),
    agentExecutableBind: (target, executablePath) => ipcRenderer.invoke("lazymind:agentExecutableBind", target, executablePath),
    agentExecutableClear: (target) => ipcRenderer.invoke("lazymind:agentExecutableClear", target),
    assistantSessionSet: (session) => ipcRenderer.invoke("lazymind:assistantSessionSet", session),
    assistantSessionClear: () => ipcRenderer.invoke("lazymind:assistantSessionClear"),
    restartRuntime: () => ipcRenderer.invoke("lazymind:restartRuntime"),
    resetRuntime: (scope) => ipcRenderer.invoke("lazymind:resetRuntime", scope),
    localFolderAccessStatus: () => ipcRenderer.invoke("lazymind:localFolderAccessStatus"),
    chooseLocalDiscoveryRoots: () => ipcRenderer.invoke("lazymind:chooseLocalDiscoveryRoots"),
    discoverLocalFolders: () => ipcRenderer.invoke("lazymind:discoverLocalFolders"),
    authorizeLocalFolders: (paths) => ipcRenderer.invoke("lazymind:authorizeLocalFolders", paths),
    selectFolder: () => ipcRenderer.invoke("lazymind:selectFolder"),
    selectLocalWorkspace: () => ipcRenderer.invoke("lazymind:selectLocalWorkspace"),
    reauthorizeLocalWorkspace: (workspaceId) => ipcRenderer.invoke("lazymind:reauthorizeLocalWorkspace", workspaceId),
    authorizeLocalWorkspace: (selectionToken) => ipcRenderer.invoke("lazymind:authorizeLocalWorkspace", selectionToken),
    selectExecutable: (target) => ipcRenderer.invoke("lazymind:selectExecutable", target),
    exportDiagnostics: () => ipcRenderer.invoke("lazymind:exportDiagnostics"),
    showItemInFolder: (payload) => ipcRenderer.invoke("lazymind:showItemInFolder", payload),
    saveFileAs: (payload) => ipcRenderer.invoke("lazymind:saveFileAs", payload),
    downloadFile: (payload) => ipcRenderer.invoke("lazymind:downloadFile", payload),
    notifyAppReady: () => ipcRenderer.send("lazymind:renderer-ready"),
    startupDiagnostics: () => ipcRenderer.invoke("lazymind:startupDiagnostics"),
    copyStartupLogs: () => ipcRenderer.invoke("lazymind:copyStartupLogs"),
    openCloudLogin: (url) => ipcRenderer.invoke("lazymind:openCloudLogin", url),
    openManagedProviderAuthorization: (url) => ipcRenderer.invoke("lazymind:openManagedProviderAuthorization", url),
    openFeishuCLIAuthorization: (url) => ipcRenderer.invoke("lazymind:openFeishuCLIAuthorization", url),
    openCloudRegister: () => ipcRenderer.invoke("lazymind:openCloudRegister"),
    openCloudTokenPlan: (url) => ipcRenderer.invoke("lazymind:openCloudTokenPlan", url),
    onStartupDiagnosticsUpdate: (handler) => {
      if (typeof handler !== "function") return () => {};
      const listener = (_event, payload) => handler(payload);
      ipcRenderer.on("lazymind:startupDiagnosticsUpdate", listener);
      return () => ipcRenderer.removeListener("lazymind:startupDiagnosticsUpdate", listener);
    },
  };
}

function installDesktopBridge(contextBridge, ipcRenderer) {
  const bridge = createDesktopBridge(ipcRenderer);
  contextBridge.exposeInMainWorld("lazymindDesktop", bridge);
  return bridge;
}

// Same immutable base + session-scoped refresh format as frontend auth.ts.
// Never merge credentials from a different active-session generation.
function storedNotificationUser(target, raw) {
  const user = JSON.parse(raw);
  if (!user?.token) return user;
  const entry = JSON.parse(target.localStorage.getItem(`lazymind:refresh:${user.sessionId || user.token}`));
  return entry?.base === raw && entry?.endpoint === `${target.location.origin}/api/authservice/auth/refresh`
    ? { ...user, ...entry.credentials } : user;
}

function installNotificationSessionSync(target, bridge) {
  let previous;
  const sync = () => {
    let user;
    try { user = storedNotificationUser(target, target.localStorage.getItem("lazymind:user")); } catch { /* Treat invalid storage as logged out. */ }
    const value = user?.token && user?.refreshToken ? {
      server_url: target.location.origin, username: user.username,
      access_token: user.token, refresh_token: user.refreshToken,
      role: user.role, tenant_id: user.tenantId || user.tenant_id,
    } : null;
    const fingerprint = JSON.stringify(value);
    if (previous === fingerprint) return;
    previous = fingerprint;
    const operation = value ? bridge.assistantSessionSet(value) : bridge.assistantSessionClear();
    Promise.resolve(operation).catch(() => {
      // A later readiness/login event can retry; never log session material.
      if (previous === fingerprint) previous = undefined;
    });
  };
  const events = ["DOMContentLoaded", "lazymind:user-change", "storage"];
  for (const event of events) target.addEventListener(event, sync);
  if (target.document.readyState !== "loading") sync();
  // Closing the renderer is background mode, not logout.
  return () => { for (const event of events) target.removeEventListener(event, sync); };
}

function restoreNotificationSession(target, ipcRenderer) {
  try {
    const raw = target.localStorage.getItem("lazymind:user");
    const user = storedNotificationUser(target, raw);
    if (!user?.token || !user?.refreshToken) return;
    const session = ipcRenderer.sendSync("lazymind:notificationSessionRestore", {
      server_url: target.location.origin, access_token: user.token, refresh_token: user.refreshToken,
    });
    if (!session?.access_token || !session.refresh_token || session.server_url !== target.location.origin
      || target.localStorage.getItem("lazymind:user") !== raw) return;
    const base = JSON.parse(raw);
    target.localStorage.setItem(`lazymind:refresh:${base.sessionId || base.token}`, JSON.stringify({
      base: raw, endpoint: `${target.location.origin}/api/authservice/auth/refresh`,
      credentials: { token: session.access_token, refreshToken: session.refresh_token, timestamp: Date.now() },
    }));
  } catch { /* Unavailable storage/IPC must never restore or log credentials. */ }
}

if (process.type === "renderer") {
  const { contextBridge, ipcRenderer } = require("electron");
  restoreNotificationSession(window, ipcRenderer);
  const bridge = installDesktopBridge(contextBridge, ipcRenderer);
  installNotificationSessionSync(window, bridge);
}

module.exports = { createDesktopBridge, installDesktopBridge, installNotificationSessionSync, restoreNotificationSession };
