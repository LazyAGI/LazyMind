import net from 'node:net';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';

export async function listenBridge(directory, kind, accept, platform = process.platform) {
  const tcp = platform === 'win32';
  const token = tcp ? crypto.randomBytes(32).toString('hex') : null;
  const server = net.createServer(socket => {
    if (!tcp) { accept(socket); return; }
    let buffer = Buffer.alloc(0);
    const timer = setTimeout(() => socket.destroy(), 5000);
    socket.on('error', () => {});
    socket.once('close', () => clearTimeout(timer));
    const authenticate = chunk => {
      buffer = Buffer.concat([buffer, chunk]);
      const end = buffer.indexOf(10);
      if (end < 0) { if (buffer.length > 64) socket.destroy(); return; }
      const candidate = buffer.subarray(0, end);
      if (candidate.length !== 64 || !crypto.timingSafeEqual(candidate, Buffer.from(token))) { socket.destroy(); return; }
      clearTimeout(timer); socket.pause(); socket.off('data', authenticate);
      if (buffer.length > end + 1) socket.unshift(buffer.subarray(end + 1));
      accept(socket); socket.resume();
    };
    socket.on('data', authenticate);
  });
  const socketPath = tcp ? null : path.join(directory, `${kind}.sock`);
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    if (tcp) server.listen(0, '127.0.0.1', resolve); else server.listen(socketPath, resolve);
  });
  if (socketPath) fs.chmodSync(socketPath, 0o600);
  return { server, socketPath, endpoint: tcp ? { port: server.address().port, token } : undefined };
}
export function connectBridge(bridge, kind) {
  if (process.platform !== 'win32') return net.createConnection(path.join(bridge.dir, `${kind}.sock`));
  const endpoint = bridge.endpoints?.[kind];
  if (!endpoint || !Number.isInteger(endpoint.port) || endpoint.port < 1 || endpoint.port > 65535 || !/^[a-f0-9]{64}$/.test(endpoint.token)) throw new Error('Invalid authenticated bridge endpoint');
  const socket = net.createConnection({ host: '127.0.0.1', port: endpoint.port });
  socket.once('connect', () => socket.write(endpoint.token + '\n'));
  return socket;
}
