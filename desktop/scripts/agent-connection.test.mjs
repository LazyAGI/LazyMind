import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../electron/src/main.js', import.meta.url), 'utf8');
const start = source.indexOf('function runAgentConnector(');
const end = source.indexOf('\nfunction startAgentLogin(', start);
// Execute the production timeout declarations with the extracted function so
// the harness cannot hide a changed budget behind its own stale constants.
const timeoutDeclarations = [
  'agentConnectorActionTimeoutMs',
  'agentConnectorStatusTimeoutMs',
  'agentConnectorDisconnectTimeoutMs',
  'agentConnectorInstallTimeoutMs',
].map((name) => {
  const declaration = source.match(new RegExp(`^const ${name} = .+;$`, 'm'));
  assert.ok(declaration, `missing Desktop timeout declaration: ${name}`);
  return declaration[0];
}).join('\n');

for (const platform of ['darwin', 'win32']) {
  test(`desktop ${platform} uses production budgets for installation, status and disconnection`, async () => {
    const commands = [];
    const run = vm.runInNewContext(`${timeoutDeclarations}\n(${source.slice(start, end)})`, {
      URL, process: { platform, env: {} },
      runConnectorJSON: async (args, timeout) => {
        commands.push([Array.from(args), timeout]);
        return { state: 'enabled' };
      },
    });
    assert.equal((await run('deepseek-harness', 'connect')).state, 'enabled');
    await run('all', 'status');
    await run('deepseek-harness', 'status');
    await run('deepseek-harness', 'disconnect');
    await run('cursor', 'connect');
    await run('cursor', 'disconnect');
    assert.deepEqual(commands, [
      [['assistant', 'start', '--listen', '127.0.0.1:19091'], 15000],
      [['internal', 'agent', 'deepseek-harness', 'connect'], 660000],
      [['internal', 'agent', 'all', 'status'], 30000],
      [['internal', 'agent', 'deepseek-harness', 'status'], 30000],
      [['internal', 'agent', 'deepseek-harness', 'disconnect'], 60000],
      [['internal', 'agent', 'cursor', 'connect'], 15000],
      [['internal', 'agent', 'cursor', 'disconnect'], 15000],
    ]);
  });
}
