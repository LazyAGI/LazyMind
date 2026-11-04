import { afterEach, describe, expect, it, vi } from 'vitest'
import { connectionRecovery } from '../src/connection-recovery'
import { CodexAdapter } from '../src/adapter'

afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })
describe('connection recovery UI requests', () => {
  it('reports once per outage and sends no restart command', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true }); vi.stubGlobal('fetch', fetch)
    const recovery = connectionRecovery('http://127.0.0.1:19091', { connector_id: 'host-1', token: 'secret' }, new AbortController().signal)
    await recovery.unavailable(); await recovery.unavailable()
    expect(fetch).toHaveBeenCalledOnce()
    expect(fetch.mock.calls[0][1].body).toBe('{"code":"CODEX_CONNECTION_CHECK"}')
    expect(fetch.mock.calls[0][1].headers.Authorization).toBe('Bearer secret')
    recovery.available(); await recovery.unavailable(); expect(fetch).toHaveBeenCalledTimes(2)
  })
  it('retries a failed notification transport and does nothing after shutdown', async () => {
    const fetch = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ ok: true }); vi.stubGlobal('fetch', fetch)
    const abort = new AbortController()
    const recovery = connectionRecovery('http://127.0.0.1:19091', { connector_id: 'host-1', token: 'secret' }, abort.signal)
    await recovery.unavailable(); await recovery.unavailable(); expect(fetch).toHaveBeenCalledTimes(2)
    recovery.available(); abort.abort(); await recovery.unavailable(); expect(fetch).toHaveBeenCalledTimes(2)
  })
  it('busy chats do not request recovery; disconnected pending actions do', async () => {
    const native = { read: vi.fn().mockResolvedValue({ active: true }), start: vi.fn(), interrupt: vi.fn() }
    const recovery = { available: vi.fn(), unavailable: vi.fn().mockResolvedValue(undefined) }
    const signal = new AbortController().signal
    const adapter = new CodexAdapter(native as any, {} as any, {} as any, signal, recovery)
    await expect(adapter.ready('thread', signal)).resolves.toBe(false)
    expect(recovery.unavailable).not.toHaveBeenCalled()
    native.read.mockRejectedValue(new Error('CDP disconnected'))
    await expect(adapter.ready('thread', signal)).resolves.toBe(false)
    expect(recovery.unavailable).toHaveBeenCalledOnce()
    expect(native.start).not.toHaveBeenCalled()
  })
})
