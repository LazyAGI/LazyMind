import type { Context } from '@deepseek-ai/cordis'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { HostBridge } from '../src/bridge'
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

  it('sends fresh MCP evidence with each authenticated action poll', async () => {
    const f = fixture()
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ actions: [] }), { status: 200 }))
    // Use a fresh response body for every call.
    fetch.mockImplementation(async () => new Response(JSON.stringify({ actions: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    const bridge = new HostBridge('http://127.0.0.1:18086', { connector_id: 'profile-desktop', token: 'token' }, async signal => ({
      'X-LazyMind-MCP-Ready': await f.ready(signal) ? '1' : '0',
    }))
    const signal = new AbortController().signal
    await bridge.actions('', signal)
    expect(fetch.mock.calls[0][1].headers).toMatchObject({ 'X-LazyMind-MCP-Ready': '1',
      'X-LazyMind-Connector-Id': 'profile-desktop', Authorization: 'Bearer token' })
    f.request.mockRejectedValue(new Error('MCP connection closed'))
    await bridge.actions('', signal)
    expect(fetch.mock.calls[1][1].headers['X-LazyMind-MCP-Ready']).toBe('0')
    // A missing MCP connection does not prevent control actions from being polled.
    expect(fetch).toHaveBeenCalledTimes(2)
  })
})
