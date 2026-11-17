import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createRequire } from 'node:module';
const { createPageRecovery } = createRequire(import.meta.url)('../electron/src/page-recovery.js');
const tick = () => new Promise(resolve => setImmediate(resolve));

function fixture(options = {}) {
  const windows = [], handlers = new Map(), loads = [];
  class Window extends EventEmitter {
    constructor(options) { super(); this.options = options; this.webContents = new EventEmitter(); this.webContents.mainFrame = {}; this.webContents.getURL = () => this.url || 'http://localhost:8090/agent/chat/home/123?tab=a#end'; this.webContents.setWindowOpenHandler = () => {}; windows.push(this); }
    isDestroyed() { return !!this.destroyed; }
    async loadFile() {}
    async loadURL(url) { loads.push(url); this.url = url; }
    show() {}
    focus() {}
    close() { this.destroyed = true; this.emit('closed'); }
  }
  const owner = new Window();
  let enabled = true;
  const recovery = createPageRecovery({ BrowserWindow: Window, ipcMain: { handle: (key, fn) => handlers.set(key, fn) }, dialog: { showMessageBox: async () => ({ response: 2 }) }, locale: () => 'zh-CN', enabled: () => enabled, ...options });
  recovery.attach(owner, 'http://localhost:8090/agent/chat/home');
  const invoke = (action = 'retry', event) => {
    const popup = windows.at(-1);
    return handlers.get('lazymind:pageRecovery')(event || { sender: popup.webContents, senderFrame: popup.webContents.mainFrame }, action);
  };
  return { owner, windows, loads, invoke, Window, disable: () => { enabled = false; } };
}

test('crash offers recovery and restores the same trusted page without replaying work', async () => {
  const f = fixture(); f.owner.webContents.emit('did-navigate', {}, f.owner.webContents.getURL());
  f.owner.webContents.emit('render-process-gone', {}, { reason: 'crashed' }); await tick();
  assert.equal(f.windows.length, 2); assert.deepEqual(f.loads, []);
  assert.equal(f.windows[1].options.webPreferences.sandbox, true);
  assert.equal(f.windows[1].options.webPreferences.nodeIntegration, false);
  await f.invoke(); assert.deepEqual(f.loads, ['http://localhost:8090/agent/chat/home/123?tab=a#end']);
  assert.equal(f.windows[1].isDestroyed(), true);
});

test('ignores clean shutdown, subframe failures, cancellation and shutdown work', async () => {
  const f = fixture();
  f.owner.webContents.emit('render-process-gone', {}, { reason: 'clean-exit' });
  f.owner.webContents.emit('did-fail-load', {}, -3, 'aborted', '', true);
  f.owner.webContents.emit('did-fail-load', {}, -105, 'failed', '', false);
  f.disable(); f.owner.webContents.emit('render-process-gone', {}, { reason: 'crashed' });
  await tick(); assert.equal(f.windows.length, 1);
});

test('rejects foreign IPC and duplicate retry; a failed retry remains recoverable', async () => {
  const f = fixture(); f.owner.webContents.emit('render-process-gone', {}, { reason: 'oom' }); await tick();
  await assert.rejects(f.invoke('retry', { sender: {}, senderFrame: {} }));
  await assert.rejects(f.invoke('retry', { sender: f.windows[1].webContents, senderFrame: {} }));
  await assert.rejects(f.invoke('https://untrusted.example'));
  let reject, count = 0;
  f.owner.loadURL = () => { count++; return new Promise((_, fail) => { reject = fail; }); };
  const first = f.invoke(); await f.invoke(); assert.equal(count, 1);
  reject(new Error('private')); assert.deepEqual(await first, { ok: false });
  assert.equal(f.windows[1].isDestroyed(), false);
  f.owner.loadURL = async () => { count++; }; await f.invoke('home'); assert.equal(count, 2);
});

test('a failed recovery document falls back to a native dialog and never retries automatically', async () => {
  let prompts = 0, retries = 0;
  const f = fixture({ dialog: { showMessageBox: async () => ({ response: prompts++ === 0 ? 0 : 2 }) } });
  f.Window.prototype.loadFile = async () => { throw new Error('load failed'); };
  f.owner.loadURL = async () => { retries++; throw new Error('offline'); };
  f.owner.webContents.emit('render-process-gone', {}, { reason: 'crashed' });
  f.owner.webContents.emit('render-process-gone', {}, { reason: 'crashed' });
  await tick();
  assert.equal(f.windows.length, 2); assert.equal(f.windows[1].isDestroyed(), true);
  assert.equal(retries, 1); assert.equal(prompts, 2);
});

test('never restores an external URL and cleans up with the owner window', async () => {
  const f = fixture(); f.owner.webContents.emit('did-navigate', {}, 'https://example.org/secret');
  f.owner.webContents.emit('did-fail-load', {}, -105, 'failed', 'http://localhost:8090/agent/chat/home', true); await tick();
  await f.invoke(); assert.deepEqual(f.loads, ['http://localhost:8090/agent/chat/home']);
  f.owner.webContents.emit('render-process-gone', {}, { reason: 'crashed' }); await tick();
  f.owner.close(); assert.equal(f.windows.at(-1).isDestroyed(), true);
});
