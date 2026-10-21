import { describe, expect, it, vi } from 'vitest'
import { CodexAdapter, type QueueRunner } from '../src/adapter'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import { BridgeError, type HostAction, type HostTransport } from '../../workflow-agent-core/src/transport'
import type { WorkflowControl } from '../../workflow-agent-core/src/protocol'

const thread = '01a0cd90-7aaa-7960-86ab-6ee368c3d327'
function fixture(run: QueueRunner = vi.fn(async () => {})) {
  const signal = new AbortController().signal
  const adapter = new CodexAdapter('/desktop/codex', '/profile', run)
  const cancel = vi.spyOn(adapter, 'cancel')
  let action: HostAction = { id: 'action-1', session_id: 'run-1', native_session_id: thread,
    kind: 'continue', status: 'pending' }
  let control: WorkflowControl = { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: 'continue', admission: { can_begin: true },
    binding: { driver_session_id: thread, bound: true } }
  const bridge: HostTransport = {
    bind: vi.fn(async () => control), state: vi.fn(async () => control),
    actions: vi.fn(async () => ({ actions: [action] })),
    action: vi.fn(async () => ({ action: { ...action }, control })),
    claim: vi.fn(async () => {
      if (action.status === 'pending') {
        action = { ...action, status: 'dispatching' }
        return { action: { ...action }, dispatch_token: 'token', control }
      }
      if (action.status === 'dispatching') action.status = 'unknown'
      return { action: { ...action }, control }
    }),
    settle: vi.fn(async (_id, _instance, _token, status) => { action.status = status }),
  }
  const dispatcher = () => createDispatcher(adapter, undefined, bridge, 'instance', signal)
  return { adapter, run, cancel, bridge, signal, dispatcher,
    action: () => ({ ...action }),
    update: (fields: Partial<HostAction>) => Object.assign(action, fields),
    setControl: (fields: Partial<WorkflowControl>) => { control = { ...control, ...fields } } }
}

describe('Codex queue delivery', () => {
  it('queues once to an exact UUID with the notification and runtime instructions', async () => {
    const run = vi.fn(async () => {})
    const f = fixture(run)
    const pending = f.action()
    await f.dispatcher().deliver(pending)
    await f.dispatcher().deliver(pending)
    expect(run).toHaveBeenCalledTimes(1)
    const [binary, args, signal, home] = run.mock.calls[0] as unknown as Parameters<QueueRunner>
    expect(binary).toBe('/desktop/codex'); expect(home).toBe('/profile'); expect(signal).toBe(f.signal)
    expect(args.slice(0, 4)).toEqual(['queue', '--thread', thread, '--message'])
    expect(args[4]).toContain('action_id=action-1')
    expect(args[4]).toContain('workflow.step.begin')
    expect(args[4]).toContain('END this turn')
    expect(args[4]).toContain('current Core state and authorization override')
    expect(f.cancel).not.toHaveBeenCalled()
    expect(f.bridge.settle).toHaveBeenCalledWith('action-1', 'instance', 'token', 'accepted', 0, '', f.signal)
  })
  it('rejects unsupported cancellation without claiming that the turn stopped', async () => {
    const f = fixture(); f.update({ kind: 'cancel' }); f.setControl({ continuation: 'stopped' })
    await f.dispatcher().deliver(f.action())
    expect(f.action().status).toBe('failed'); expect(f.cancel).not.toHaveBeenCalled(); expect(f.run).not.toHaveBeenCalled()
  })
  it('does not redeliver after an uncertain queue result or dispatcher restart', async () => {
    const run = vi.fn(async () => { throw new Error('timeout') })
    const f = fixture(run)
    await f.dispatcher().deliver(f.action())
    expect(f.action().status).toBe('unknown')
    await f.dispatcher().deliver(f.action())
    expect(run).toHaveBeenCalledTimes(1)
  })
  it('does not resend after a successful queue whose Core receipt was lost', async () => {
    const f = fixture()
    vi.mocked(f.bridge.settle).mockRejectedValue(new Error('disconnected'))
    await expect(f.dispatcher().deliver(f.action())).rejects.toThrow('disconnected')
    expect(f.action().status).toBe('dispatching')
    await f.dispatcher().deliver(f.action())
    expect(f.run).toHaveBeenCalledTimes(1)
    expect(f.action().status).toBe('unknown')
  })
  it('records definite launch failure as failed but nonzero exit as unknown', async () => {
    for (const [code, status] of [['ENOENT', 'failed'], [1, 'unknown']] as const) {
      const f = fixture(vi.fn(async () => { throw Object.assign(new Error('exit'), { code }) }))
      await f.dispatcher().deliver(f.action())
      expect(f.action().status).toBe(status)
    }
  })
  it('rejects a task title instead of queuing to an ambiguous target', async () => {
    const f = fixture()
    f.update({ native_session_id: 'task title' })
    await f.dispatcher().deliver(f.action())
    expect(f.action().status).toBe('failed')
    expect(f.run).not.toHaveBeenCalled()
  })
  it('does not queue when Core rejects a claim', async () => {
    const f = fixture()
    vi.mocked(f.bridge.claim).mockRejectedValue(new BridgeError('DELIVERY_PENDING', 'another dispatcher owns the notification', 409))
    await expect(f.dispatcher().deliver(f.action())).rejects.toThrow('another dispatcher owns the notification')
    expect(f.run).not.toHaveBeenCalled()
  })
})
