function createDesktopBridge(ipcRenderer) {
  return {
    platform: process.platform,
    openLogsDir: () => ipcRenderer.invoke("lazymind:openLogsDir"),
    openDataDir: () => ipcRenderer.invoke("lazymind:openDataDir"),
    runtimeStatus: () => ipcRenderer.invoke("lazymind:runtimeStatus"),
    agentIntegrationStatuses: () => ipcRenderer.invoke("lazymind:agentIntegrationStatuses"),
    agentIntegrationAction: (agent, action) => ipcRenderer.invoke("lazymind:agentIntegrationAction", agent, action),
    executorIntegrationPolicies: () => ipcRenderer.invoke("lazymind:executorIntegrationPolicies"),
    executorIntegrationAction: (provider, action) => ipcRenderer.invoke("lazymind:executorIntegrationAction", provider, action),
    restartRuntime: () => ipcRenderer.invoke("lazymind:restartRuntime"),
    resetRuntime: (scope) => ipcRenderer.invoke("lazymind:resetRuntime", scope),
    selectFolder: () => ipcRenderer.invoke("lazymind:selectFolder"),
    selectExecutable: () => ipcRenderer.invoke("lazymind:selectExecutable"),
    exportDiagnostics: () => ipcRenderer.invoke("lazymind:exportDiagnostics"),
    showItemInFolder: (payload) => ipcRenderer.invoke("lazymind:showItemInFolder", payload),
    saveFileAs: (payload) => ipcRenderer.invoke("lazymind:saveFileAs", payload),
    downloadFile: (payload) => ipcRenderer.invoke("lazymind:downloadFile", payload),
    notifyAppReady: () => ipcRenderer.send("lazymind:renderer-ready"),
    startupDiagnostics: () => ipcRenderer.invoke("lazymind:startupDiagnostics"),
    copyStartupLogs: () => ipcRenderer.invoke("lazymind:copyStartupLogs"),
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
