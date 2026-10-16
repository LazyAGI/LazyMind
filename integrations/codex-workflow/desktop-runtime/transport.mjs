import net from 'node:net';
import WebSocket from 'ws';
import { connectionTarget, authorization, runtimeEndpoint } from './endpoint.mjs';

export function openSocket(target) {
  const endpoint = typeof target === "string" ? { socketPath: target } : connectionTarget(runtimeEndpoint(target), target.tokenFile);
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(endpoint.url ?? 'ws+unix://localhost/socket:/rpc', {
      ...(endpoint.socketPath ? { createConnection: () => net.createConnection({ path: endpoint.socketPath }) } : { headers: authorization(endpoint.tokenFile) }),
      handshakeTimeout: 5000, maxPayload: 32 * 1024 * 1024,
      perMessageDeflate: false, followRedirects: false,
    });
    socket.once('open', () => resolve(socket));
    socket.once('error', reject);
  });
}

// The Desktop owns initialization and approval responses. This transport does not
// inject requests or reconnect/replay a partially delivered conversation.
export async function forwardDesktop(socketPath, input, output, onFailure) {
  const socket = await openSocket(socketPath);
  let buffer = '';
  const data = chunk => {
    buffer += chunk;
    if (Buffer.byteLength(buffer) > 32 * 1024 * 1024) return onFailure(new Error('Desktop message exceeds transport limit'));
    let end;
    while ((end = buffer.indexOf('\n')) >= 0) {
      const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
      if (line.trim()) socket.send(line, error => {
        if (error) onFailure(error);
        else if (socket.bufferedAmount < 1024 * 1024) input.resume();
      });
    }
    if (socket.bufferedAmount > 1024 * 1024) {
      input.pause();
    }
  };
  input.setEncoding('utf8');
  input.on('data', data);
  socket.on('message', (data, binary) => {
    if (binary) return onFailure(new Error('Unexpected binary App Server message'));
    if (!output.write(data.toString() + '\n')) { socket.pause(); output.once('drain', () => socket.resume()); }
  });
  socket.on('error', onFailure);
  socket.on('close', () => onFailure(new Error('Backend disconnected; in-flight outcome unknown; no replay')));
  return () => { input.off('data', data); socket.removeAllListeners('close'); socket.terminate(); };
}
