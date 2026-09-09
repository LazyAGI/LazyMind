import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../electron/src/main.js', import.meta.url), 'utf8');
const start = source.indexOf('async function runAgentConnector(');
const end = source.indexOf('\nfunction startAgentLogin(', start);

for (const platform of ['darwin', 'win32']) {
  test(`desktop ${platform} installation uses the same asynchronous Bridge as the web UI`, async () => {
    const commands = [], requests = [];
    const run = vm.runInNewContext(`(${source.slice(start, end)})`, {
      URL, AbortSignal, process: { platform, env: {} }, agentConnectorActionTimeoutMs: 15000,
      runConnectorJSON: async (args) => { commands.push(Array.from(args)); return { ok: true }; },
      fetch: async (url, options) => {
        requests.push([url.href, options.method]);
        return { ok: true, json: async () => ({ state: 'connecting' }) };
      },
    });
    assert.equal((await run('deepseek-harness', 'connect')).state, 'connecting');
    assert.deepEqual(commands, [['assistant', 'start', '--listen', '127.0.0.1:19091']]);
    assert.deepEqual(requests, [['http://127.0.0.1:19091/v1/agents/deepseek-harness/connect', 'POST']]);
    await run('all', 'status');
    assert.deepEqual(requests[1], ['http://127.0.0.1:19091/v1/agents', 'GET']);
  });
}
