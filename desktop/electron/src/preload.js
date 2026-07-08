const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("lazymindDesktop", {
  openLogsDir: () => ipcRenderer.invoke("lazymind:openLogsDir"),
  openDataDir: () => ipcRenderer.invoke("lazymind:openDataDir"),
  runtimeStatus: () => ipcRenderer.invoke("lazymind:runtimeStatus"),
  restartRuntime: () => ipcRenderer.invoke("lazymind:restartRuntime"),
  resetRuntime: (scope) => ipcRenderer.invoke("lazymind:resetRuntime", scope),
  selectFolder: () => ipcRenderer.invoke("lazymind:selectFolder"),
  exportDiagnostics: () => ipcRenderer.invoke("lazymind:exportDiagnostics"),
});
