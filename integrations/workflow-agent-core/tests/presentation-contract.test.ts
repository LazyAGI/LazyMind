import { afterEach, expect, it, vi } from 'vitest'
import type { BindingResult, LookupBinding, PanelView, UIAdapter, WatchView } from '../src/adapter'
import { runPresentation, type PresentationStatus } from '../src/presentation'
import { HostBridge } from '../src/transport'

const frameUrl = 'http://127.0.0.1:19091/v1/codex/frame'

function start(lookupBinding: LookupBinding, ui: UIAdapter) {
  const lifetime = new AbortController(), states: PresentationStatus[] = []
  let reportView!: (view: PanelView) => void
  const watchView: WatchView = (report, signal) => {
    reportView = report
    return new Promise(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
  }
  const running = runPresentation({ watchView, lookupBinding, ui, frameUrl, signal: lifetime.signal, report: state => states.push(state) })
  return { states, view: reportView, stop: async () => { lifetime.abort(); await running } }
}

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

it.each(['C', 'A'])('clears the old view and fences late lookups on A → B → %s', async last => {
  vi.useFakeTimers()
  const pending: Array<{ chatId: string; signal: AbortSignal; finish: (result: BindingResult) => void }> = []
  const ops: string[] = []
  const lookup: LookupBinding = (chatId, signal) => new Promise(resolve => {
    ops.push(`lookup:${chatId}`)
    pending.push({ chatId, signal, finish: resolve })
  })
  const syncPanel = vi.fn<UIAdapter['syncPanel']>(async (view, target) => {
    ops.push(`${target ? 'mount' : 'unmount'}:${view.epoch}`)
    return { status: target ? 'mounted' : 'unmounted' }
  })
  const loop = start(lookup, { syncPanel })
  try {
    loop.view({ chatId: 'A', epoch: 'first' })
    loop.view({ chatId: 'B', epoch: 'second' })
    await vi.advanceTimersByTimeAsync(0)
    expect(ops).toEqual(['lookup:A', 'unmount:second', 'lookup:B'])
    expect(pending[0].signal.aborted).toBe(true)
    loop.view({ chatId: last, epoch: 'third' })
    loop.view({ chatId: last, epoch: 'third' })
    await vi.advanceTimersByTimeAsync(0)
    expect(pending.map(item => item.chatId)).toEqual(['A', 'B', last])
    expect(pending[1].signal.aborted).toBe(true)
    pending[2].finish({ status: 'bound', runId: 'latest' })
    await vi.advanceTimersByTimeAsync(0)
    pending[0].finish({ status: 'bound', runId: 'obsolete' })
    pending[1].finish({ status: 'access-denied' })
    await vi.advanceTimersByTimeAsync(0)
    expect(ops).toEqual(['lookup:A', 'unmount:second', 'lookup:B', 'unmount:third', `lookup:${last}`, 'mount:third'])
    expect(syncPanel).toHaveBeenLastCalledWith({ chatId: last, epoch: 'third' }, { runId: 'latest', frameUrl }, expect.anything())
    expect(loop.states.at(-1)).toMatchObject({ status: 'mounted', run_id: 'latest', native_session_id: last })
  } finally { await loop.stop() }
})

it('refreshes a stationary chat, retries a layout, and revokes only on confirmed absence or denial', async () => {
  vi.useFakeTimers()
  const binding = vi.fn<LookupBinding>()
    .mockResolvedValueOnce({ status: 'bound', runId: 'run-1' })
    .mockResolvedValueOnce({ status: 'bound', runId: 'run-1' })
    .mockResolvedValueOnce({ status: 'unavailable' })
    .mockResolvedValueOnce({ status: 'bound', runId: 'run-2' })
    .mockResolvedValueOnce({ status: 'access-denied' })
    .mockResolvedValueOnce({ status: 'unbound' })
  const syncPanel = vi.fn<UIAdapter['syncPanel']>()
    .mockResolvedValueOnce({ status: 'mounted', pageStatus: 'loading' })
    .mockResolvedValueOnce({ status: 'reused', pageStatus: 'ready' })
    .mockResolvedValueOnce({ status: 'layout-unavailable' })
    .mockResolvedValueOnce({ status: 'mounted' })
    .mockResolvedValue({ status: 'unmounted' })
  const loop = start(binding, { syncPanel })
  try {
    loop.view({ chatId: 'A', epoch: 'one' })
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(syncPanel.mock.calls.map(([, target]) => target?.runId)).toEqual(['run-1', 'run-1'])
    expect(loop.states.map(state => state.status)).toEqual(['mounted', 'mounted', 'binding-unavailable'])
    expect(loop.states[1].page_status).toBe('ready')
    await vi.advanceTimersByTimeAsync(5100)
    expect(binding).toHaveBeenCalledTimes(4)
    expect(syncPanel.mock.calls.map(([, target]) => target?.runId)).toEqual(['run-1', 'run-1', 'run-2', 'run-2'])
    expect(loop.states.slice(-2).map(state => state.status)).toEqual(['layout-unavailable', 'mounted'])
    await vi.advanceTimersByTimeAsync(9900)
    expect(syncPanel.mock.calls.slice(-2).map(([, target]) => target)).toEqual([null, null])
    expect(loop.states.slice(-2).map(state => state.status)).toEqual(['access-denied', 'unbound'])
  } finally { await loop.stop() }
})

it.each(['codex', 'workbuddy'] as const)('looks up %s presentations through authenticated, typed Bridge results', async provider => {
  const response = vi.fn<typeof fetch>()
    .mockResolvedValueOnce(Response.json({ session_id: 'A', run_id: 'run-1' }))
    .mockResolvedValueOnce(Response.json({ session_id: 'A', run_id: '' }))
    .mockResolvedValueOnce(new Response('Unauthorized', { status: 401 }))
    .mockResolvedValueOnce(new Response('Forbidden', { status: 403 }))
    .mockResolvedValueOnce(Response.json({ error: 'unavailable' }, { status: 503 }))
    .mockResolvedValueOnce(Response.json({ session_id: 'B', run_id: 'wrong' }))
  vi.stubGlobal('fetch', response)
  const lookup = new HostBridge('http://127.0.0.1:19091', { connector_id: 'connector', token: 'token' }).lookupBinding(provider)
  const signal = new AbortController().signal
  await expect(lookup('A', signal)).resolves.toEqual({ status: 'bound', runId: 'run-1' })
  await expect(lookup('A', signal)).resolves.toEqual({ status: 'unbound' })
  await expect(lookup('A', signal)).resolves.toMatchObject({ status: 'access-denied' })
  await expect(lookup('A', signal)).resolves.toMatchObject({ status: 'access-denied' })
  await expect(lookup('A', signal)).resolves.toMatchObject({ status: 'unavailable' })
  await expect(lookup('A', signal)).resolves.toMatchObject({ status: 'unavailable' })
  expect(response.mock.calls[0][0]).toEqual(new URL(`http://127.0.0.1:19091/v1/${provider}/presentation?session_id=A`))
  expect(response.mock.calls[0][1]).toMatchObject({ redirect: 'error', headers: { Authorization: 'Bearer token', 'X-LazyMind-Connector-Id': 'connector' } })
})

it('mounts the panel on refresh when an open chat gets its first run binding', async () => {
  vi.useFakeTimers()
  const binding = vi.fn<LookupBinding>()
    .mockResolvedValueOnce({ status: 'unbound' })
    .mockResolvedValueOnce({ status: 'bound', runId: 'run-1' })
  const syncPanel = vi.fn<UIAdapter['syncPanel']>(async (_view, target) => ({
    status: target ? 'mounted' : 'unmounted',
  }))
  const loop = start(binding, { syncPanel })
  const view = { chatId: 'A', epoch: 'one' }
  try {
    loop.view(view)
    await vi.advanceTimersByTimeAsync(0)
    expect(binding.mock.calls.map(([chatId]) => chatId)).toEqual(['A'])
    expect(syncPanel.mock.calls.map(([, target]) => target?.runId ?? null)).toEqual([null])

    await vi.advanceTimersByTimeAsync(5_000)
    expect(binding.mock.calls.map(([chatId]) => chatId)).toEqual(['A', 'A'])
    expect(syncPanel.mock.calls.map(([, target]) => target?.runId ?? null)).toEqual([null, 'run-1'])
    expect(syncPanel).toHaveBeenLastCalledWith(view, { runId: 'run-1', frameUrl }, expect.anything())
  } finally { await loop.stop() }
})
