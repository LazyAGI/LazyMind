const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('pageRecovery', {
  retry: () => ipcRenderer.invoke('lazymind:pageRecovery', 'retry'),
  home: () => ipcRenderer.invoke('lazymind:pageRecovery', 'home'),
});
