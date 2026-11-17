const path = require('node:path');

function createPageRecovery({ BrowserWindow, ipcMain, dialog, locale = () => 'en', enabled = () => true, report = () => {} }) {
  const states = new Map();
  const available = state => enabled() && !state.owner.isDestroyed();

  async function retry(state, action) {
    if (state.busy || !available(state)) return { ok: false };
    state.busy = true;
    try {
      await state.owner.loadURL(action === 'home' ? state.home : state.url);
      if (!available(state)) return { ok: false };
      state.owner.show(); state.owner.focus();
      state.popup?.close();
      return { ok: true };
    } catch { report('PAGE_RECOVERY_RETRY_FAILED'); return { ok: false }; }
    finally { state.busy = false; }
  }

  ipcMain.handle('lazymind:pageRecovery', async (event, action) => {
    const state = [...states.values()].find(item => item.popup && !item.popup.isDestroyed()
      && event.sender === item.popup.webContents && event.senderFrame === item.popup.webContents.mainFrame);
    if (!state || !['retry', 'home'].includes(action)) throw new Error('PAGE_RECOVERY_REQUIRED');
    return retry(state, action);
  });

  async function show(state) {
    if (!available(state) || state.busy || state.showing) return;
    if (state.popup && !state.popup.isDestroyed()) { state.popup.show(); return; }
    state.showing = true;
    const zh = /^zh/i.test(locale());
    try {
      const popup = new BrowserWindow({
        parent: state.owner, modal: true, width: 540, height: 330, show: false,
        resizable: false, autoHideMenuBar: true, title: 'LazyMind',
        webPreferences: { preload: path.join(__dirname, 'page-recovery-preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
      });
      state.popup = popup;
      popup.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
      popup.webContents.on('will-navigate', event => event.preventDefault());
      popup.on('closed', () => { if (state.popup === popup) state.popup = null; });
      await popup.loadFile(path.join(__dirname, 'page-recovery.html'), { query: { lang: zh ? 'zh' : 'en' } });
      if (available(state) && !popup.isDestroyed()) { popup.show(); popup.focus(); }
    } catch {
      state.popup?.close();
      if (!available(state)) return;
      report('PAGE_RECOVERY_VIEW_FAILED');
      while (available(state)) {
        const result = await dialog.showMessageBox(state.owner, {
          type: 'error', title: 'LazyMind',
          message: zh ? '页面暂时无法显示' : 'This page cannot be displayed',
          detail: zh ? '请重试以重新加载页面。未保存的内容可能丢失。' : 'Reload the page to try again. Unsaved changes may be lost.',
          buttons: zh ? ['重试', '返回首页', '取消'] : ['Retry', 'Back to home', 'Cancel'], defaultId: 0, cancelId: 2,
        });
        if (result.response >= 2) break;
        if ((await retry(state, result.response === 1 ? 'home' : 'retry')).ok) break;
      }
    } finally { state.showing = false; }
  }

  return {
    attach(owner, home) {
      if (states.has(owner)) return;
      const origin = new URL(home).origin;
      const state = { owner, home, url: home, popup: null, busy: false, showing: false };
      states.set(owner, state);
      const remember = (_event, value) => {
        try {
          const url = new URL(value);
          if (url.origin === origin && !url.username && !url.password) state.url = url.href;
        } catch { /* Non-application documents never become recovery targets. */ }
      };
      owner.webContents.on('did-navigate', remember);
      owner.webContents.on('did-navigate-in-page', (event, url, isMainFrame) => { if (isMainFrame) remember(event, url); });
      owner.webContents.on('render-process-gone', (_event, details) => {
        if (details.reason !== 'clean-exit') void show(state).catch(() => report('PAGE_RECOVERY_VIEW_FAILED'));
      });
      owner.webContents.on('did-fail-load', (_event, code, _description, _url, isMainFrame) => {
        if (isMainFrame && code !== -3) void show(state).catch(() => report('PAGE_RECOVERY_VIEW_FAILED'));
      });
      owner.on('closed', () => { state.popup?.close(); states.delete(owner); });
    },
  };
}

module.exports = { createPageRecovery };
