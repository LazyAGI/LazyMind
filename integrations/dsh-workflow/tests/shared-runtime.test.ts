import { describe, expect, it, vi } from 'vitest'
import type { HostEvent, RuntimeAdapter, ToolCall } from '../../workflow-agent-core/src/adapter'
import { createCoordinator } from '../../workflow-agent-core/src/coordinator'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import { BridgeError, type HostAction, type HostTransport } from '../../workflow-agent-core/src/transport'
import type { WorkflowControl } from '../../workflow-agent-core/src/protocol'

// Deliberately contains no DSH SDK, event names, goal format or tool-name encoding.
interface Agent { id: string; parent?: Agent; events: HostEvent[] }
function fixture() {
  const root: Agent = { id: 'native-1', events: [] }
  const other: Agent = { id: 'native-2', events: [] }
  const child: Agent = { id: 'worker-1', parent: root, events: [] }
  const agents = [root, other, child]
  const lifetime = new AbortController()
  let control: WorkflowControl = { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: 'continue', admission: { can_begin: true }, active_execution_ids: [],
    binding: { driver_session_id: root.id, generation: 1, bound: true } }
  let action: HostAction = { id: 'action-1', session_id: 'run-1', native_session_id: root.id,
    kind: 'continue', binding_generation: 1, status: 'pending' }
  const receipts = new Map<string, number>()
  const runtime: RuntimeAdapter<Agent> = {
    id: a => a.id, parent: a => a.parent, isLive: a => agents.includes(a), isRunning: () => true,
    history: a => a.events, canReturnResult: a => !!a.parent,
    resolve: vi.fn(async id => {
      const agent = agents.find(a => a.id === id)
      return agent ? { agent } : { error: 'missing session' }
    }),
    prompt: vi.fn(async (agent, input) => { receipts.set(`${agent.id}:${input.actionId}`, 12); return 12 }),
    cancel: vi.fn(), eventSeq: () => 12,
    reconcile: vi.fn(async (id, actionId) => receipts.get(`${id}:${actionId}`) ?? 0), warn: vi.fn(),
  }
  const bridge: HostTransport = {
    bind: vi.fn(async () => control), state: vi.fn(async () => control),
    actions: vi.fn(async () => ({ actions: ['pending', 'dispatching', 'unknown'].includes(action.status) ? [{ ...action }] : [] })),
    action: vi.fn(async id => {
      if (id !== action.id) throw new BridgeError('NOT_FOUND', 'not a workflow notification', 404)
      return { action: { ...action }, control }
    }),
    claim: vi.fn(async () => {
      if (action.status === 'pending') {
        action = { ...action, status: 'dispatching' }
        return { action: { ...action }, dispatch_token: 'token', control }
      }
      if (action.status === 'dispatching') action = { ...action, status: 'unknown' }
      return { action: { ...action }, control }
    }),
    settle: vi.fn(async (_id, _instance, _token, status) => { action = { ...action, status } }),
  }
  const coordinator = createCoordinator(runtime, bridge, 'http://localhost:8090', lifetime.signal)
  const dispatcher = createDispatcher(runtime, coordinator, bridge, 'instance-1', lifetime.signal)
  const call = (operation: string | null, agent = root, args: unknown = {}): ToolCall<Agent> => ({
    operation, agent, arguments: args, callId: 'call-1', signal: lifetime.signal,
  })
  const start = async () => coordinator.afterResult({ structuredContent: { session_id: 'run-1',
    interaction_url: 'http://localhost:8090/workflow-runs/run-1', control } }, call('start'))
  return { root, other, child, runtime, bridge, coordinator, dispatcher, lifetime, call, start, receipts,
    action: () => ({ ...action }), control: () => control,
    updateAction: (fields: Partial<HostAction>) => { action = { ...action, ...fields } },
    updateControl: (fields: Partial<WorkflowControl>) => { control = { ...control, ...fields, state_version: control.state_version + 1 } },
  }
}

