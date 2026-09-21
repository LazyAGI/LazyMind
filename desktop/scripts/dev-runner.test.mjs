import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

test('late exit of old Electron cannot discard the replacement child', async () => {
  const children = [], timers = new Map();
  let sequence = 0;
  const context = vm.createContext({
    require(name) {
      if (name === 'electron') return 'fake-electron';
      if (name === 'node:path') return { resolve: () => '/fake/electron', join: () => '/fake/electron/src' };
      if (name === 'node:fs') return { watch: () => ({ close() {} }) };
      if (name === 'node:child_process') return { spawn() {
        const child = Object.assign(new EventEmitter(), { pid: children.length + 1, exitCode: null, signalCode: null, signals: [] });
        child.kill = signal => child.signals.push(signal);
        children.push(child); return child;
      } };
      throw new Error(name);
    },
    __dirname: '/fake', process: { env: {}, on() {}, stdout: { write() {} }, stderr: { write() {} } },
    setTimeout(callback) { timers.set(++sequence, callback); return sequence; },
    clearTimeout(id) { timers.delete(id); },
  });
  vm.runInContext(readFileSync(new URL('../electron/scripts/dev-runner.js', import.meta.url), 'utf8'), context);
  const stopped = vm.runInContext('stopElectron()', context);
  [...timers.values()][0]();
  let stopCompleted = false;
  stopped.then(() => { stopCompleted = true; });
  await Promise.resolve();
  assert.equal(stopCompleted, false, 'SIGKILL request is not an exit acknowledgement');
  // Even a caller that starts early cannot lose the replacement reference.
  vm.runInContext('startElectron()', context);
  children[0].emit('exit', null, 'SIGKILL');
  await stopped;
  vm.runInContext('stopElectron()', context);
  assert.deepEqual(children[1].signals, ['SIGTERM']);
});
