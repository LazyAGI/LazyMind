const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { once } = require('node:events');
const WebSocket = require('ws');
const { BrowserConnection } = require('../src/browser-connection');
const { profilePartition } = require('../src/managed-browser');

async function until(check) {
  for (let i = 0; i < 200; i++) {
    if (check()) return;
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  assert.fail('Timed out waiting for browser connection');
}

test('automatically pairs, runs ordered commands, reconnects after Core restart, and clears on logout', async () => {
  let pairings = 0;
  let invalidDevice = false;
  let peer;
  const calls = [];
  const disposed = [];
  const server = http.createServer(async (request, response) => {
    let body = '';
    for await (const chunk of request) body += chunk;
    response.setHeader('Content-Type', 'application/json');
    if (request.url === '/api/core/browser/manage/pairings') {
      assert.equal(request.headers.authorization, 'Bearer signed-in-user');
      response.end(JSON.stringify({ data: { code: `pair-${++pairings}` } }));
    } else {
      assert.equal(JSON.parse(body).code, `pair-${pairings}`);
      assert.equal(JSON.parse(body).browser, 'Microsoft Edge');
      assert.equal(JSON.parse(body).device_name, 'Microsoft Edge');
      assert.equal(JSON.parse(body).browser_version, '');
      response.end(JSON.stringify({ device_id: `device-${pairings}`, device_token: 'device-secret' }));
    }
  });
  const wss = new WebSocket.WebSocketServer({ server });
  wss.on('connection', socket => {
    peer = socket;
    socket.on('message', raw => {
      const message = JSON.parse(String(raw));
      if (message.type === 'hello') {
        assert.equal(message.device_token, 'device-secret');
        socket.send(JSON.stringify({ type: invalidDevice ? 'hello_error' : 'hello_ack' }));
        invalidDevice = false;
      }
    });
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const connection = new BrowserConnection({
    WebSocket, fetch, version: 'test', browserVersion: 'test',
    createController: async (_server, user) => ({
      browserName: 'Microsoft Edge', browserVersion: '',
      async dispatch(action, payload) { calls.push(action); return { action, payload }; },
      async dispose() { disposed.push(user); },
    }),
  });
  // Short retry for the test; production uses two seconds.
  const schedule = connection.schedule.bind(connection);
  connection.schedule = () => schedule(1);
  const auth = { server_url: `http://127.0.0.1:${server.address().port}`, user_id: 'alice', access_token: 'signed-in-user' };
  try {
    await connection.setSession(auth);
    await until(() => connection.state === 'connected');
    assert.equal(pairings, 1);
    const result = once(peer, 'message');
    peer.send(JSON.stringify({ type: 'command', id: 'one', action: 'snapshot' }));
    assert.deepEqual(JSON.parse(String((await result)[0])), {
      type: 'result', id: 'one', ok: true, result: { action: 'snapshot', payload: {} },
    });
    const expired = once(peer, 'message');
    peer.send(JSON.stringify({ type: 'command', id: 'old', action: 'click', deadline_ms: 1 }));
    assert.equal(JSON.parse(String((await expired)[0])).error.code, 'ACTION_TIMEOUT');
    assert.deepEqual(calls, ['snapshot']);
    await connection.setSession(auth);
    assert.equal(pairings, 1, 'auth synchronization must not create duplicate devices');
    invalidDevice = true;
    peer.close();
    await until(() => pairings === 2 && connection.state === 'connected');
    await connection.setSession(null);
    assert.equal(connection.state, 'signed_out');
    assert.deepEqual(disposed, ['alice']);
    assert.equal(connection.auth, null);
    await assert.rejects(connection.open('https://example.com'), /Sign in/);
  } finally {
    await connection.clear();
    for (const socket of wss.clients) socket.terminate();
    wss.close();
    await new Promise(resolve => server.close(resolve));
  }
});

test('account and server identities select separate persistent browser profiles', () => {
  const first = profilePartition('https://one.test', 'alice');
  assert.ok(first.startsWith('persist:'));
  assert.equal(first, profilePartition('https://one.test', 'alice'));
  assert.notEqual(first, profilePartition('https://one.test', 'bob'));
  assert.notEqual(first, profilePartition('https://two.test', 'alice'));
  assert.ok(!first.includes('alice'));
});

test('logout during pairing does not install credentials or create a websocket', async () => {
  let resolvePairing;
  const connection = new BrowserConnection({
    WebSocket: class { constructor() { assert.fail('stale connection created'); } },
    fetch: () => new Promise(resolve => { resolvePairing = resolve; }),
    createController: async () => ({ dispose: async () => {} }),
  });
  await connection.setSession({ server_url: 'https://example.test', user_id: 'alice', access_token: 'token' });
  clearTimeout(connection.timer);
  const pending = connection.connect();
  await connection.clear();
  resolvePairing({ ok: true, json: async () => ({ data: { code: 'old' } }) });
  await pending;
  assert.equal(connection.device, null);
  assert.equal(connection.state, 'signed_out');
});

test('a failed browser initialization can recover on the next session synchronization', async () => {
  let attempts = 0;
  const connection = new BrowserConnection({
    createController: async () => {
      if (++attempts === 1) throw new Error('runtime unavailable');
      return { dispose: async () => {} };
    },
  });
  const auth = { server_url: 'https://example.test', user_id: 'alice', access_token: 'token' };
  await assert.rejects(connection.setSession(auth), /runtime unavailable/);
  await connection.setSession(auth);
  assert.equal(attempts, 2);
  assert.ok(connection.controller);
  await connection.clear();
});
