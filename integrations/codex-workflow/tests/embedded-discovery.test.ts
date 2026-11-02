import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import { expect, it } from 'vitest'

it('the shipped renderer probe does not evaluate a self-expanding signal family', async () => {
  // Exercise the asset embedded in the Go executable, not just the TS source:
  // a stale dispatcher can reintroduce the freeze even when source tests pass.
  const bundle = readFileSync(new URL('../../../local/lazymind-cli/internal/codexplugin/assets/dispatcher.mjs', import.meta.url), 'utf8')
  const start = bundle.indexOf('async function codexOperation(')
  const end = bundle.indexOf('\nfunction nativeExpression(', start)
  expect(start).toBeGreaterThanOrEqual(0)
  expect(end).toBeGreaterThan(start)

  // Keep all application objects in the same VM realm for instanceof Map.
  // The VM deadline also makes a regression fail instead of hanging Vitest.
  const result = await runInNewContext(`
    let reads = 0;
    const family = new Map();
    const cell = {
      atom: {}, store: {},
      get() { reads++; family.set(reads, { value: cell }); return {}; },
    };
    family.set('local', { value: cell });
    const client = { node: { familyBindings: new Map([[{ kind: 'readable-family' }, family]]) } };
    globalThis.__codexRoot = { _internalRoot: { current: { memoizedState: { memoizedState: { current: client } } } } };
    ${bundle.slice(start, end)}
    codexOperation({ kind: 'probe' }, () => undefined)
      .then(status => ({ status, reads, cells: family.size }));
  `, { performance, Node: class {}, document: { querySelectorAll: () => [] } }, { timeout: 250 })

  expect(result).toEqual({ status: { ready: false }, reads: 0, cells: 1 })
})
