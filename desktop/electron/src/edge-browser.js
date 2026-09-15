const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { spawn } = require('node:child_process');
const { StringDecoder } = require('node:string_decoder');

function findEdge({ platform = process.platform, env = process.env, home = os.homedir(), exists = fs.existsSync } = {}) {
  const candidates = platform === 'darwin'
    ? ['/Applications', path.join(home, 'Applications')].map(root => path.join(root, 'Microsoft Edge.app/Contents/MacOS/Microsoft Edge'))
    : platform === 'win32'
      ? [env['PROGRAMFILES(X86)'], env.PROGRAMFILES, env.LOCALAPPDATA].filter(Boolean).map(root => path.join(root, 'Microsoft/Edge/Application/msedge.exe'))
      : ['/usr/bin/microsoft-edge', '/usr/bin/microsoft-edge-stable', '/opt/microsoft/msedge/msedge'];
  return candidates.find(exists);
}

// CDP uses inherited pipes instead of a listening TCP port. Only our child
// process and its dedicated profile are connected; personal Edge is untouched.
class PipeConnection {
  constructor(child, onEvent, onClose) {
    this.child = child;
    this.pending = new Map();
    this.nextID = 0;
    const decoder = new StringDecoder('utf8');
    let buffer = '';
    child.stdio[4].on('data', chunk => {
      buffer += decoder.write(chunk);
      if (buffer.length > 32 * 1024 * 1024) return this.fail(new Error('Edge response is too large'));
      let end;
      while ((end = buffer.indexOf('\0')) !== -1) {
        const raw = buffer.slice(0, end);
        buffer = buffer.slice(end + 1);
        let message;
        try { message = JSON.parse(raw); } catch { continue; }
        const request = this.pending.get(message.id);
        if (request) {
          this.pending.delete(message.id);
          clearTimeout(request.timer);
          if (message.error) request.reject(new Error(message.error.message));
          else request.resolve(message.result);
        } else if (message.method) onEvent(message);
      }
    });
    this.onClose = onClose;
    child.once('error', error => this.fail(error));
    child.once('exit', () => this.fail(new Error('Edge browser closed')));
    child.stdio[3].on('error', error => this.fail(error));
    child.stdio[4].on('error', error => this.fail(error));
    child.stdio[4].once('end', () => this.fail(new Error('Edge browser connection closed')));
  }

