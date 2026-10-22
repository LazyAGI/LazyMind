import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../electron/src/main.js', import.meta.url), 'utf8');
const declarations = source.match(/^const agent(?:Connector\w+TimeoutMs|Logins) = .+;$/gm).join('\n');
const functions = source.slice(source.indexOf('function runAgentConnector('), source.indexOf('\nfunction readAgentBindings('));

function harness(platform = 'darwin') {
  const children = [];
  const timers = new Map();
  const treeKills = [];
  let restarts = 0;
  const context = vm.createContext({
    URL, process: { env: {} }, isWindows: platform === 'win32', isQuitting: false,
    agentConnectorPath: '/fixture/connector', sidecarEnv: () => ({}),
    appendStartupLog() {}, serializeError: (error) => error.message,
    restartAgentHost: () => { restarts += 1; },
    execFile: (command, args, options, callback) => {
      treeKills.push({ command, args: Array.from(args), options });
      callback(null);
    },
    setTimeout: (callback, delay) => { const id = {}; timers.set(id, { callback, delay }); return id; },
    clearTimeout: (id) => timers.delete(id),
    spawn: (_path, _args, options) => {
      const child = new EventEmitter();
      child.pid = 4201 + children.length;
      child.exitCode = null;
      child.signalCode = null;
      child.stdout = new EventEmitter();
      child.stderr = new EventEmitter();
      child.kill = () => { child.killed = true; };
      child.options = options;
      children.push(child);
      return child;
    },
    runConnectorJSON: async (args) => {
      if (args[1] === 'binding') return { target: args[2], configured: args[3] === 'set' };
      const status = { agent: 'codex', display_name: 'Codex', state: 'enabled' };
      return args[2] === 'all' ? { agents: { codex: status } } : status;
    },
  });
  vm.runInContext(`${declarations}\n${functions}`, context);
  return {
    children, timers, treeKills, get restarts() { return restarts; },
    start: () => context.runAgentConnector('codex', 'login'),
    status: () => context.runAgentConnector('codex', 'status'),
    all: () => context.runAgentConnector('all', 'status'),
    bind: (target = 'codex-cli') => context.runAgentBinding(target, 'set', '/fixture/codex'),
  };
}

function finish(child, payload, code = 0, signal = null) {
  if (payload !== undefined) child.stdout.emit('data', typeof payload === 'string' ? payload : JSON.stringify(payload));
  child.exitCode = code;
  child.signalCode = signal;
  child.emit('exit', code, signal);
  child.emit('close', code, signal);
}

test('login JSON error is published through individual and all status without changing MCP state', async () => {
  const h = harness();
  assert.equal((await h.start()).login?.state, 'running');
  finish(h.children[0], { state: 'error', message: 'Codex login failed: missing native dependency' });
  const status = await h.status();
  assert.equal(status.state, 'enabled');
  assert.equal(status.login?.state, 'failed');
  assert.match(status.login.message, /missing native dependency/);
  assert.deepEqual((await h.all()).agents.codex.login, status.login);
});

test('successful login is retained after its process closes', async () => {
  const h = harness();
  await h.start();
  assert.equal(h.children[0].options.stdio[1], 'pipe');
  finish(h.children[0], { state: 'ready' });
  assert.equal((await h.status()).login?.state, 'succeeded');
  assert.equal(h.timers.size, 0);
  assert.equal(h.restarts, 1);
});

test('failed process publishes a bounded diagnostic from stderr', async () => {
  const h = harness();
  await h.start();
  h.children[0].stderr.emit('data', `${'x'.repeat(20_000)}\nCannot open browser`);
  finish(h.children[0], undefined, 1);
  const status = await h.status();
  assert.equal(status.login?.state, 'failed');
  assert.match(status.login.message, /Cannot open browser/);
  assert.ok(status.login.message.length <= 8192);
});

test('invalid or missing connector JSON cannot report successful login', async () => {
  for (const payload of ['', 'not JSON', '{}', 'null', '{"state":""}', '{"state":"unexpected"}']) {
    const h = harness();
    await h.start();
    finish(h.children[0], payload);
    assert.equal((await h.status()).login?.state, 'failed', `payload ${payload}`);
  }
});

