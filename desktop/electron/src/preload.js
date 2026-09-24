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

if (process.type === "renderer") {
  const { contextBridge, ipcRenderer } = require("electron");
  installDesktopBridge(contextBridge, ipcRenderer);
}

module.exports = { createDesktopBridge, installDesktopBridge };