describe('shared delivery contract with an SDK-free host', () => {
  it('admits the exact action to the target session and does not replay stale pending listings', async () => {
    const f = fixture()
    const pending = f.action()
    await f.dispatcher.deliver(pending)
    await f.dispatcher.deliver(pending)
    expect(f.runtime.prompt).toHaveBeenCalledOnce()
    expect(f.runtime.prompt).toHaveBeenCalledWith(f.root, expect.objectContaining({ actionId: pending.id }), f.lifetime.signal)
    expect(f.bridge.settle).toHaveBeenCalledWith(pending.id, 'instance-1', 'token', 'accepted', 12, '', f.lifetime.signal)
    // Queue admission does not make a different current user turn Workflow-owned.
    expect(f.coordinator.ensure(f.root).activeOwned).toBe(false)
  })

  it('reconciles admission after a lost response and dispatcher restart without another prompt', async () => {
    const f = fixture()
    vi.mocked(f.runtime.prompt).mockImplementationOnce(async (agent, input) => {
      f.receipts.set(`${agent.id}:${input.actionId}`, 18)
      throw new Error('response lost after admission')
    })
    await f.dispatcher.deliver(f.action())
    expect(f.action().status).toBe('unknown')
    const restored = createCoordinator(f.runtime, f.bridge, 'http://localhost:8090', f.lifetime.signal)
    const restarted = createDispatcher(f.runtime, restored, f.bridge, 'instance-2', f.lifetime.signal)
    await restarted.deliver(f.action())
    expect(f.action().status).toBe('accepted')
    expect(f.runtime.prompt).toHaveBeenCalledOnce()
    expect(f.bridge.settle).toHaveBeenLastCalledWith('action-1', 'instance-2', '', 'accepted', 18, '', f.lifetime.signal)
  })

  it('does not retry unknown delivery when history contains no evidence', async () => {
    const f = fixture()
    f.updateAction({ status: 'unknown' })
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.prompt).not.toHaveBeenCalled()
    expect(f.bridge.settle).not.toHaveBeenCalled()
    expect(f.action().status).toBe('unknown')
  })

  it('reads Core before checking host evidence for an uncertain continuation', async () => {
    const f = fixture()
    f.updateAction({ status: 'unknown' })
    await f.dispatcher.deliver(f.action())
    expect(vi.mocked(f.bridge.action).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(f.runtime.reconcile!).mock.invocationCallOrder[0])
  })

  it('does not reconcile a continuation already accepted by Core', async () => {
    const f = fixture()
    f.updateAction({ status: 'unknown' })
    vi.mocked(f.bridge.action).mockImplementationOnce(async () => ({ action: { ...f.action(), status: 'accepted' }, control: f.control() }))
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.reconcile).not.toHaveBeenCalled()
    expect(f.bridge.claim).not.toHaveBeenCalled()
  })

  it('does not resend after a dispatch lease expires without receipt evidence', async () => {
    const f = fixture()
    f.updateAction({ status: 'dispatching' })
    await f.dispatcher.deliver(f.action())
    expect(f.action().status).toBe('unknown')
    expect(f.runtime.prompt).not.toHaveBeenCalled()
  })

  it.each(['consumed', 'binding', 'superseded'] as const)('revalidates %s immediately before admission', async kind => {
    const f = fixture()
    vi.mocked(f.bridge.action).mockImplementationOnce(async () => ({
      action: { ...f.action(), ...(kind === 'consumed' ? { consumed_at: 'now' } : kind === 'superseded' ? { status: 'superseded' } : {}) },
      control: kind === 'binding' ? { ...f.control(), binding: { generation: 2, bound: true } } : f.control(),
    }))
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.prompt).not.toHaveBeenCalled()
    expect(f.runtime.cancel).not.toHaveBeenCalled()
  })

  it.each([
    { continuation: 'awaiting_user', canBegin: false },
    { continuation: 'continue', canBegin: false },
  ])('does not admit an ordinary continuation after control changes: %j', async ({ continuation, canBegin }) => {
    const f = fixture()
    vi.mocked(f.bridge.action).mockImplementationOnce(async () => ({ action: f.action(), control: {
      ...f.control(), continuation, admission: { can_begin: canBegin },
    } }))
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.prompt).not.toHaveBeenCalled()
    expect(f.action().status).toBe('failed')
  })

  it('does not admit an execution update after the workflow stops', async () => {
    const f = fixture()
    f.updateAction({ execution_id: 'retry-execution' })
    vi.mocked(f.bridge.action).mockImplementationOnce(async () => ({ action: f.action(), control: {
      ...f.control(), continuation: 'stopped', admission: { can_begin: false },
    } }))
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.prompt).not.toHaveBeenCalled()
    expect(f.action().status).toBe('failed')
  })

  it('keeps an unknown continuation unresolved without a reconciliation capability', async () => {
    const f = fixture()
    f.updateAction({ status: 'unknown' })
    delete f.runtime.reconcile
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.prompt).not.toHaveBeenCalled()
    expect(f.action().status).toBe('unknown')
  })

  it('uses claim for an existing execution instead of starting a replacement', async () => {
    const f = fixture()
    f.updateAction({ execution_id: 'retry-execution' })
    await f.dispatcher.deliver(f.action())
    const message = vi.mocked(f.runtime.prompt).mock.calls[0][1].message
    expect(message).toContain('workflow.state')
    expect(message).toContain('workflow.step.claim with execution_id=retry-execution')
    expect(message).not.toContain('workflow.step.begin')
    expect(f.runtime.cancel).not.toHaveBeenCalled()
  })

  it('records a definitely missing host as failed before any send', async () => {
    const f = fixture()
    f.updateAction({ native_session_id: 'missing' })
    await f.dispatcher.deliver(f.action())
    expect(f.action().status).toBe('failed')
    expect(f.runtime.prompt).not.toHaveBeenCalled()
  })

  it('does not cancel unrelated user work when a delayed stop arrives', async () => {
    const f = fixture()
    await f.start()
    await f.coordinator.beforeTurn({ agent: f.root, turn: 2, messages: [{ user: true, requestId: 'manual' }], signal: f.lifetime.signal })
    f.updateAction({ kind: 'cancel' })
    f.updateControl({ continuation: 'stopped', admission: { can_begin: false } })
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.cancel).not.toHaveBeenCalled()
    expect(f.action().status).toBe('accepted')
  })

  it('cancels its own stopped Workflow, but not a cancel superseded by Resume', async () => {
    const f = fixture()
    await f.start()
    f.updateAction({ kind: 'cancel' })
    f.updateControl({ continuation: 'stopped', admission: { can_begin: false } })
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.cancel).toHaveBeenCalledWith(f.root)
    vi.mocked(f.runtime.cancel).mockClear()
    f.updateAction({ status: 'unknown' })
    f.updateControl({ continuation: 'continue', admission: { can_begin: true } })
    await f.dispatcher.deliver(f.action())
    expect(f.runtime.cancel).not.toHaveBeenCalled()
  })

  it('waits for asynchronous cancellation before admitting a replacement continuation', async () => {
    const f = fixture()
    await f.start()
    const events: string[] = []
    vi.mocked(f.runtime.cancel).mockImplementation(async () => {
      await Promise.resolve()
      events.push('cancelled')
    })
    vi.mocked(f.runtime.prompt).mockImplementation(async () => { events.push('admitted'); return 12 })
    await f.dispatcher.deliver(f.action())
    expect(events).toEqual(['cancelled', 'admitted'])
  })

  it('rejects consumed or stale queued inputs at the actual turn boundary', async () => {
    const f = fixture()
    f.updateAction({ consumed_at: 'now' })
    const payload = { agent: f.root, turn: 1, messages: [{ user: true, requestId: 'action-1' }], signal: f.lifetime.signal }
    expect(await f.coordinator.beforeTurn(payload)).toBe(false)
    f.updateAction({ consumed_at: undefined, binding_generation: 0 })
    expect(await f.coordinator.beforeTurn({ ...payload, turn: 2 })).toBe(false)
  })

  it('stops an idle polling loop on disposal', async () => {
    const f = fixture()
    vi.mocked(f.bridge.actions).mockImplementationOnce(async () => { f.lifetime.abort(); return { actions: [] } })
    await f.dispatcher.poll()
    expect(f.bridge.actions).toHaveBeenCalledOnce()
    expect(f.runtime.prompt).not.toHaveBeenCalled()
  })
})

