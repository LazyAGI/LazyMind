const { contextBridge, ipcRenderer } = require("electron");
contextBridge.exposeInMainWorld("agentRecovery", {
  state: () => ipcRenderer.invoke("lazymind:agentRecoveryState"),
  confirm: () => ipcRenderer.invoke("lazymind:agentRecoveryConfirm"),
  dismiss: () => ipcRenderer.invoke("lazymind:agentRecoveryDismiss"),
  onState: handler => ipcRenderer.on("lazymind:agentRecoveryState", (_event, state) => handler(state)),
});
