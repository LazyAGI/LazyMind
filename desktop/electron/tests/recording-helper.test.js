const { test } = require('node:test');
const assert = require('node:assert/strict');
const net = require('node:net');
const { createRecordingHelper } = require('../src/recording-helper');
function fixture(permissions = { input: true, screen: true }) {
  const clients = [], commands = [], events = [];
  const helper = createRecordingHelper({ source: '', root: '', install: () => '/stable/helper.app', onEvent: e => events.push(e), launch: (_cmd, args, callback) => {
    const socket = net.connect(args[args.indexOf('--socket') + 1]); clients.push(socket);
    socket.on('connect', () => socket.write(JSON.stringify({ hello: args[args.indexOf('--token') + 1] }) + '\n'));
    let buffer = '';
    socket.on('data', bytes => {
      buffer += bytes; let end;
      while ((end = buffer.indexOf('\n')) >= 0) {
        const command = JSON.parse(buffer.slice(0, end)); buffer = buffer.slice(end + 1); commands.push(command);
        const result = command.method === 'permissions' ? permissions : command.method === 'start' ? { session_id: 'test', startedAt: Date.now() } : { frames: [], evidence: { events: [] } };
        socket.write(JSON.stringify({ id: command.id, result }) + '\n');
      }
    }); socket.on('error', () => {}); callback(null);
  } });
  return { helper, clients, commands, events };
}
test('each recording refreshes only the helper and checks permissions before capture', async () => {
  const app = fixture();
  try {
    await app.helper.start(); await app.helper.stop('test'); await app.helper.start();
    assert.equal(app.clients.length, 2);
    assert.deepEqual(app.commands.map(c => c.method), ['permissions', 'start', 'stop', 'permissions', 'start']);
  } finally { app.helper.dispose(); }
});
test('missing screen permission never starts capture and retry can succeed', async () => {
  const permissions = { input: true, screen: false }; const app = fixture(permissions);
  try {
    await assert.rejects(app.helper.start(), /recording-helper-screen-permission/);
    assert.equal(app.commands.some(c => c.method === 'start'), false);
    permissions.screen = true; await app.helper.start(); assert.equal(app.clients.length, 2);
  } finally { app.helper.dispose(); }
});
test('cancel closes the private IPC connection and crash ends the session', async () => {
  const app = fixture();
  try {
    await app.helper.start(); app.clients[0].destroy();
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(app.events[0].event, 'failed');
    await app.helper.start(); await app.helper.cancel();
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(app.clients[1].destroyed, true);
  } finally { app.helper.dispose(); }
});
