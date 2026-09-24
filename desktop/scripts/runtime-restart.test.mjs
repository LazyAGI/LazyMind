import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { EventEmitter } from 'node:events';
const source = readFileSync(new URL('../electron/src/main.js', import.meta.url), 'utf8');
function fn(name, next) { return source.slice(source.indexOf(`function ${name}`), source.indexOf(`\n${next}`, source.indexOf(`function ${name}`))); }
test('restart pauses auxiliary respawn, stops before starting, and coalesces clicks', async () => {
  let release;
  const events = [];
  const down = new Promise(r => { release = r; });
  const context = vm.createContext({ runtimeRestartPromise: undefined, runtimeStopping: false,
    agentHostRestartTimer: undefined, agentHostStableTimer: undefined,
    agentHostProcess: { kill() { events.push('agent-stop'); } }, clearTimeout,
    appendStartupLog() {}, serializeError: String,
    runSidecar: async () => { assert.equal(context.runtimeStopping, true); events.push('down'); await down; },
    detachRuntimeMonitor() { events.push('detach'); },
    startRuntime() { assert.equal(context.runtimeStopping, false); events.push('start'); },
    waitForRuntimeReady: async () => { events.push('ready'); return { ok: true }; },
    startAgentHost() { events.push('agent-start'); },
  });
  vm.runInContext(fn('restartRuntimeAfterFolderAccessChange', 'function logStartupContext'), context);
  const first = context.restartRuntimeAfterFolderAccessChange();
  assert.equal(first, context.restartRuntimeAfterFolderAccessChange());
  assert.deepEqual(events, ['agent-stop', 'down']);
  release(); await first;
  assert.deepEqual(events, ['agent-stop', 'down', 'detach', 'start', 'ready', 'agent-start']);
  assert.equal(context.runtimeRestartPromise, undefined);
});
test('detached old monitor cannot publish a late close into the next generation', () => {
  const proc = new EventEmitter();
  let staleClose = false;
  proc.on('close', () => { staleClose = true; });
  proc.kill = () => {}; proc.unref = () => {};
  const context = vm.createContext({ runtimeProcess: proc, isWindows: false, appendStartupLog() {}, serializeError: String });
  vm.runInContext(fn('detachRuntimeMonitor', 'function spawnDetachedShutdownHelper'), context);
  context.detachRuntimeMonitor();
  proc.emit('close', 0, null);
  assert.equal(staleClose, false);
  assert.equal(context.runtimeProcess, null);
});
