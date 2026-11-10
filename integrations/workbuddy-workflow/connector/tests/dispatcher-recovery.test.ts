import { afterEach, expect, it, vi } from 'vitest'
import { createDispatcher } from '../../../workflow-agent-core/src/dispatcher'
import type { RuntimeAdapter } from '../../../workflow-agent-core/src/adapter'
import { BridgeError, HostBridge, isHostAuthorizationError, type HostTransport } from '../../../workflow-agent-core/src/transport'

const lifetimes: AbortController[] = []
afterEach(() => { lifetimes.splice(0).forEach(c => c.abort()); vi.useRealTimers(); vi.unstubAllGlobals() })
function fixture() {
  vi.useFakeTimers()
  const lifetime = new AbortController(); lifetimes.push(lifetime)
  const runtime: RuntimeAdapter<string> = { cancellation: 'none', id: x => x,
    resolve: vi.fn(async id => ({ agent: id })), prompt: vi.fn(async () => 0), cancel: vi.fn(), warn: vi.fn() }
  const bridge = { actions: vi.fn<HostTransport['actions']>(async () => ({ actions: [] })), action: vi.fn(), bind: vi.fn(),
    state: vi.fn(), claim: vi.fn(), settle: vi.fn() } satisfies HostTransport
  return { runtime, bridge, lifetime, poll: () => createDispatcher(runtime, undefined, bridge, 'worker', lifetime.signal).poll() }
}
it.each([401, 403])('waits for authorization recovery after HTTP %s and resumes without restarting', async status => {
  const f = fixture()
  f.bridge.actions.mockRejectedValue(new BridgeError('HOST_CREDENTIAL_MISMATCH', 'private raw error', status))
  const polling = f.poll(); await vi.advanceTimersByTimeAsync(0)
  await vi.advanceTimersByTimeAsync(29_999)
  expect(f.bridge.actions).toHaveBeenCalledTimes(1)
  expect(f.runtime.warn).toHaveBeenCalledTimes(1)
  expect(f.runtime.warn).toHaveBeenCalledWith(expect.stringContaining('authorization required'))
  expect(f.runtime.warn).not.toHaveBeenCalledWith(expect.stringContaining('private raw error'))
  f.bridge.actions.mockResolvedValue({ actions: [] })
  await vi.advanceTimersByTimeAsync(1)
  expect(f.bridge.actions).toHaveBeenCalledTimes(2)
  await vi.advanceTimersByTimeAsync(1000)
  expect(f.bridge.actions).toHaveBeenCalledTimes(3)
  f.lifetime.abort(); await polling
})
it('backs off temporary outages and resets after a successful poll', async () => {
  const f = fixture(); f.bridge.actions.mockRejectedValue(new Error('private endpoint'))
  const polling = f.poll(); await vi.advanceTimersByTimeAsync(0)
  await vi.advanceTimersByTimeAsync(1000); expect(f.bridge.actions).toHaveBeenCalledTimes(2)
  await vi.advanceTimersByTimeAsync(1000); expect(f.bridge.actions).toHaveBeenCalledTimes(2)
  await vi.advanceTimersByTimeAsync(1000); expect(f.bridge.actions).toHaveBeenCalledTimes(3)
  expect(f.runtime.warn).toHaveBeenCalledTimes(1)
  f.bridge.actions.mockResolvedValue({ actions: [] })
  await vi.advanceTimersByTimeAsync(4000); expect(f.bridge.actions).toHaveBeenCalledTimes(4)
  await vi.advanceTimersByTimeAsync(1000); expect(f.bridge.actions).toHaveBeenCalledTimes(5)
  f.lifetime.abort(); await polling
})
it.each([
  [401, 'HOST_AUTH_REQUIRED'],
  [403, 'HOST_CREDENTIAL_MISMATCH'],
] as const)('waits when claim reports connection authorization failure (%s, %s)', async (status, code) => {
  const f = fixture()
  f.bridge.actions.mockResolvedValue({ actions: [{ id: 'a', kind: 'continue', status: 'pending', session_id: 'run', native_session_id: 'native' }] })
  f.bridge.claim.mockRejectedValue(new BridgeError(code, 'denied', status))
  const polling = f.poll(); await vi.advanceTimersByTimeAsync(0)
  await vi.advanceTimersByTimeAsync(29_999)
  expect(f.bridge.claim).toHaveBeenCalledTimes(1)
  expect(f.runtime.prompt).not.toHaveBeenCalled()
  f.lifetime.abort(); await polling
})

it.each(['PERMISSION_DENIED', 'HOST_AUTH_REQUIRED'])('continues dispatch after a scoped 403 (%s)', async code => {
  const f = fixture()
  const actions = ['a', 'b'].map(id => ({ id, kind: 'continue' as const, status: 'pending', session_id: id, native_session_id: id }))
  f.bridge.actions.mockResolvedValue({ actions })
  f.bridge.claim
    .mockRejectedValueOnce(new BridgeError(code, 'action denied', 403))
    .mockResolvedValueOnce({ action: { ...actions[1], status: 'dispatching' }, dispatch_token: 'receipt' })
  const polling = f.poll(); await vi.advanceTimersByTimeAsync(0)
  expect(f.bridge.claim).toHaveBeenCalledTimes(2)
  expect(f.runtime.prompt).toHaveBeenCalledOnce()
  expect(f.runtime.prompt).toHaveBeenCalledWith('b', expect.objectContaining({ actionId: 'b' }), f.lifetime.signal)
  f.lifetime.abort(); await polling
})

it.each(['<html>private gateway body</html>', 'null', '[]'])('preserves HTTP authentication status for an invalid error body: %s', async body => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status: 401 })))
  const bridge = new HostBridge('http://127.0.0.1:19091', { connector_id: 'connector', token: 'private-token' })
  const error = await bridge.actions('', new AbortController().signal).catch(e => e)
  expect(isHostAuthorizationError(error)).toBe(true)
  expect(error.status).toBe(401)
  expect(error.message).not.toContain('private')
})
it('does not repeat authorization warnings on unsuccessful recovery probes', async () => {
  const f = fixture()
  f.bridge.actions.mockRejectedValue(new BridgeError('HOST_PAIRING_DISABLED', 'disabled', 403))
  const polling = f.poll(); await vi.advanceTimersByTimeAsync(60_000)
  expect(f.bridge.actions).toHaveBeenCalledTimes(3)
  expect(f.runtime.warn).toHaveBeenCalledTimes(1)
  f.lifetime.abort(); await polling
})
