import { expect, it, vi } from 'vitest'
import { createDiagnostics, diagnosedTransport } from '../src/diagnostics'
import { WorkBuddyAdapter } from '../src/adapter'
import type { HostTransport } from '../../../workflow-agent-core/src/transport'

it('logs correlation fields without credentials, bodies or error text', () => {
  const sink = vi.fn(), log = createDiagnostics('host-test', sink)
  log('action.claim', { action_id: 'action-1', run_id: 'run-1', native_session_id: 'session-1', duration_ms: 1.7,
    dispatch_token: 'SECRET', execution_handle: 'SECRET', message: 'PRIVATE', error: 'PRIVATE' } as any)
  const line = sink.mock.calls[0][0]
  expect(line).not.toMatch(/SECRET|PRIVATE|dispatch_token|execution_handle/)
  expect(JSON.parse(line)).toMatchObject({ event: 'action.claim', action_id: 'action-1', run_id: 'run-1', native_session_id: 'session-1', duration_ms: 2 })
})

it('preserves receipt failures and does not log an unconfirmed settle as accepted', async () => {
  const sink = vi.fn(), failure = new Error('PRIVATE token=SECRET')
  const settle = vi.fn().mockRejectedValue(failure)
  const transport = diagnosedTransport({ settle } as unknown as HostTransport, createDiagnostics('host-test', sink))
  const args = ['action-1', 'worker', 'SECRET', 'accepted', 0, 'PRIVATE', AbortSignal.timeout(1000)] as const
  await expect(transport.settle(...args)).rejects.toBe(failure)
  expect(settle).toHaveBeenCalledOnce()
  expect(settle).toHaveBeenCalledWith(...args)
  expect(JSON.parse(sink.mock.calls[0][0])).toMatchObject({ event: 'action.settle', status: 'unconfirmed' })
  expect(sink.mock.calls[0][0]).not.toMatch(/SECRET|PRIVATE/)
})

it('a failed log sink cannot turn an admitted send into an unknown outcome', async () => {
  const adapter = new WorkBuddyAdapter({ evaluate: async <T>() => ({ accepted: true }) as T }, createDiagnostics('host-test', () => { throw Error('disk full') }))
  await expect(adapter.prompt('session-1', { actionId: 'action-1', message: 'PRIVATE' }, AbortSignal.timeout(1000))).resolves.toBe(0)
})

it('does not pass raw shared-dispatcher errors into WorkBuddy logs', () => {
  const sink = vi.fn()
  const adapter = new WorkBuddyAdapter({ evaluate: vi.fn() }, createDiagnostics('host-test', sink))
  adapter.warn('lazymind-workflow: delivery pending: PRIVATE server response')
  expect(JSON.parse(sink.mock.calls[0][0])).toMatchObject({ event: 'connector.warning', kind: 'delivery-pending' })
  expect(sink.mock.calls[0][0]).not.toContain('PRIVATE')
})

it.each(['failed', 'unknown'])('keeps native %s distinct without logging the error body', async status => {
  const sink = vi.fn(), log = createDiagnostics('host-test', sink)
  const adapter = new WorkBuddyAdapter({ evaluate: async <T>() => {
    if (status === 'failed') return { rejected: 'PRIVATE rejection' } as T
    throw Error('PRIVATE connection error')
  } }, log)
  await expect(adapter.prompt('session-1', { actionId: 'action-1', message: 'PRIVATE' }, AbortSignal.timeout(1000))).rejects.toThrow()
  expect(JSON.parse(sink.mock.calls[0][0])).toMatchObject({ event: 'native.send', action_id: 'action-1', native_session_id: 'session-1', status })
  expect(sink.mock.calls[0][0]).not.toContain('PRIVATE')
})
