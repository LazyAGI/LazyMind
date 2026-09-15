const test = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { PassThrough } = require('node:stream');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { createEdgeAdapter, findEdge, PipeConnection } = require('../src/edge-browser');
const { loadBrowserController } = require('../src/managed-browser');

test('finds system/user Edge installations without executing shell commands', () => {
  assert.equal(findEdge({ platform: 'darwin', home: '/users/test', exists: p => p.startsWith('/users/test/') }), '/users/test/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge');
  assert.match(findEdge({ platform: 'win32', env: { LOCALAPPDATA: '/user' }, exists: () => true }), /Microsoft[/\\]Edge[/\\]Application[/\\]msedge.exe$/);
  assert.equal(findEdge({ exists: () => false }), undefined);
  assert.throws(() => createEdgeAdapter({ executable: null, profileDir: '/unused' }), /not installed/);
});

test('CDP pipe handles fragmented Unicode, events, errors and process exit', async () => {
  const child = Object.assign(new EventEmitter(), { stdio: [null, null, null, new PassThrough(), new PassThrough()] });
  const events = [];
  let closes = 0;
  const pipe = new PipeConnection(child, event => events.push(event), () => closes++);
  const response = pipe.send('Browser.getVersion');
  const bytes = Buffer.from(JSON.stringify({ id: 1, result: { name: '测试 Edge' } }) + '\0');
  for (const byte of bytes) child.stdio[4].write(Buffer.from([byte]));
  assert.deepEqual(await response, { name: '测试 Edge' });
  child.stdio[4].write('{"method":"Target.targetDestroyed","params":{"targetId":"x"}}\0');
  assert.equal(events[0].method, 'Target.targetDestroyed');
  const failed = pipe.send('Page.badCommand');
  child.stdio[4].write('{"id":2,"error":{"message":"invalid command"}}\0');
  await assert.rejects(failed, /invalid command/);
  const pending = pipe.send('Page.enable');
  child.emit('exit', 1);
  await assert.rejects(pending, /closed/);
  await assert.rejects(pipe.send('Page.enable'), /closed/);
  child.stdio[4].end();
  assert.equal(closes, 1);
});

// Opt in locally: LAZYMIND_TEST_EDGE=1 node --test tests/edge-browser.test.js
// Uses the actual installed Edge with a disposable profile and a local fixture.
test('real Edge opens, types, clicks, captures, navigates and persists its dedicated profile', {
  skip: process.env.LAZYMIND_TEST_EDGE !== '1', timeout: 90000,
}, async () => {
  assert.ok(findEdge(), 'Install Microsoft Edge before running this integration test');
  const profileDir = fs.mkdtempSync(path.join(os.tmpdir(), 'lazymind-edge-test-'));
  const server = http.createServer((_request, response) => {
    response.setHeader('Content-Type', 'text/html; charset=utf-8');
    response.end(`<!doctype html><title>LazyMind Edge test</title>
      <h1>Edge integration</h1><label>Name <input id="name"></label>
      <button onclick="document.querySelector('#result').textContent='Hello '+document.querySelector('#name').value; localStorage.setItem('saved','yes'); document.cookie='edge_test=ok; Max-Age=3600; SameSite=Lax'">Greet</button>
      <p id="result"></p><p id="saved"></p>
      <script>document.querySelector('#saved').textContent='Saved: '+localStorage.getItem('saved')</script>`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}`;
  const { BrowserController, captureCurrentPage } = await loadBrowserController(path.resolve(__dirname, '../../../browser-extension'));
  let adapter = createEdgeAdapter({ profileDir });
  try {
    let controller = new BrowserController(adapter);
    let snapshot = await controller.dispatch('open', { url, allow_private_network: true });
    assert.equal(snapshot.title, 'LazyMind Edge test');
    const session_id = snapshot.session_id;
    const field = snapshot.elements.find(e => e.role === 'textbox' && e.name === 'Name');
    assert.ok(field, JSON.stringify(snapshot.elements));
    snapshot = await controller.dispatch('type', { session_id, ref: field.ref, expected_revision: snapshot.revision, text: 'LazyMind 测试', replace: true, verify_text: 'LazyMind 测试' });
    const button = snapshot.elements.find(e => e.role === 'button' && e.name === 'Greet');
    snapshot = await controller.dispatch('click', { session_id, ref: button.ref, expected_revision: snapshot.revision });
    assert.ok(snapshot.elements.some(e => e.name.includes('Hello LazyMind 测试')));
    const capture = await captureCurrentPage({}, adapter);
    assert.ok(JSON.stringify(capture).includes('Hello LazyMind 测试'));
    const shot = await controller.dispatch('screenshot', { session_id });
    assert.ok(Buffer.from(shot.data_base64, 'base64').length > 1000);
    const tabs = await controller.dispatch('tabs', { session_id });
    await assert.rejects(adapter.debugger.sendCommand({ tabId: 'personal-edge-tab' }, 'Runtime.evaluate', {}), /closed/);
    await assert.rejects(adapter.windows.create({ url: 'file:///etc/passwd' }), /HTTP/);
    snapshot = await controller.dispatch('navigate', { session_id, url: `${url}/next`, allow_private_network: true });
    assert.ok(snapshot.elements.some(e => e.name === 'Saved: yes'));
    await controller.dispatch('close', { session_id });
    await assert.rejects(adapter.tabs.get(tabs.tabs[0].tab_id), /closed/);
    await adapter.dispose();
    adapter = createEdgeAdapter({ profileDir });
    controller = new BrowserController(adapter);
    snapshot = await controller.dispatch('open', { url, allow_private_network: true });
    assert.ok(snapshot.elements.some(e => e.name === 'Saved: yes'), 'local storage persists after process restart');
    const [tab] = await adapter.tabs.query();
    const cookies = await adapter.debugger.sendCommand({ tabId: tab.id }, 'Runtime.evaluate', { expression: 'document.cookie', returnByValue: true });
    assert.ok(cookies.result.value.includes('edge_test=ok'), 'persistent cookies survive restart');
    await controller.dispatch('close', { session_id: snapshot.session_id });
  } finally {
    await adapter.dispose();
    await new Promise(resolve => server.close(resolve));
    fs.rmSync(profileDir, { recursive: true, force: true });
  }
});
