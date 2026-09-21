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
