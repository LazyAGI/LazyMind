import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

const source = readFileSync(new URL('./desktop-dev.sh', import.meta.url), 'utf8');
const functions = source.slice(source.indexOf('pid_is_running()'), source.indexOf('case "${1:-start}"'));
function running({ vite = true, runner = true, child = '', command = '' } = {}) {
  const result = spawnSync('bash', ['-c', `
${functions}
ELECTRON_DIR=/repo/desktop/electron
pid_is_running() { case "$1" in 10) ${vite};; 20) ${runner};; *) false;; esac; }
pgrep() { printf '%s' '${child}'; }
ps() { printf '%s' '${command}'; }
if desktop_dev_is_running 10 20; then echo running; else echo stopped; fi
`], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, '', 'helper must exist and execute without errors');
  return result.stdout.trim();
}

test('a surviving watcher and Vite without Electron are not a running desktop', () => {
  assert.equal(running(), 'stopped');
});
test('Vite and a live Electron child count as running', () => {
  assert.equal(running({ child: '30', command: '/runtime/Electron /repo/desktop/electron' }), 'running');
});
test('an unrelated child does not count as Electron', () => {
  assert.equal(running({ child: '30', command: 'sleep 100' }), 'stopped');
});
test('a missing Vite or watcher cannot count as a ready desktop', () => {
  assert.equal(running({ vite: false, child: '30', command: '/runtime/Electron /repo/desktop/electron' }), 'stopped');
  assert.equal(running({ runner: false }), 'stopped');
});

test('start and stop cannot enter a lifecycle transition owned by another invocation', async t => {
  const { mkdtempSync, mkdirSync, writeFileSync, existsSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { spawn } = await import('node:child_process');
  const root = mkdtempSync(join(tmpdir(), 'desktop-dev-lock-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, 'desktop/scripts'), { recursive: true });
  const script = join(root, 'desktop/scripts/desktop-dev.sh');
  // Only replace service bodies: exercise the actual command dispatcher/lock.
  const barrier = `
start_dev() { touch '${root}/entered'; while [[ ! -f '${root}/release' ]]; do sleep 0.02; done; }
stop_dev() { touch '${root}/stopped'; }
`;
  writeFileSync(script, source.replace('case "${1:-start}"', barrier + '\ncase "${1:-start}"'));
  const first = spawn('bash', [script, 'start']);
  const ended = new Promise(resolve => first.on('exit', resolve));
  t.after(() => first.kill());
  for (let i = 0; i < 200 && !existsSync(join(root, 'entered')); i++) await new Promise(r => setTimeout(r, 10));
  assert.ok(existsSync(join(root, 'entered')), 'first start reached barrier');
  const duplicate = spawnSync('bash', [script, 'start'], { encoding: 'utf8', timeout: 2000 });
  assert.notEqual(duplicate.status, 0);
  assert.match(duplicate.stderr, /transition|lock|progress/i);
  // Stop must fail rather than execute while startup owns the state files.
  const second = spawnSync('bash', [script, 'stop'], { encoding: 'utf8', timeout: 2000 });
  writeFileSync(join(root, 'release'), '');
  await ended;
  assert.notEqual(second.status, 0, second.stdout);
  assert.equal(existsSync(join(root, 'stopped')), false);
  assert.match(second.stderr, /transition|lock|progress/i);
  assert.equal(spawnSync('bash', [script, 'stop']).status, 0);
  assert.ok(existsSync(join(root, 'stopped')));
});

test('a live Vite and watcher in a hot-restart gap are not recovered', () => {
  const result = spawnSync('bash', ['-c', `
${functions}
STATE_DIR=/unused
mkdir() { :; }
read_pid() { echo 123; }
pid_is_running() { return 0; }
desktop_dev_is_running() { return 1; }
stop_pid_file() { echo UNSAFE_CLEANUP; }
start_dev
`], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /already running/);
  assert.doesNotMatch(result.stdout, /UNSAFE_CLEANUP|Recovering/);
});

test('PID identity is rechecked before SIGKILL without signaling a reused process', async t => {
  const { mkdtempSync, writeFileSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const root = mkdtempSync(join(tmpdir(), 'desktop-pid-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const file = join(root, 'vite.pid');
  writeFileSync(file, '123');
  writeFileSync(file + '.identity', 'original vite/bin/vite.js\n');
  const result = spawnSync('bash', ['-c', `
${functions}
pid_is_running() { return 0; }
ps() { echo 'node vite/bin/vite.js'; }
process_identity() { if [[ -f '${root}/changed' ]]; then echo 'replacement vite/bin/vite.js'; else echo 'original vite/bin/vite.js'; fi; }
kill() { echo "SIGNAL $*"; touch '${root}/changed'; }
sleep() { :; }
stop_pid_file '${file}' 'vite/bin/vite.js'
`], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /reused pid/);
  assert.doesNotMatch(result.stdout, /KILL/);
});
