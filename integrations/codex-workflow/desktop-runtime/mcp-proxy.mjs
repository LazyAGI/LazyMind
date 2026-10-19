import readline from 'node:readline';
import { loadConfig, logger, kinds } from './config.mjs';
import { selectBridge } from './bridge-registry.mjs';
import { connectBridge } from './bridge-transport.mjs';

const config = loadConfig();
const kind = process.argv[2];
if (!kinds.includes(kind)) throw new Error('Unknown Desktop bridge');
const log = logger(config, `proxy-${kind}`);
const output = message => process.stdout.write(JSON.stringify(message) + '\n');
let socket = null, initial = null, initialized = false, connecting = null, generation = 0;
let internal = null;
const pending = new Map();
const fail = (message, text) => {
  if (message.id !== undefined && message.method) output({ jsonrpc: '2.0', id: message.id, error: { code: -32001, message: text } });
};

async function connect() {
  // Wait for the initialization barrier even if the replacement socket is open.
  if (connecting) return connecting;
  if (socket && !socket.destroyed) return;
  connecting = (async () => {
    const bridge = selectBridge(config.runtimeDir);
    const current = connectBridge(bridge, kind);
    const epoch = ++generation;
    let buffer = '';
    current.setEncoding('utf8');
    current.on('data', chunk => {
      buffer += chunk;
      if (Buffer.byteLength(buffer) > 32 * 1024 * 1024) { current.destroy(); return; }
      let end;
      while ((end = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        let message;
        try { message = JSON.parse(line); } catch { current.destroy(); return; }
        if (internal && message.id === internal.id && epoch === internal.epoch && !message.method) {
          const waiter = internal; internal = null;
          if (message.error) waiter.reject(new Error('Bridge reinitialize failed'));
          else waiter.resolve();
          continue;
        }
        if (socket !== current) continue;
        if (message.id !== undefined && !message.method) pending.delete(JSON.stringify(message.id));
        // All native context, server requests and approvals pass through unchanged.
        output(message);
      }
    });
    current.on('error', error => log('socket_error', { code: error.code, epoch }));
    current.on('close', () => {
      if (socket !== current) return;
      socket = null;
      if (internal) { internal.reject(new Error('Bridge lost during initialize')); internal = null; }
      log('disconnected', { epoch, inFlight: pending.size });
      for (const request of pending.values()) fail(request, 'Desktop bridge disconnected; execution outcome unknown; request was NOT replayed. Verify state before a new request.');
      pending.clear();
    });
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => { current.destroy(); reject(new Error('Bridge connect timeout')); }, 5000);
      current.once('connect', () => { clearTimeout(timer); resolve(); });
      current.once('error', error => { clearTimeout(timer); reject(error); });
    });
    socket = current;
    log('connected', { bridge: bridge.pid, desktop: bridge.ppid, epoch });
    if (initial && initialized) {
      try {
        await new Promise((resolve, reject) => {
          const id = `bridge-reinitialize-${process.pid}-${epoch}`;
          const timer = setTimeout(() => { internal = null; reject(new Error('Bridge initialize timeout')); }, 10000);
          internal = { id, epoch, resolve: () => { clearTimeout(timer); resolve(); }, reject: error => { clearTimeout(timer); reject(error); } };
          current.write(JSON.stringify({ ...initial, id }) + '\n');
        });
        current.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) + '\n');
      } catch (error) { current.destroy(); throw error; }
    }
  })();
  try { await connecting; } finally { connecting = null; }
}

let queue = Promise.resolve();
readline.createInterface({ input: process.stdin }).on('line', line => {
  let message;
  try { message = JSON.parse(line); } catch { return; }
  queue = queue.then(async () => {
    try {
      // Responses/cancellations for a lost plugin session cannot target a new one.
      if ((!socket || socket.destroyed) && (!message.method || message.method === 'notifications/cancelled')) return;
      await connect();
      if (message.method === 'initialize') initial = message;
      if (message.method === 'notifications/initialized') initialized = true;
      if (message.id !== undefined && message.method) pending.set(JSON.stringify(message.id), message);
      socket.write(line + '\n');
    } catch (error) { fail(message, `Request not sent: ${error.message}`); }
  });
}).on('close', () => { socket?.destroy(); process.exit(0); });
process.on('SIGTERM', () => { socket?.destroy(); process.exit(0); });
process.stdout.on('error', () => { socket?.destroy(); process.exit(1); });
