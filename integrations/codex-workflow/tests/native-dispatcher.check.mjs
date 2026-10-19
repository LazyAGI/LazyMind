// Exercise the built entrypoint and actual HTTP/Unix-WebSocket transports.
// The Core and App Server are fixtures; no real Workflow or Desktop is changed.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { WebSocketServer } from 'ws';

test('built dispatcher starts Continue, settles receipt, then interrupts only its own turn', { timeout: 15000 }, async () => {
  const dir = fs.mkdtempSync('/private/tmp/lm-control-');
  const socket = path.join(dir, 'rpc.sock');
  const thread = '01a12f01-e372-7ef3-aaae-4a0a295d6700';
  const connector = 'host-' + 'a'.repeat(32), token = 'b'.repeat(64);
  const pairingFile = path.join(dir, 'pairing.json');
  fs.writeFileSync(pairingFile, JSON.stringify({ provider: 'codex', enabled: true, profile: dir, connector_id: connector, token }), { mode: 0o600 });
  fs.writeFileSync(path.join(dir, 'lazymind-shared-server.json'), JSON.stringify({ version: 1, profile: dir,
    app_server_url: `ws+unix://localhost${socket}:/rpc` }), { mode: 0o600 });
  const rpcCalls = [], settlements = [];
  let turns = [], cancelAvailable = false;
  const actions = [
    { id: 'continue-1', session_id: 'run-1', native_session_id: thread, kind: 'continue', status: 'pending' },
    { id: 'cancel-1', session_id: 'run-1', native_session_id: thread, kind: 'cancel', status: 'pending' },
  ];
  const control = () => ({ protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: cancelAvailable ? 'stopped' : 'continue', admission: { can_begin: !cancelAvailable },
    binding: { bound: true, provider: 'codex', driver_session_id: thread, connector_id: connector } });
  const app = http.createServer(); const ws = new WebSocketServer({ server: app });
  ws.on('connection', peer => peer.on('message', raw => {
    const message = JSON.parse(raw); rpcCalls.push(message);
    let result;
    if (message.method === 'initialize') result = { userAgent: 'codex/0.162.0-alpha.17.2', codexHome: dir, platformFamily: 'unix', platformOs: 'macos' };
    else if (message.method === 'thread/read') result = { thread: { id: thread, status: { type: turns.some(t => t.status === 'inProgress') ? 'active' : 'idle' }, turns } };
    else if (message.method === 'turn/start') {
      assert.equal(message.params.threadId, thread);
      turns = [{ id: 'native-turn', status: 'inProgress', items: [{ type: 'userMessage', clientId: message.params.clientUserMessageId, content: message.params.input }] }];
      result = { turn: turns[0] };
    } else if (message.method === 'turn/interrupt') {
      assert.deepEqual(message.params, { threadId: thread, turnId: 'native-turn' });
      turns[0].status = 'interrupted'; result = {};
    } else if (message.method === 'initialized') return;
    else { peer.send(JSON.stringify({ id: message.id, error: { code: -32601, message: 'unexpected fixture method' } })); return; }
    peer.send(JSON.stringify({ id: message.id, result }));
  }));
  const bridge = http.createServer(async (request, response) => {
    try {
      assert.equal(request.headers.authorization, `Bearer ${token}`);
      assert.equal(request.headers['x-lazymind-connector-id'], connector);
      let raw = ''; for await (const chunk of request) raw += chunk;
      const body = raw ? JSON.parse(raw) : {};
      const route = request.url.replace('/v1/workflow-host', '');
      let result;
      if (route === '/actions') result = { actions: actions.filter((a, i) => a.status === 'pending' && (i === 0 || cancelAvailable)) };
      else {
        const [, , id, operation] = route.split('/');
        const action = actions.find(a => a.id === id);
        assert.ok(action, route);
        if (operation === 'claim') { action.status = 'dispatching'; result = { action, control: control(), dispatch_token: 'fixture-token' }; }
        else if (operation === 'settle') {
          settlements.push({ id, ...body }); action.status = body.status;
          if (id === 'continue-1') cancelAvailable = true;
          result = {};
        } else result = { action, control: control() };
      }
      response.writeHead(200, { 'content-type': 'application/json' }); response.end(JSON.stringify(result));
    } catch (error) { response.writeHead(500); response.end(JSON.stringify({ error: String(error) })); }
  });
  let child, exited; let output = '';
  try {
    await new Promise((resolve, reject) => { app.once('error', reject); app.listen(socket, resolve); });
    await new Promise((resolve, reject) => { bridge.once('error', reject); bridge.listen(0, '127.0.0.1', resolve); });
    child = spawn(process.execPath, [fileURLToPath(new URL('../dist/main.mjs', import.meta.url)),
      '--codex-bin', '/fixture/queue-must-not-run', '--codex-home', dir, '--pairing-file', pairingFile,
      '--bridge-url', `http://127.0.0.1:${bridge.address().port}`], { stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout.on('data', data => { output += data; }); child.stderr.on('data', data => { output += data; });
    exited = new Promise(resolve => child.once('exit', resolve));
    const deadline = Date.now() + 8000;
    while (settlements.length < 2 && Date.now() < deadline && child.exitCode === null) await new Promise(resolve => setTimeout(resolve, 25));
    assert.equal(settlements.length, 2, output);
    assert.deepEqual(settlements.map(item => [item.id, item.status, item.native_event_seq]), [['continue-1', 'accepted', 0], ['cancel-1', 'accepted', 0]]);
    assert.equal(rpcCalls.filter(m => m.method === 'turn/start').length, 1);
    assert.equal(rpcCalls.filter(m => m.method === 'turn/interrupt').length, 1);
    assert.match(output, /native dispatcher ready/);
    const receipts = fs.readdirSync(pairingFile + '.native-receipts').map(file => JSON.parse(fs.readFileSync(path.join(pairingFile + '.native-receipts', file), 'utf8')));
    assert.ok(receipts.every(record => record.turnId === 'native-turn' && record.status === 'accepted'));
    for (const peer of ws.clients) peer.terminate();
    await exited;
    assert.ok(!fs.existsSync(pairingFile + '.runtime.lock'));
  } finally {
    if (child?.exitCode === null) { child.kill('SIGTERM'); await exited; }
    for (const peer of ws.clients) peer.terminate();
    await new Promise(resolve => ws.close(resolve));
    bridge.closeAllConnections();
    await new Promise(resolve => bridge.close(resolve));
    await new Promise(resolve => app.close(resolve));
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
