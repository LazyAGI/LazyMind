const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("lazymindDesktop", {
  openLogsDir: () => ipcRenderer.invoke("lazymind:openLogsDir"),
  openDataDir: () => ipcRenderer.invoke("lazymind:openDataDir"),
  runtimeStatus: () => ipcRenderer.invoke("lazymind:runtimeStatus"),
  restartRuntime: () => ipcRenderer.invoke("lazymind:restartRuntime"),
  resetRuntime: (scope) => ipcRenderer.invoke("lazymind:resetRuntime", scope),
  selectFolder: () => ipcRenderer.invoke("lazymind:selectFolder"),
  exportDiagnostics: () => ipcRenderer.invoke("lazymind:exportDiagnostics"),
  startupDiagnostics: () => ipcRenderer.invoke("lazymind:startupDiagnostics"),
  desktopAdminSession: () => ipcRenderer.invoke("lazymind:desktopAdminSession"),
  copyStartupLogs: () => ipcRenderer.invoke("lazymind:copyStartupLogs"),
  onStartupDiagnosticsUpdate: (handler) => {
    if (typeof handler !== "function") {
      return () => {};
    }
    const listener = (_event, payload) => handler(payload);
    ipcRenderer.on("lazymind:startupDiagnosticsUpdate", listener);
    return () => ipcRenderer.removeListener("lazymind:startupDiagnosticsUpdate", listener);
  },
});
