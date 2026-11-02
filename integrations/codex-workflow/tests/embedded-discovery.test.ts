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

it('the shipped panel discovers indirectly loaded browser modules', async () => {
  const bundle = readFileSync(new URL('../../../local/lazymind-cli/internal/codexplugin/assets/dispatcher.mjs', import.meta.url), 'utf8')
  const start = bundle.indexOf('async function prepareCodexBrowser(')
  const end = bundle.indexOf('\nfunction createCodexSurface(', start)
  expect(start).toBeGreaterThanOrEqual(0)
  expect(end).toBeGreaterThan(start)
  const host: any = {}
  const fetched: string[] = [], imported: string[] = []
  const manager = {pages:{getWebview(){},disposeHost(){}},getSnapshot(){}}
  await runInNewContext(`${bundle.slice(start,end)};prepareCodexBrowser(load)`, {
    window:host, document:{querySelectorAll:()=>[{src:'app://-/assets/index-current.js'}]},
    URL, AbortSignal, TextDecoder,
    fetch: async (url:string) => {
      fetched.push(url)
      return new Response(url.includes('/index-') ? 'import("./app-main-current.js")' : 'import{a}from"./app-initial-current.js"')
    },
    load:async (url:string) => {imported.push(url);return {minified:manager}},
  }, {timeout:250})
  expect(fetched).toEqual(['app://-/assets/index-current.js','app://-/assets/app-main-current.js'])
  expect(imported).toEqual(['app://-/assets/app-initial-current.js'])
  expect(host.__lazymindCodexBrowser).toBe(manager)
})

it('the shipped RPC reports host ownership and refuses an unfenced interruption', async () => {
  const bundle = readFileSync(new URL('../../../local/lazymind-cli/internal/codexplugin/assets/dispatcher.mjs', import.meta.url), 'utf8')
  const start = bundle.indexOf('async function codexOperation(')
  const end = bundle.indexOf('\nfunction nativeExpression(', start)
  expect(start).toBeGreaterThanOrEqual(0)
  expect(end).toBeGreaterThan(start)
  const result = await runInNewContext(`
    let interrupts = 0;
    const rpc = Object.assign(() => {}, {
      getHostId: async () => 'local',
      getConversation: async () => ({id:'thread', resumeState:'resumed', turns:[]}),
      getStreamRole: async () => ({role:'owner'}),
      interruptConversation: async () => { interrupts++; return 'turn'; },
    });
    globalThis.__lazymindCodexManager = {
      __lazymindRpc: rpc, getHostId: () => 'local',
      getConversation: rpc.getConversation, getStreamRole: rpc.getStreamRole,
      startTurn() {}, interruptConversation: rpc.interruptConversation,
    };
    ${bundle.slice(start, end)}
    (async () => ({
      read: await codexOperation({kind:'read', threadId:'thread'}, () => undefined),
      stop: await codexOperation({kind:'interrupt', threadId:'thread', turnId:'turn'}, () => undefined),
      interrupts,
    }))();
  `, {}, {timeout:250})
  expect(result.read).toMatchObject({owner:true, ownerScope:'host', ready:true})
  expect(result.stop.rejected).toContain('Stop the current response in Codex')
  expect(result.interrupts).toBe(0)
})