describe('shared coordination contract with an SDK-free host', () => {
  it('yields at review without blocking another host session', async () => {
    const f = fixture()
    await f.start()
    f.updateControl({ continuation: 'awaiting_user', admission: { can_begin: false } })
    const control = await f.coordinator.afterResult({ structuredContent: { execution_id: 'done', control: f.control() } }, f.call('step_complete'))
    expect(f.coordinator.shouldConclude(f.root, control)).toBe(true)
    expect(await f.coordinator.beforeTool(f.call(null))).toContain('waiting for user review')
    expect(await f.coordinator.beforeTool(f.call(null, f.other))).toBeUndefined()
  })

  it('allows only effective executions to drain and preserves child result return', async () => {
    const f = fixture()
    await f.start()
    f.updateControl({ continuation: 'draining', admission: { can_begin: false }, active_execution_ids: ['exec-1'] })
    await f.coordinator.afterResult({ structuredContent: { execution: { execution_id: 'exec-1', executor_host: 'external-agent' }, control: f.control() } }, f.call('step_begin', f.child))
    expect(await f.coordinator.beforeTool(f.call('artifact_publish', f.child, { execution_id: 'exec-1' }))).toBeUndefined()
    expect(await f.coordinator.beforeTool(f.call('artifact_publish', f.child, { execution_id: 'wrong' }))).toContain('already granted')
    f.updateControl({ continuation: 'awaiting_user', active_execution_ids: [] })
    const result = await f.coordinator.afterResult({ structuredContent: { execution_id: 'exec-1', control: f.control() } }, f.call('step_complete', f.child))
    expect(f.coordinator.shouldConclude(f.child, result)).toBe(false)
    expect(await f.coordinator.beforeTool({ ...f.call(null, f.child), returnsResult: true })).toBeUndefined()
    expect(await f.coordinator.beforeTool(f.call('step_begin', f.root))).toBeDefined()
  })

  it('never grants an internal execution to the external worker', async () => {
    const f = fixture()
    await f.start()
    f.updateControl({ continuation: 'awaiting_executor', admission: { can_begin: false }, active_execution_ids: ['native-1'], native_execution_ids: ['native-1'] })
    const result = await f.coordinator.afterResult({ structuredContent: { execution: { execution_id: 'native-1', executor_host: 'lazymind' }, control: f.control() } }, f.call('step_begin'))
    expect(f.coordinator.shouldConclude(f.root, result)).toBe(true)
    expect(f.coordinator.ensure(f.root).grants.size).toBe(0)
    expect(await f.coordinator.beforeTool(f.call(null))).toBeDefined()
  })

  it('repairs binding from a persisted start receipt, but not a historical state read', async () => {
    const f = fixture()
    f.root.events.push({ seq: 1, time: 1, user: false, run: { runId: 'run-1', hostSessionId: f.root.id, operation: 'start', url: 'http://localhost:8090/workflow-runs/run-1' } })
    f.updateControl({ continuation: 'binding_required', binding: { bound: false, generation: 0 }, admission: { can_begin: false } })
    const restored = createCoordinator(f.runtime, f.bridge, 'http://localhost:8090', f.lifetime.signal)
    await restored.beforeTurn({ agent: f.root, turn: 1, messages: [], signal: f.lifetime.signal })
    expect(f.bridge.bind).toHaveBeenCalledWith('run-1', f.root.id, expect.any(AbortSignal))
    vi.mocked(f.bridge.bind).mockClear()
    await restored.afterResult({ structuredContent: { session_id: 'other-run', interaction_url: 'http://localhost:8090/workflow-runs/other-run' } }, f.call('state', f.other))
    expect(f.bridge.bind).not.toHaveBeenCalled()
    expect(await restored.beforeTool(f.call(null, f.other))).toBeUndefined()
  })

  it('preserves committed results while blocking automatic work on binding failure', async () => {
    const f = fixture()
    vi.mocked(f.bridge.bind).mockRejectedValueOnce(new Error('offline'))
    expect(await f.start()).toBeNull()
    expect(f.coordinator.shouldConclude(f.root, null)).toBe(true)
    expect(f.coordinator.denial(f.root, f.call(null))).toContain('unavailable')
  })
})
