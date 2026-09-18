const fs = require('node:fs');
const path = require('node:path');
const net = require('node:net');
const crypto = require('node:crypto');
const { execFile, execFileSync } = require('node:child_process');

function installHelper(source, root) {
  const target = path.join(root, 'LazyMind Recorder.app');
  const marker = 'Contents/Resources/build-id';
  const version = fs.readFileSync(path.join(source, marker), 'utf8');
  if (!/^[a-f0-9]{64}$/.test(version)) throw new Error('recording-helper-invalid-build');
  // Never re-sign or replace an unchanged helper when the main app updates.
  if (fs.existsSync(path.join(target, marker)) && fs.readFileSync(path.join(target, marker), 'utf8') === version) return target;
  execFileSync('/usr/bin/codesign', ['--verify', '--strict', source], { stdio: 'pipe' });
  fs.mkdirSync(root, { recursive: true, mode: 0o700 });
  const staging = fs.mkdtempSync(path.join(root, '.install-'));
  const copied = path.join(staging, 'LazyMind Recorder.app');
  try {
    execFileSync('/usr/bin/ditto', [source, copied]);
    execFileSync('/usr/bin/codesign', ['--verify', '--strict', copied], { stdio: 'pipe' });
    const previous = `${target}.previous`;
    fs.rmSync(previous, { recursive: true, force: true });
    if (fs.existsSync(target)) fs.renameSync(target, previous);
    try { fs.renameSync(copied, target); }
    catch (error) { if (fs.existsSync(previous)) fs.renameSync(previous, target); throw error; }
  } finally { fs.rmSync(staging, { recursive: true, force: true }); }
  return target;
}

function createRecordingHelper({ source, root, onEvent = () => {}, log = () => {}, launch = execFile, install = installHelper }) {
  let socket, server, directory, ready, sequence = 0, active = false, closed = true;
  const pending = new Map();
  function rejectPending() { for (const value of pending.values()) { clearTimeout(value.timer); value.reject(new Error('recording-helper-disconnected')); } pending.clear(); }
  function dispose() {
    closed = true; active = false;
    if (socket && !socket.destroyed) socket.destroy(); socket = null;
    server?.close(); server = null;
    if (directory) fs.rmSync(directory, { recursive: true, force: true }); directory = null;
    rejectPending(); ready = null;
  }
  function ensure() {
    if (ready) return ready;
    closed = false;
    ready = new Promise((resolve, reject) => {
      let settled = false;
      const timeout = setTimeout(() => fail(new Error('recording-helper-start-timeout')), 15000);
      const fail = (error) => { if (!settled) { settled = true; clearTimeout(timeout); reject(error); dispose(); } };
      try {
        const app = install(source, root);
        directory = fs.mkdtempSync('/tmp/lmr-'); fs.chmodSync(directory, 0o700);
        const address = path.join(directory, 'rpc.sock'); const secret = crypto.randomBytes(32).toString('hex');
        server = net.createServer((candidate) => {
          let buffer = Buffer.alloc(0), authenticated = false;
          candidate.on('data', (bytes) => {
            buffer = Buffer.concat([buffer, bytes]);
            if (buffer.length > 32 * 1024 * 1024) { candidate.destroy(); return; }
            let end;
            while ((end = buffer.indexOf(10)) !== -1) {
              const line = buffer.subarray(0, end); buffer = buffer.subarray(end + 1);
              let message; try { message = JSON.parse(line.toString()); } catch { candidate.destroy(); return; }
              if (!authenticated) {
                if (message.hello !== secret || socket) { candidate.destroy(); return; }
                authenticated = true; socket = candidate; settled = true; clearTimeout(timeout); resolve(); continue;
              }
              if (message.event) { if (message.event === 'ended' || message.event === 'failed') active = false; onEvent(message); continue; }
              const waiting = pending.get(message.id); if (!waiting) continue;
              pending.delete(message.id); clearTimeout(waiting.timer);
              if (message.error) waiting.reject(new Error(message.error)); else waiting.resolve(message.result);
            }
          });
          candidate.on('error', () => {});
          candidate.on('close', () => {
            if (socket !== candidate || closed) return;
            const wasActive = active; dispose();
            if (wasActive) onEvent({ event: 'failed', error: 'recording-helper-disconnected' });
          });
        });
        server.on('error', fail);
        server.listen(address, () => {
          fs.chmodSync(address, 0o600);
          launch('/usr/bin/open', ['-n', '-a', app, '--args', '--socket', address, '--token', secret, '--parent', String(process.pid)], (error) => { if (error) fail(error); });
        });
      } catch (error) { fail(error); }
    });
    return ready;
  }
  async function request(method, payload = {}, timeoutMs = 15000) {
    await ensure();
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error('recording-helper-request-timeout')); dispose(); }, timeoutMs);
      pending.set(id, { resolve, reject, timer });
      socket.write(JSON.stringify({ id, method, ...payload }) + '\n');
    });
  }
  return {
    async start() {
      if (active) throw new Error('recording-busy');
      // A fresh permission-owning process sees newly granted TCC permissions.
      dispose();
      const permissions = await request('permissions');
      if (!permissions.input) throw new Error('recording-helper-input-permission');
      if (!permissions.screen) throw new Error('recording-helper-screen-permission');
      active = true;
      try { return await request('start', {}, 180000); }
      catch (error) { active = false; log('start-failed', error.message); throw error; }
    },
    async stop(id) { try { return await request('stop', { session_id: id }); } finally { active = false; } },
    async cancel() { dispose(); },
    async settings() { await request('permissions'); return request('settings'); },
    dispose,
  };
}
module.exports = { installHelper, createRecordingHelper };
