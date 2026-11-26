import type { Context } from '@deepseek-ai/cordis'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { HostBridge, reportRuntime } from '../src/bridge'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import type { RuntimeAdapter } from '../../workflow-agent-core/src/adapter'
import { mcpReadiness } from '../src/mcp-readiness'

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

function fixture(present = true) {
  const names = new Set(['start', 'state', 'step_begin'].map(operation => `mcp__lazymind__workflow_${operation}_012345abcdef`))
  const request = vi.fn().mockResolvedValue({ resources: [] })
  let dispose = () => {}
  const ctx = {
    tools: { schemas: () => [...names].map(name => ({ name })), get: (name: string) => names.has(name) ? {} : undefined },
    inject: (_names: string[], callback: (value: unknown) => void) => {
      if (present) callback({ mcpResources: { request }, effect: (install: () => () => void) => { dispose = install() } })
    },
  } as unknown as Context
  return { request, names, ready: mcpReadiness(ctx, 'lazymind'), dispose: () => dispose() }
}

describe('DSH live MCP readiness', () => {
  it('requires both registered LazyMind tools and a current native MCP connection', async () => {
    const f = fixture()
    expect(await f.ready(new AbortController().signal)).toBe(true)
    expect(f.request).toHaveBeenCalledWith('lazymind', { method: 'resources/list' }, { agent: undefined, signal: expect.any(AbortSignal) })
    // DSH retains registered schemas after its MCP client disconnects.
    f.request.mockRejectedValue(new Error('MCP server is not connected'))
    expect(await f.ready(new AbortController().signal)).toBe(false)
    expect(f.names.size).toBe(3)
    f.request.mockResolvedValue({ resources: [] })
    expect(await f.ready(new AbortController().signal)).toBe(true)
    f.names.clear()
    expect(await f.ready(new AbortController().signal)).toBe(false)
  })
  it('does not claim readiness without the optional native connection service', async () => {
    const f = fixture(false)
    expect(await f.ready(new AbortController().signal)).toBe(false)
    expect(f.request).not.toHaveBeenCalled()
  })

  it('rejects a late result after the native service is disposed', async () => {
    const f = fixture()
    let finish!: (value: unknown) => void
    f.request.mockReturnValue(new Promise(resolve => { finish = resolve }))
    const result = f.ready(new AbortController().signal)
    f.dispose()
    finish({ resources: [] })
    expect(await result).toBe(false)
  })

  it('bounds a native provider that ignores cancellation so control polling can continue', async () => {
    vi.useFakeTimers()
    const f = fixture()
    f.request.mockReturnValue(new Promise(() => {}))
    const result = f.ready(new AbortController().signal)
    await vi.advanceTimersByTimeAsync(2000)
    expect(await result).toBe(false)
    expect(f.request.mock.calls[0][2].signal.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

})

it('continues local heartbeats throughout the shared dispatcher authorization backoff', async () => {
  vi.useFakeTimers()
  const lifetime = new AbortController()
  const mcp = fixture()
  let actionCalls = 0
  const heartbeats: Array<{ time: number; headers: Record<string, string> }> = []
  const fetch = vi.fn(async (url: string, request: { headers: Record<string, string> }) => {
    if (url.endsWith('/heartbeat')) {
      heartbeats.push({ time: Date.now(), headers: request.headers })
      return Response.json({ ok: true })
    }
    if (++actionCalls === 1) return Response.json({ code: 'HOST_AUTH_REQUIRED' }, { status: 401 })
    return Response.json({ actions: [] })
  })
  vi.stubGlobal('fetch', fetch)
  const identity = { 'X-LazyMind-DSH-Dispatcher': 'dispatcher', 'X-LazyMind-DSH-Process': 'process' }
  const headers = async (signal: AbortSignal) => ({
    ...identity,
    'X-LazyMind-MCP-Ready': await mcp.ready(signal) ? '1' : '0',
    'X-LazyMind-DSH-Build': '0.1.6+0000000000000001',
    'X-LazyMind-DSH-Client-Build': '0.1.6+0000000000000002',
    'X-LazyMind-DSH-Panel': 'bound',
  })
  const bridge = new HostBridge('http://127.0.0.1:19091', { connector_id: 'profile-desktop', token: 'token' }, async () => identity)
  const native = { cancellation: 'none', warn: vi.fn() } as unknown as RuntimeAdapter<string>
  const dispatcher = createDispatcher(native, undefined, bridge, 'dispatcher', lifetime.signal)
  const reporting = reportRuntime(bridge, headers, lifetime.signal)
  const polling = dispatcher.poll()
  try {
    await vi.advanceTimersByTimeAsync(0)
    const authObservation = dispatcher.connection
    expect(authObservation?.issue).toBe('lazymind_auth_required')
    mcp.request.mockRejectedValue(new Error('MCP connection closed'))
    await vi.advanceTimersByTimeAsync(18_000)
    expect(actionCalls).toBe(1)
    expect(dispatcher.connection).toBe(authObservation)
    expect(heartbeats.at(-1)!.time - authObservation!.checked_at).toBeGreaterThan(12_000)
    expect(heartbeats.at(-1)!.headers).toMatchObject({
      ...identity, Authorization: 'Bearer token', 'X-LazyMind-MCP-Ready': '0',
      'X-LazyMind-DSH-Build': '0.1.6+0000000000000001',
      'X-LazyMind-DSH-Client-Build': '0.1.6+0000000000000002', 'X-LazyMind-DSH-Panel': 'bound',
    })
    await vi.advanceTimersByTimeAsync(12_000)
    expect(actionCalls).toBe(2)
    expect(dispatcher.connection).toEqual({ checked_at: Date.now(), issue: '' })
    expect(fetch.mock.calls.filter(([url]) => url.endsWith('/actions')).at(-1)?.[1].headers).not.toHaveProperty('X-LazyMind-MCP-Ready')
  } finally {
    lifetime.abort()
    await Promise.all([reporting, polling])
  }
  const stopped = heartbeats.length
  await vi.advanceTimersByTimeAsync(12_000)
  expect(heartbeats).toHaveLength(stopped)
})
