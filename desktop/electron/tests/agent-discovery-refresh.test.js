const test = require('node:test');
const assert = require('node:assert/strict');
const { createDesktopBridge } = require('../src/preload');

test('polling does not force installation discovery; explicit refresh crosses IPC as a boolean', async () => {
  const calls = [];
  const bridge = createDesktopBridge({ invoke: async (...args) => { calls.push(args); return { agents: {} }; } });
  await bridge.agentIntegrationStatuses();
  await bridge.agentIntegrationStatuses(true);
  await bridge.agentIntegrationStatuses({ refresh: true });
  assert.deepEqual(calls, [
    ['lazymind:agentIntegrationStatuses', false],
    ['lazymind:agentIntegrationStatuses', true],
    ['lazymind:agentIntegrationStatuses', false],
  ]);
});