test('spawn errors and cancellation are visible after independent status refresh', async () => {
  const h = harness();
  await h.start();
  h.children[0].emit('error', new Error('spawn EACCES'));
  finish(h.children[0], undefined, -2);
  assert.match((await h.status()).login?.message || '', /EACCES/);
  await h.start();
  finish(h.children[1], undefined, null, 'SIGTERM');
  assert.equal((await h.status()).login?.state, 'cancelled');
});

test('timeout is terminal even if the killed child closes later', async () => {
  const h = harness();
  await h.start();
  const timeout = [...h.timers.values()].find((timer) => timer.delay === 120_000);
  assert.ok(timeout, 'login should have a bounded timeout');
  timeout.callback();
  assert.equal(h.children[0].killed, true);
  finish(h.children[0], { state: 'ready' });
  const status = await h.status();
  assert.equal(status.login?.state, 'failed');
  assert.match(status.login.message, /timed out/i);
});

test('retry supersedes an older child completion and clears its failure', async () => {
  const h = harness();
  await h.start();
  await h.start();
  assert.equal(h.children[0].killed, true);
  finish(h.children[1], { state: 'ready' });
  finish(h.children[0], { state: 'error', message: 'old failed login' });
  assert.equal((await h.status()).login?.state, 'succeeded');
  assert.equal(h.restarts, 1);
});

test('relevant executable binding clears the login and invalidates stale completion', async () => {
  for (const target of ['codex-cli', 'codex-desktop']) {
    const h = harness();
    await h.start();
    await h.bind('cursor-cli');
    assert.equal((await h.status()).login?.state, 'running');
    await h.bind(target);
    assert.equal(h.children[0].killed, true);
    finish(h.children[0], { state: 'error', message: 'old binding' });
    assert.equal((await h.status()).login, undefined);
  }
});

for (const action of ['timeout', 'retry', 'rebind']) {
  test(`Windows ${action} terminates the connector subtree without reviving an old login`, async () => {
    const h = harness('win32');
    await h.start();
    if (action === 'timeout') [...h.timers.values()][0].callback();
    else if (action === 'retry') await h.start();
    else await h.bind();
    assert.equal(h.treeKills.length, 1);
    assert.equal(h.treeKills[0].command, 'taskkill');
    assert.deepEqual(h.treeKills[0].args, ['/PID', '4201', '/T', '/F']);
    assert.equal(h.treeKills[0].options.windowsHide, true);
    assert.ok(h.treeKills[0].options.timeout > 0 && h.treeKills[0].options.timeout <= 5000);
    finish(h.children[0], { state: 'ready' });
    assert.equal((await h.status()).login?.state,
      action === 'timeout' ? 'failed' : action === 'retry' ? 'running' : undefined);
  });
}

test('Windows does not invoke subtree termination without its own valid child PID', async () => {
  for (const pid of [undefined, 0, -1, 1.5, '4201']) {
    const h = harness('win32');
    await h.start();
    h.children[0].pid = pid;
    await h.bind();
    assert.equal(h.treeKills.length, 0);
    assert.equal(h.children[0].killed, true);
  }
});

for (const action of ['retry', 'rebind']) {
  test(`Windows ${action} never targets the PID of a completed login`, async () => {
    for (const state of ['ready', 'error']) {
      const h = harness('win32');
      await h.start();
      finish(h.children[0], { state });
      if (action === 'retry') await h.start();
      else await h.bind();
      assert.equal(h.treeKills.length, 0);
    }
  });
}

for (const action of ['timeout', 'retry', 'rebind']) {
  test(`Windows ${action} does not target an exited PID while stdio is still closing`, async () => {
    for (const [code, signal] of [[0, null], [null, 'SIGTERM']]) {
      const h = harness('win32');
      await h.start();
      const child = h.children[0];
      child.exitCode = code;
      child.signalCode = signal;
      child.emit('exit', code, signal);
      if (action === 'timeout') [...h.timers.values()][0].callback();
      else if (action === 'retry') await h.start();
      else await h.bind();
      assert.equal(h.treeKills.length, 0);
      finish(child, { state: 'ready' }, code, signal);
      assert.equal((await h.status()).login?.state,
        action === 'timeout' ? 'failed' : action === 'retry' ? 'running' : undefined);
    }
  });
}
