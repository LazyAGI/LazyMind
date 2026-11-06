import { afterEach, expect, it, vi } from 'vitest'
import { BridgeHealth } from '../src/bridge-health'
import { createDiagnostics, diagnosedTransport } from '../src/diagnostics'
import { BridgeError, type HostTransport } from '../../../workflow-agent-core/src/transport'

afterEach(() => vi.useRealTimers())

it('requires successful bridge requests, and identity success cannot hide a workflow auth failure', async () => {
  const health = new BridgeHealth()
  expect(health.issue()).toBe('lazymind_unavailable')
  await health.track('workbuddy', async () => ({}))
  const actions = vi.fn().mockRejectedValue(new BridgeError('HOST_ACCOUNT_MISMATCH', 'PRIVATE', 403))
  const transport = diagnosedTransport({ actions } as unknown as HostTransport, createDiagnostics('test', () => {}), health)
  await expect(transport.actions('', AbortSignal.timeout(1000))).rejects.toThrow('PRIVATE')
  await health.track('workbuddy', async () => ({}))
  expect(health.issue()).toBe('pairing_account_mismatch')
  actions.mockResolvedValue({ actions: [] })
  await transport.actions('', AbortSignal.timeout(1000))
  expect(health.issue()).toBe('')
})

it('does not clear a new failure when an older in-flight request completes', async () => {
  const health = new BridgeHealth()
  let resolve!: () => void
  const older = health.track('workflow', () => new Promise<void>(done => { resolve = done }))
  await expect(health.track('workflow', async () => { throw new BridgeError('AUTH', 'secret', 401) })).rejects.toThrow()
  resolve(); await older
  expect(health.issue()).toBe('lazymind_auth_required')
})

it('gates identity/presentation retries for authorization and resets after a real success', async () => {
  vi.useFakeTimers()
  const health = new BridgeHealth()
  await expect(health.track('workbuddy', async () => { throw new BridgeError('HOST_PAIRING_DISABLED', 'secret', 403) })).rejects.toThrow()
  const request = vi.fn().mockResolvedValue({})
  const pending = health.wait('workbuddy', new AbortController().signal).then(() => health.track('workbuddy', request))
  await vi.advanceTimersByTimeAsync(29_999)
  expect(request).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(1); await pending
  expect(request).toHaveBeenCalledOnce()
  expect(health.retryAfter('workbuddy')).toBe(0)
})

it('backs off temporary failures and keeps the bridge healthy on action-level errors', async () => {
  vi.useFakeTimers()
  const health = new BridgeHealth()
  for (const expected of [1000, 2000, 4000]) {
    await expect(health.track('workflow', async () => { throw new TypeError('private URL') })).rejects.toThrow()
    expect(health.retryAfter('workflow')).toBe(expected)
    await vi.advanceTimersByTimeAsync(expected)
  }
  await health.track('workflow', async () => ({}))
  await health.track('workbuddy', async () => ({}))
  for (const error of [
    new BridgeError('HOST_ACTION_PENDING', 'private', 409),
    new BridgeError('PERMISSION_DENIED', 'private', 403),
    new BridgeError('HOST_AUTH_REQUIRED', 'private', 403),
  ]) {
    await expect(health.track('workflow', async () => { throw error })).rejects.toThrow()
    expect(health.issue()).toBe('')
    expect(health.retryAfter('workflow')).toBe(0)
  }
})
