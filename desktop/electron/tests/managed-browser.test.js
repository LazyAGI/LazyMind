const test = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { createBrowserAdapter, loadBrowserController } = require('../src/managed-browser');
const path = require('node:path');

function fixture() {
  const created = [];
  const commands = [];
  let id = 100;
  const browserSession = { cookies: { async flushStore() {} }, flushStorageData() {} };
  class Window extends EventEmitter {
    constructor(options) {
      super();
      this.options = options;
      this.webContents = Object.assign(new EventEmitter(), {
        id: ++id,
        getURL: () => this.url,
        getTitle: () => 'Fixture',
        isLoading: () => false,
        setWindowOpenHandler: (handler) => { this.popup = handler; },
        debugger: Object.assign(new EventEmitter(), {
          attach: () => {}, detach: () => {},
          sendCommand: async (method, params) => { commands.push({ method, params }); return {}; },
        }),
      });
      created.push(this);
    }
    isDestroyed() { return Boolean(this.destroyed); }
    async loadURL(url) { this.url = url; }
    maximize() {}
    show() {}
    focus() { this.emit('focus'); }
    destroy() { this.destroyed = true; this.emit('closed'); }
  }
  const adapter = createBrowserAdapter({
    BrowserWindow: Window,
    session: { fromPartition: () => browserSession },
    partition: 'persist:test',
  });
  return { adapter, created, commands };
}

test('dedicated windows isolate website privileges and restrict control to owned web contents', async () => {
  const { adapter, created, commands } = fixture();
  const result = await adapter.windows.create({ url: 'https://example.com' });
  const win = created[0];
  assert.deepEqual(Object.keys(win.options.webPreferences).sort(), ['contextIsolation', 'nodeIntegration', 'sandbox', 'session', 'webSecurity']);
  assert.equal(win.options.webPreferences.nodeIntegration, false);
  assert.equal(win.options.webPreferences.sandbox, true);
  await adapter.debugger.sendCommand({ tabId: result.id }, 'Input.insertText', { text: 'fixture' });
  assert.deepEqual(commands, [{ method: 'Input.insertText', params: { text: 'fixture' } }]);
  await assert.rejects(adapter.debugger.sendCommand({ tabId: 1 }, 'Runtime.evaluate', {}), /closed/);
  assert.deepEqual(win.popup({ url: 'file:///etc/passwd' }), { action: 'deny' });
  assert.equal(win.popup({ url: 'https://login.example.com' }).action, 'allow');
  let prevented = false;
  win.webContents.emit('will-navigate', { preventDefault() { prevented = true; } }, 'file:///etc/passwd');
  assert.equal(prevented, true);
  await assert.rejects(adapter.windows.create({ url: 'javascript:alert(1)' }), /HTTP/);
  await adapter.dispose();
  assert.equal(win.destroyed, true);
  await assert.rejects(adapter.tabs.get(result.id), /closed/);
});

test('shared controller imports as ESM and uses the injected transport without Chrome globals', async () => {
  const { BrowserController, captureCurrentPage } = await loadBrowserController(path.resolve(__dirname, '../../../browser-extension'));
  assert.equal(typeof captureCurrentPage, 'function');
  const { adapter, commands } = fixture();
  const window = await adapter.windows.create({ url: 'https://example.com' });
  const controller = new BrowserController(adapter);
  await controller.send(window.id, 'Page.enable');
  assert.equal(commands[0].method, 'Page.enable');
  await adapter.dispose();
});
