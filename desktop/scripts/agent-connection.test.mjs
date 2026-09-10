import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../electron/src/main.js', import.meta.url), 'utf8');
const start = source.indexOf('function runAgentConnector(');
const end = source.indexOf('\nfunction startAgentLogin(', start);

for (const platform of ['darwin', 'win32']) {
  test(`desktop ${platform} reuses the existing installation action with an installation timeout`, async () => {
    const commands = [];
    const run = vm.runInNewContext(`(${source.slice(start, end)})`, {
      URL, process: { platform, env: {} },
      agentConnectorActionTimeoutMs: 15000, agentConnectorInstallTimeoutMs: 120000,
      runConnectorJSON: async (args, timeout) => {
        commands.push([Array.from(args), timeout]);
        return { state: 'enabled' };
      },
    });
    assert.equal((await run('deepseek-harness', 'connect')).state, 'enabled');
    assert.deepEqual(commands, [
      [['assistant', 'start', '--listen', '127.0.0.1:19091'], 15000],
      [['internal', 'agent', 'deepseek-harness', 'connect'], 120000],
    ]);
    await run('all', 'status');
    assert.deepEqual(commands[2], [['internal', 'agent', 'all', 'status'], 15000]);
  });
}
