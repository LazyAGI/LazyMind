import { describe, it, expect, vi } from 'vitest'
import { WorkBuddyAdapter } from '../src/adapter'
import { createDispatcher } from '../../../workflow-agent-core/src/dispatcher'
import type { HostAction, HostTransport } from '../../../workflow-agent-core/src/transport'
import type { WorkflowControl } from '../../../workflow-agent-core/src/protocol'

function fixture() {
  const action: HostAction = { id: 'action-1', session_id: 'run-1', native_session_id: '8c76b002-6467-41f5-a6aa-f9e4242d8865', kind: 'continue', status: 'pending' }
  const control: WorkflowControl = { protocol: 'workflow.control.v1', session_id: action.session_id, state_version: 1, continuation: 'continue', admission: { can_begin: true } }
  let busy = false, failSend = false, sends = 0
  const adapter = new WorkBuddyAdapter({ evaluate: async <T>(expression: string) => {
    if (expression.includes('"kind":"status"')) return { state: busy ? 'working' : 'idle' } as T
    sends++
    if (failSend) throw new Error('connection lost after send')
    return { accepted: true } as T
  } })
  const bridge: HostTransport = {
    bind: vi.fn(async () => control), state: vi.fn(async () => control),
    actions: vi.fn(async () => ({ actions: [action] })), action: vi.fn(async () => ({ action: { ...action }, control })),
    claim: vi.fn(async () => {
      if (action.status !== 'pending') { action.status = 'unknown'; return { action: { ...action }, control } }
      action.status = 'dispatching'; return { action: { ...action }, control, dispatch_token: 'lease' }
    }),
    settle: vi.fn(async (_id, _instance, _token, state) => { action.status = state }),
  }
  const signal = new AbortController().signal
  const dispatcher = createDispatcher(adapter, adapter.control(signal), bridge, 'worker', signal)
  return { action, control, bridge, dispatcher, adapter, sends: () => sends, busy: () => { busy = true }, fail: () => { failSend = true } }
}
describe('independent HostAction delivery', () => {
  it('keeps busy sessions pending without claiming, reading Core state or sending', async () => {
    const f = fixture(); f.busy(); await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('pending'); expect(f.bridge.claim).not.toHaveBeenCalled(); expect(f.bridge.state).not.toHaveBeenCalled(); expect(f.sends()).toBe(0)
  })
  it('acknowledges admission with no invented event sequence', async () => {
    const f = fixture(); await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('accepted'); expect(f.sends()).toBe(1)
    expect(f.bridge.settle).toHaveBeenCalledWith('action-1', 'worker', 'lease', 'accepted', 0, '', expect.any(AbortSignal))
  })
  it('retains unknown after a timeout; restart cannot replay it', async () => {
    const f = fixture(); f.fail(); await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('unknown'); await f.dispatcher.deliver(f.action); expect(f.sends()).toBe(1)
  })
  it('does not resend after the host accepted but the Core receipt was lost', async () => {
    const f = fixture(); vi.mocked(f.bridge.settle).mockRejectedValue(new Error('receipt lost'))
    await expect(f.dispatcher.deliver(f.action)).rejects.toThrow('receipt lost')
    await f.dispatcher.deliver(f.action)
    expect(f.sends()).toBe(1); expect(f.action.status).toBe('unknown')
  })
  it('delivers Stop to a busy session without waiting for idle', async () => {
    const f = fixture(); f.busy(); f.action.kind = 'cancel'; f.control.continuation = 'stopped'
    await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('accepted'); expect(f.sends()).toBe(1)
  })
  it('rejects a stale Stop when the workflow has resumed', async () => {
    const f = fixture(); f.action.kind = 'cancel'
    await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('failed'); expect(f.sends()).toBe(0)
  })
  it('does not repeat a cancel with an uncertain host receipt', async () => {
    const f = fixture(); f.action.kind = 'cancel'; f.control.continuation = 'stopped'; f.fail()
    await f.dispatcher.deliver(f.action); await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('unknown'); expect(f.sends()).toBe(1)
  })
  it('rejects ambiguous identity evidence instead of choosing the active UI session', async () => {
    const adapter = new WorkBuddyAdapter({ evaluate: async <T>() => ({ matches: [{ sessionId: 'a' }, { sessionId: 'b' }] }) as T })
    await expect(adapter.identify({}, new AbortController().signal)).rejects.toThrow('ambiguous')
  })
  it('fails an invalid target instead of leaving it pending indefinitely', async () => {
    const f = fixture(); f.action.native_session_id = '-'.repeat(36)
    await f.dispatcher.deliver(f.action)
    expect(f.action.status).toBe('failed'); expect(f.sends()).toBe(0)
  })
})
