import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const windows = process.platform === 'win32';
// Execute the very same static Windows-side wrapper used by the WSL helper.
const wrapper = readFileSync(path.join(repo, 'local/scripts/assistant-bridge-wsl.sh'), 'utf8')
  .match(/-Command '([\s\S]*?)'\); then/)[1];

function fixture(t, binary) {
  const parent = mkdtempSync(path.join(os.tmpdir(), 'lazymind-win-'));
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const root = path.join(parent, "O'Brien & $cash `cache` [资料]");
  mkdirSync(root);
  const script = path.join(root, 'assistant-bridge-win.ps1');
  const source = path.join(root, 'lazymind.exe');
  copyFileSync(path.join(repo, 'local/scripts/assistant-bridge-win.ps1'), script);
  if (binary) copyFileSync(binary, source);
  const env = { ...process.env, LOCALAPPDATA: path.join(root, 'app-data'), USERPROFILE: root,
    LAZYMIND_HOME: '/invalid/wsl/home', LAZYMIND_LOCAL_BUILD_ROOT: '/invalid/wsl/build',
    LAZYMIND_WSL_BRIDGE_SCRIPT: script, LAZYMIND_WSL_BRIDGE_SOURCE: source,
    BRIDGE_PROBE_LOG: path.join(root, 'calls.jsonl'), BRIDGE_PROBE_MODE: 'success' };
  const installed = path.join(env.LOCALAPPDATA, 'LazyMind/assistant-bridge/lazymind.exe');
  const run = (action) => spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-Command', wrapper],
    { env: { ...env, LAZYMIND_WSL_BRIDGE_ACTION: action }, encoding: 'utf8', timeout: 30000 });
  const calls = () => readFileSync(env.BRIDGE_PROBE_LOG, 'utf8').trim().split('\n').map(JSON.parse);
  return { env, script, source, installed, run, calls };
}

function success(result, action) {
  assert.equal(result.status, 0, result.stderr || String(result.error));
  assert.equal(result.stdout.trim(), `LAZYMIND_ASSISTANT_BRIDGE_OK:${action}`);
}

test('Windows PowerShell preserves literal paths and normalizes native failures', { skip: !windows }, async t => {
  const build = mkdtempSync(path.join(os.tmpdir(), 'lazymind-probe-'));
  t.after(() => rmSync(build, { recursive: true, force: true }));
  const binary = path.join(build, 'probe.exe');
  const compiled = spawnSync('go', ['build', '-o', binary, path.join(repo, 'desktop/scripts/fixtures/assistant-bridge-probe.go')],
    { cwd: build, encoding: 'utf8', timeout: 120000 });
  assert.equal(compiled.status, 0, compiled.stderr || String(compiled.error));

  await t.test('start stages native executable, verifies health, replaces and stops it', t => {
    const f = fixture(t, binary);
    success(f.run('start'), 'start');
    assert.ok(existsSync(f.installed));
    assert.deepEqual(f.calls().map(call => [call.executable, call.args]), [
      [f.source, ['assistant', 'stop']], [f.installed, ['assistant', 'start']], [f.installed, ['assistant', 'status']],
    ]);
    assert.ok(f.calls().every(call => call.home === '' && call.buildRoot === ''));
    success(f.run('start'), 'start');
    rmSync(f.source);
    success(f.run('stop'), 'stop');
    assert.equal(f.calls().at(-1).executable, f.installed);
  });
  await t.test('stop with no executable is idempotent', t => success(fixture(t).run('stop'), 'stop'));
  for (const mode of ['missing-script', 'missing-source', 'fail-stop', 'fail-start', 'fail-status', 'invalid-json', 'not-running', 'wrong-platform', 'string-running']) {
    await t.test(`start fails truthfully for ${mode}`, t => {
      const f = fixture(t, binary);
      f.env.BRIDGE_PROBE_MODE = mode;
      if (mode === 'missing-script') rmSync(f.script);
      if (mode === 'missing-source') rmSync(f.source);
      const result = f.run('start');
      assert.equal(result.status, 1, result.stderr || String(result.error));
      assert.ok(!result.stdout.includes('LAZYMIND_ASSISTANT_BRIDGE_OK'));
    });
  }
  await t.test('stop normalizes the full native failure code', t => {
    const f = fixture(t, binary);
    f.env.BRIDGE_PROBE_MODE = 'fail-stop';
    const result = f.run('stop');
    assert.equal(result.status, 1, result.stderr);
    assert.ok(!result.stdout.includes('LAZYMIND_ASSISTANT_BRIDGE_OK'));
  });
});

test('real Windows Bridge starts, reports native staged identity and stops', {
  skip: !windows || !process.env.LAZYMIND_TEST_BRIDGE_EXE,
}, async t => {
  // Refuse to interfere with an existing local Bridge; CI owns this port.
  const listener = net.createServer();
  await new Promise((resolve, reject) => listener.once('error', reject).listen(19091, '127.0.0.1', resolve));
  await new Promise(resolve => listener.close(resolve));
  const f = fixture(t, process.env.LAZYMIND_TEST_BRIDGE_EXE);
  try {
    success(f.run('start'), 'start');
    const result = spawnSync(f.installed, ['assistant', 'status'], { encoding: 'utf8', timeout: 10000 });
    assert.equal(result.status, 0, result.stderr);
    const health = JSON.parse(result.stdout);
    assert.equal(health.running, true);
    assert.equal(health.platform, 'windows');
    assert.equal(health.executable.toLowerCase(), f.installed.toLowerCase());
    success(f.run('stop'), 'stop');
    const stopped = spawnSync(f.installed, ['assistant', 'status'], { encoding: 'utf8', timeout: 10000 });
    assert.notEqual(stopped.status, 0, 'Bridge listener must be closed after stop');
  } finally {
    f.run('stop');
  }
});
