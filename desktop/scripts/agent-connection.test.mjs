import assert from 'node:assert/strict';
import test from 'node:test';
import { createAgentConnector } from '../electron/src/agent-connector.js';

for (const platform of ['darwin', 'win32']) {
  test(`desktop ${platform} uses production budgets for installation, status and disconnection`, async () => {
    const commands = [];
    const run = createAgentConnector({
      withAgentLoginStatus: (_agent, result) => result,
      agentRecovery: { start() {} },
      runConnectorJSON: async (args, timeout) => {
        commands.push([Array.from(args), timeout]);
        return { state: 'enabled' };
      },
    });
    assert.equal((await run('deepseek-harness', 'connect')).state, 'enabled');
    await run('workbuddy', 'connect');
    await run('codex', 'connect');
    await run('all', 'status');
    await run('deepseek-harness', 'status');
    await run('deepseek-harness', 'disconnect');
    await run('cursor', 'connect');
    await run('cursor', 'disconnect');
    assert.deepEqual(commands, [
      [['assistant', 'start'], 15000],
      [['internal', 'agent', 'deepseek-harness', 'connect'], 660000],
      [['assistant', 'start'], 15000],
      [['internal', 'agent', 'workbuddy', 'connect'], 120000],
      [['assistant', 'start'], 15000],
      [['internal', 'agent', 'codex', 'connect'], 120000],
      [['internal', 'agent', 'all', 'status'], 30000],
      [['internal', 'agent', 'deepseek-harness', 'status'], 30000],
      [['internal', 'agent', 'deepseek-harness', 'disconnect'], 60000],
      [['internal', 'agent', 'cursor', 'connect'], 15000],
      [['internal', 'agent', 'cursor', 'disconnect'], 15000],
    ]);
  });
}

for (const platform of ['darwin', 'win32']) {
  test(`desktop ${platform} cancels recovery before disconnecting Codex`, async () => {
    const operations = [];
    let finishStop;
    const run = createAgentConnector({
      withAgentLoginStatus: (_agent, result) => result,
      agentRecovery: {
        start: () => operations.push('listen'),
        stop: () => new Promise(resolve => { operations.push('stop'); finishStop = resolve; }),
      },
      runConnectorJSON: async args => { operations.push(Array.from(args).join(' ')); return { state: 'enabled' }; },
    });
    await run('codex', 'connect');
    assert.deepEqual(operations, ['assistant start', 'internal agent codex connect', 'listen']);
    operations.length = 0;
    const pending = run('codex', 'disconnect');
    assert.deepEqual(operations, ['stop']);
    finishStop(); await pending;
    assert.deepEqual(operations, ['stop', 'internal agent codex disconnect']);
  });
}
