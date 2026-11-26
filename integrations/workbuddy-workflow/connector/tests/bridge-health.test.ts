import { afterEach, expect, it, vi } from 'vitest'
import { WorkBuddyAdapter } from '../src/adapter'
import { workBuddyConnection } from '../src/connection'
import { presentationLoop, type PresentationStatus } from '../src/presentation'
import type { Cdp } from '../src/cdp'

vi.mock('node:timers/promises', () => ({
  setTimeout: (ms: number, _value: undefined, { signal }: { signal: AbortSignal }) => new Promise<void>((resolve, reject) => {
    function stop() { clearTimeout(timer); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', stop); resolve() }, ms)
    signal.addEventListener('abort', stop, { once: true })
  }),
}))

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

const unavailable = () => new Response(null, { status: 502 })
const pendingIdentity = { id: 'identity-B', arguments: { workflow_id: 'workflow-B' } }
const identity = { sessionId: 'B', requestId: 'request-B', toolCallId: 'call-B' }

function fixture(chatId = 'A') {
  vi.useFakeTimers()
  const lifetime = new AbortController()
  const log = vi.fn()
  const reports: PresentationStatus[] = []
  let view = { session_id: chatId, epoch: 'first' }
  let changed: (() => void) | undefined
  const evaluate = vi.fn(async (expression: string, signal: AbortSignal) => {
    if (expression.includes('function readPresentationSession')) return view
    if (expression.includes('?.wait(')) return new Promise(resolve => {
      const stop = () => resolve(undefined)
      signal.addEventListener('abort', stop, { once: true })
      changed = () => { signal.removeEventListener('abort', stop); stop() }
    })
    if (expression.includes('function mountPresentation')) return { status: expression.includes('"run_id":""') ? 'unbound' : 'mounted' }
  })
  const native = vi.fn(async (_expression: string) => ({ matches: [identity] }))
  const adapter = new WorkBuddyAdapter({ evaluate: async <T>(expression: string) => await native(expression) as T }, log)
  const bridgeURL = 'http://127.0.0.1:19091'
  const connection = workBuddyConnection(bridgeURL, { connector_id: 'connector', token: 'token' }, adapter, log, lifetime.signal)
  const panel = vi.fn<(chat: string, signal?: AbortSignal | null) => Response | Promise<Response>>()
  const requests = vi.fn<() => Response>(() => Response.json({ requests: [] }))
  const receipts = vi.fn<(_receipt: unknown) => Response>(() => Response.json({ accepted: true }))
  vi.stubGlobal('fetch', vi.fn(async (input: URL, init?: RequestInit) => {
    switch (input.pathname) {
      case '/v1/workbuddy/presentation': return panel(input.searchParams.get('session_id')!, init?.signal)
      case '/v1/workbuddy/identity-requests': return requests()
      case '/v1/workbuddy/identity-receipts': return receipts(JSON.parse(String(init?.body)))
      default: throw new Error('unexpected route')
    }
  }))
  const loops: Promise<void>[] = []
  return { log, reports, evaluate, native, panel, requests, receipts,
    move: (session_id: string, epoch: string) => { view = { session_id, epoch }; changed?.() },
    start: () => loops.push(connection.identities(),
      presentationLoop({ evaluate } as unknown as Cdp, connection.lookupBinding, bridgeURL, lifetime.signal, report => reports.push(report))),
    stop: async () => { lifetime.abort(); await Promise.all(loops) },
  }
}

it('keeps native B identity responsive through A panel failures and preserves A’s retry and warning', async () => {
  const f = fixture()
  f.panel.mockImplementation(unavailable)
  try {
    f.start()
    await vi.advanceTimersByTimeAsync(23_000)
    expect(f.panel).toHaveBeenCalledTimes(5)
    expect(f.reports.at(-1)).toMatchObject({ native_session_id: 'A', status: 'binding-unavailable' })
    f.requests.mockReturnValueOnce(Response.json({ requests: [pendingIdentity] }))
    await vi.advanceTimersByTimeAsync(250)
    expect(f.native).toHaveBeenCalledOnce()
    expect(f.receipts).toHaveBeenCalledWith({ id: pendingIdentity.id, ...identity })
    expect(f.reports.at(-1)).toMatchObject({ native_session_id: 'A', status: 'binding-unavailable' })
    await vi.advanceTimersByTimeAsync(15_749)
    expect(f.panel).toHaveBeenCalledTimes(5)
    f.panel.mockReturnValueOnce(Response.json({ session_id: 'A', run_id: 'run-A' }))
    await vi.advanceTimersByTimeAsync(1)
    expect(f.panel).toHaveBeenCalledTimes(6)
    expect(f.reports.at(-1)).toMatchObject({ native_session_id: 'A', status: 'mounted', run_id: 'run-A' })
  } finally { await f.stop() }
})

it('keeps A panel responsive through five native identity failures without clearing identity backoff', async () => {
  const f = fixture('initial')
  f.requests.mockImplementation(unavailable)
  f.panel.mockImplementation(chat => Response.json({ session_id: chat, run_id: '' }))
  try {
    f.start()
    await vi.advanceTimersByTimeAsync(15_000)
    expect(f.requests).toHaveBeenCalledTimes(5)
    expect(f.log.mock.calls.filter(([event]) => event === 'connector.warning')).toEqual([
      ['connector.warning', { kind: 'identity-bridge', status: 'lazymind_unavailable' }],
    ])
    f.move('A', 'second')
    f.panel.mockReturnValueOnce(Response.json({ session_id: 'A', run_id: 'run-A' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(f.reports.at(-1)).toMatchObject({ native_session_id: 'A', status: 'mounted', run_id: 'run-A' })
    await vi.advanceTimersByTimeAsync(15_999)
    expect(f.requests).toHaveBeenCalledTimes(5)
    expect(f.log).not.toHaveBeenCalledWith('connector.warning', { kind: 'identity-bridge', status: 'connected' })
    f.requests.mockReturnValueOnce(Response.json({ requests: [pendingIdentity] }))
    await vi.advanceTimersByTimeAsync(1)
    expect(f.receipts).toHaveBeenCalledOnce()
    expect(f.log).toHaveBeenCalledWith('connector.warning', { kind: 'identity-bridge', status: 'connected' })
  } finally { await f.stop() }
})

it.each(['success', 'failure'] as const)('fences a late %s and an active cancellation during A → B → A', async late => {
  const f = fixture()
  let finishA!: (response: Response) => void
  let oldA: AbortSignal | null | undefined
  let oldB: AbortSignal | null | undefined
  f.panel
    .mockImplementationOnce((_chat, signal) => { oldA = signal; return new Promise(resolve => { finishA = resolve }) })
    .mockImplementationOnce((_chat, signal) => {
      oldB = signal
      return new Promise((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal.reason), { once: true }))
    })
    .mockImplementation(unavailable)
  try {
    f.start()
    await vi.advanceTimersByTimeAsync(0)
    f.move('B', 'second')
    await vi.advanceTimersByTimeAsync(0)
    expect(oldA?.aborted).toBe(true)
    f.move('A', 'third')
    await vi.advanceTimersByTimeAsync(0)
    expect(oldB?.aborted).toBe(true)
    finishA(late === 'success' ? Response.json({ session_id: 'A', run_id: 'obsolete' }) : unavailable())
    await vi.advanceTimersByTimeAsync(0)
    expect(f.reports.map(({ native_session_id, status }) => [native_session_id, status])).toEqual([
      ['B', 'unmounted'], ['A', 'unmounted'], ['A', 'binding-unavailable'],
    ])
    expect(f.evaluate.mock.calls.some(([expression]) => expression.includes('obsolete'))).toBe(false)
    await vi.advanceTimersByTimeAsync(22_999)
    expect(f.panel).toHaveBeenCalledTimes(6)
    await vi.advanceTimersByTimeAsync(1)
    expect(f.panel).toHaveBeenCalledTimes(7)
    await vi.advanceTimersByTimeAsync(15_999)
    expect(f.panel).toHaveBeenCalledTimes(7)
    f.panel.mockReturnValueOnce(Response.json({ session_id: 'A', run_id: 'current' }))
    await vi.advanceTimersByTimeAsync(1)
    expect(f.reports.at(-1)).toMatchObject({ native_session_id: 'A', run_id: 'current' })
  } finally { await f.stop() }
})