  send(method, params = {}, sessionId) {
    if (this.closed) return Promise.reject(new Error('Edge browser connection closed'));
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Edge command timed out: ${method}`));
      }, 15000);
      this.pending.set(id, { resolve, reject, timer });
      this.child.stdio[3].write(`${JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) })}\0`, error => {
        if (error) this.fail(error);
      });
    });
  }

  fail(error) {
    if (this.closed) return;
    this.closed = true;
    for (const request of this.pending.values()) { clearTimeout(request.timer); request.reject(error); }
    this.pending.clear();
    this.onClose();
  }
}

function createEdgeAdapter({ executable = findEdge(), profileDir }) {
  if (!executable) throw new Error('Microsoft Edge is not installed');
  const targets = new Map();
  const updated = new Set();
  const detached = new Set();
  let connection, child, starting, activeID, disposed = false;
  const event = listeners => ({ addListener: fn => listeners.add(fn), removeListener: fn => listeners.delete(fn) });
  const closed = id => {
    if (!targets.delete(id)) return;
    for (const fn of detached) fn({ tabId: id });
  };
  function onEvent({ method, params, sessionId }) {
    if (method === 'Target.targetDestroyed') closed(params.targetId);
    if (method === 'Target.detachedFromTarget') {
      const target = [...targets.values()].find(t => t.sessionId === params.sessionId);
      if (target) {
        target.sessionId = null;
        for (const fn of detached) fn({ tabId: target.id });
      }
    }
    if (method === 'Page.loadEventFired') {
      const target = [...targets.values()].find(t => t.sessionId === sessionId);
      if (target) for (const fn of updated) fn(target.id, { status: 'complete' });
    }
  }
  async function start() {
    if (disposed) throw new Error('Edge adapter has been disposed');
    if (connection && !connection.closed) return;
    if (starting) return starting;
    starting = (async () => {
      fs.mkdirSync(profileDir, { recursive: true, mode: 0o700 });
      child = spawn(executable, [
        `--user-data-dir=${profileDir}`, '--remote-debugging-pipe', '--no-first-run',
        '--no-default-browser-check', '--no-startup-window',
      ], { stdio: ['ignore', 'ignore', 'ignore', 'pipe', 'pipe'], windowsHide: true });
      const currentChild = child;
      const current = new PipeConnection(child, onEvent, () => {
        for (const id of targets.keys()) closed(id);
        // Browser.close may end the pipe before cookies finish flushing.
        // Allow a normal exit before terminating a stuck owned process.
        const timer = setTimeout(() => {
          if (currentChild.exitCode === null && currentChild.signalCode === null) currentChild.kill('SIGKILL');
        }, 3000);
        timer.unref();
        currentChild.once('exit', () => clearTimeout(timer));
      });
      connection = current;
      try { await current.send('Target.setDiscoverTargets', { discover: true }); }
      catch (error) { current.fail(error); throw error; }
    })();
    try { await starting; } finally { starting = null; }
  }
  function get(id) {
    const target = targets.get(id);
    if (!target || !connection || connection.closed) throw new Error('Managed Edge tab is closed');
    return target;
  }
  async function attach(id) {
    const target = get(id);
    if (!target.sessionId) {
      target.sessionId = (await connection.send('Target.attachToTarget', { targetId: id, flatten: true })).sessionId;
      await connection.send('Page.enable', {}, target.sessionId);
    }
    return target;
  }
  async function send(id, method, params) {
    const target = await attach(id);
    return connection.send(method, params, target.sessionId);
  }
  async function tab(id) {
    const target = get(id);
    const { targetInfo } = await connection.send('Target.getTargetInfo', { targetId: id });
    const { result } = await send(id, 'Runtime.evaluate', { expression: 'document.readyState', returnByValue: true });
    return { id, windowId: target.windowId, url: targetInfo.url, title: targetInfo.title, status: result.value === 'complete' ? 'complete' : 'loading' };
  }
  async function remove(id) {
    get(id);
    await connection.send('Target.closeTarget', { targetId: id });
    closed(id);
  }
  return {
    windows: {
      async create({ url }) {
        if (!['http:', 'https:'].includes(new URL(url).protocol)) throw new Error('Only HTTP and HTTPS pages are supported');
        await start();
        const { targetId } = await connection.send('Target.createTarget', { url: 'about:blank', newWindow: true });
        targets.set(targetId, { id: targetId });
        activeID = targetId;
        try {
          const { windowId } = await connection.send('Browser.getWindowForTarget', { targetId });
          get(targetId).windowId = windowId;
          await connection.send('Browser.setWindowBounds', { windowId, bounds: { windowState: 'maximized' } });
          await send(targetId, 'Page.navigate', { url });
          return { id: windowId, tabs: [await tab(targetId)] };
        } catch (error) { await remove(targetId).catch(() => {}); throw error; }
      },
      async update(windowId) {
        const target = [...targets.values()].find(t => t.windowId === windowId);
        if (!target) throw new Error('Managed Edge window is closed');
        await connection.send('Target.activateTarget', { targetId: target.id });
      },
      async remove(windowId) {
        const owned = [...targets.values()].filter(t => t.windowId === windowId);
        if (!owned.length) throw new Error('Managed Edge window is closed');
        for (const target of owned) await remove(target.id);
      },
    },
    tabs: {
      get: tab,
      async update(id) { get(id); await connection.send('Target.activateTarget', { targetId: id }); activeID = id; return tab(id); },
      async query() { const id = targets.has(activeID) ? activeID : targets.keys().next().value; return id ? [await tab(id)] : []; },
      remove, onUpdated: event(updated),
    },
    debugger: {
      onDetach: event(detached),
      async attach({ tabId }) { await attach(tabId); },
      async detach({ tabId }) {
        const target = get(tabId);
        if (target.sessionId) await connection.send('Target.detachFromTarget', { sessionId: target.sessionId });
      },
      sendCommand: ({ tabId }, method, params) => send(tabId, method, params),
    },
    scripting: {
      async executeScript({ target, func, args }) {
        const value = await send(target.tabId, 'Runtime.evaluate', {
          expression: `(${func.toString()})(...${JSON.stringify(args || [])})`, returnByValue: true, awaitPromise: true,
        });
        if (value.exceptionDetails) throw new Error(value.exceptionDetails.text);
        return [{ result: value.result.value }];
      },
    },
    async dispose() {
      disposed = true;
      await starting?.catch(() => {});
      if (!child || child.exitCode !== null || child.signalCode !== null) return;
      const exiting = new Promise(resolve => child.once('exit', resolve));
      const timer = setTimeout(() => child.kill('SIGKILL'), 3000);
      if (!connection.closed) void connection.send('Browser.close').catch(() => {});
      else child.kill();
      await exiting;
      clearTimeout(timer);
    },
  };
}

module.exports = { createEdgeAdapter, findEdge, PipeConnection };
