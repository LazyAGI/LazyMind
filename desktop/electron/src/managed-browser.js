const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const path = require('node:path');

function profilePartition(serverURL, userID) {
  const key = createHash('sha256').update(`${serverURL}\n${userID}`).digest('hex');
  return `persist:lazymind-browser-${key}`;
}

function isWebURL(url) {
  try { return ['http:', 'https:'].includes(new URL(url).protocol); } catch { return false; }
}

// Only windows owned by this adapter are addressable. It never exposes the
// LazyMind renderer or its preload to websites or browser commands.
function createBrowserAdapter({ BrowserWindow, session, partition }) {
  const windows = new Map();
  const updated = new Set();
  const detached = new Set();
  let activeID;
  const browserSession = session.fromPartition(partition);
  const preferences = {
    session: browserSession, nodeIntegration: false, contextIsolation: true,
    sandbox: true, webSecurity: true,
  };
  const event = (listeners) => ({
    addListener: (fn) => listeners.add(fn),
    removeListener: (fn) => listeners.delete(fn),
  });
  function get(id) {
    const win = windows.get(id);
    if (!win || win.isDestroyed()) throw new Error('Managed browser window is closed');
    return win;
  }
  function tab(win) {
    return {
      id: win.webContents.id, windowId: win.webContents.id,
      url: win.webContents.getURL(), title: win.webContents.getTitle(),
      status: win.webContents.isLoading() ? 'loading' : 'complete',
    };
  }
  function adopt(win) {
    const id = win.webContents.id;
    windows.set(id, win);
    activeID = id;
    win.on('focus', () => { activeID = id; });
    win.on('closed', () => {
      windows.delete(id);
      for (const fn of detached) fn({ tabId: id });
      void browserSession.cookies.flushStore().catch(() => {});
    });
    win.webContents.on('did-stop-loading', () => {
      for (const fn of updated) fn(id, { status: 'complete' });
    });
    win.webContents.debugger.on('detach', () => {
      for (const fn of detached) fn({ tabId: id });
    });
    for (const name of ['will-navigate', 'will-redirect']) {
      win.webContents.on(name, (e, url) => { if (!isWebURL(url)) e.preventDefault(); });
    }
    win.webContents.setWindowOpenHandler(({ url }) => isWebURL(url) || url === 'about:blank'
      ? { action: 'allow', overrideBrowserWindowOptions: { webPreferences: preferences } }
      : { action: 'deny' });
    win.webContents.on('did-create-window', (child) => adopt(child));
    return win;
  }
  return {
    windows: {
      async create({ url }) {
        if (!isWebURL(url)) throw new Error('Only HTTP and HTTPS pages are supported');
        const win = adopt(new BrowserWindow({
          width: 1280, height: 900, title: 'LazyMind Browser', webPreferences: preferences,
        }));
        win.maximize();
        // Return immediately like chrome.windows.create; controller waits for load.
        void win.loadURL(url).catch(() => {});
        return { id: win.webContents.id, tabs: [tab(win)] };
      },
      async update(id) { const win = get(id); win.show(); win.focus(); },
      async remove(id) { get(id).destroy(); },
    },
    tabs: {
      async get(id) { return tab(get(id)); },
      async update(id) { const win = get(id); win.show(); win.focus(); return tab(win); },
      async query() {
        const win = windows.get(activeID) || [...windows.values()].at(-1);
        return win && !win.isDestroyed() ? [tab(win)] : [];
      },
      async remove(id) { get(id).destroy(); },
      onUpdated: event(updated),
    },
    debugger: {
      onDetach: event(detached),
      async attach({ tabId }, version) { get(tabId).webContents.debugger.attach(version); },
      async detach({ tabId }) { get(tabId).webContents.debugger.detach(); },
      async sendCommand({ tabId }, method, params) {
        return get(tabId).webContents.debugger.sendCommand(method, params);
      },
    },
    scripting: {
      async executeScript({ target, func, args }) {
        const result = await get(target.tabId).webContents.executeJavaScript(
          `(${func.toString()})(...${JSON.stringify(args)})`,
        );
        return [{ result }];
      },
    },
    async dispose() {
      for (const win of windows.values()) if (!win.isDestroyed()) win.destroy();
      windows.clear();
      browserSession.flushStorageData();
      await browserSession.cookies.flushStore();
    },
  };
}

async function loadBrowserController(root) {
  const [controller, capture] = await Promise.all([
    import(pathToFileURL(path.join(root, 'src/controller.js')).href),
    import(pathToFileURL(path.join(root, 'src/capture.js')).href),
  ]);
  return { BrowserController: controller.BrowserController, captureCurrentPage: capture.captureCurrentPage };
}

module.exports = { createBrowserAdapter, loadBrowserController, profilePartition };
