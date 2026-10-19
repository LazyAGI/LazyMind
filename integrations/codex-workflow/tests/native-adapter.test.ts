import { EventEmitter } from 'node:events'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import type { HostAction, HostTransport } from '../../workflow-agent-core/src/transport'
import type { WorkflowControl } from '../../workflow-agent-core/src/protocol'
import { AppServer, RpcError, RpcUnavailable, type NativeTurn } from '../src/app-server'
import { NativeCodexAdapter, NativeReceipts, workflowTurnOwner } from '../src/native-adapter'

const thread = '01a12f01-e372-7ef3-aaae-4a0a295d6700'
const directories: string[] = []
afterEach(async () => { for (const dir of directories.splice(0)) await rm(dir, { recursive: true, force: true }) })
function acquire(run = 'run-1', tool = 'workflow.step.begin') {
  return { type: 'mcpToolCall', server: 'lazymind-workflow', tool, status: 'completed', error: null,
    arguments: { session_id: run }, result: { structuredContent: { state: { session_id: run }, execution: { execution_id: 'exec-1' } } } }
}
const user = (clientId = 'ordinary-user') => ({ type: 'userMessage', clientId, content: [{ type: 'text', text: 'user input' }] })
async function fixture(kind: HostAction['kind'] = 'cancel') {
  const dir = await mkdtemp(join(tmpdir(), 'lm-native-test-')); directories.push(dir)
  const receipts = new NativeReceipts(dir)
  const signal = new AbortController().signal
  let turns: NativeTurn[] = [{ id: 'owned-turn', status: 'inProgress', items: [user(), acquire()] }]
  let action: HostAction = { id: 'action-1', session_id: 'run-1', native_session_id: thread, kind, status: 'pending' }
  let control: WorkflowControl = { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: kind === 'cancel' ? 'stopped' : 'continue', admission: { can_begin: kind === 'continue' },
    binding: { bound: true, provider: 'codex', driver_session_id: thread, connector_id: 'connector-1' } }
  const rpc = Object.assign(new EventEmitter(), {
    readThread: vi.fn(async () => ({ id: thread, status: { type: 'active' }, turns: structuredClone(turns) })),
    startTurn: vi.fn(async (_thread: string, text: string, clientId: string) => {
      const turn: NativeTurn = { id: 'native-turn', status: 'inProgress', items: [{ type: 'userMessage', clientId, content: [{ type: 'text', text }] }] }
      turns.push(turn); return { turn }
    }),
    interruptTurn: vi.fn(async (_thread: string, id: string) => {
      const target = turns.find(t => t.id === id)
      if (!target || target.status !== 'inProgress') throw new RpcError(-32602, 'stale turn')
      target.status = 'interrupted'; return {}
    }),
  })
  const bridge: HostTransport = {
    bind: vi.fn(async () => control), state: vi.fn(async () => control), actions: vi.fn(async () => ({ actions: [action] })),
    action: vi.fn(async () => ({ action: { ...action }, control })),
    claim: vi.fn(async () => {
      if (action.status !== 'pending') { if (action.status === 'dispatching') action.status = 'unknown'; return { action: { ...action }, control } }
      action.status = 'dispatching'; return { action: { ...action }, control, dispatch_token: 'token' }
    }),
    settle: vi.fn(async (_a, _i, _t, status) => { action.status = status }),
  }
  const adapter = new NativeCodexAdapter(rpc as unknown as AppServer, bridge, receipts, signal, 'connector-1')
  const dispatcher = () => createDispatcher(adapter, adapter.host, bridge, 'instance', signal)
  return { rpc, bridge, adapter, dispatcher, receipts, action: () => ({ ...action }), control: () => control,
    updateAction: (value: Partial<HostAction>) => { action = { ...action, ...value } },
    turns: (value: NativeTurn[]) => { turns = value }, update: (value: Partial<WorkflowControl>) => { control = { ...control, ...value } }, signal }
}

describe('native Workflow Stop and Continue', () => {
  it('interrupts and confirms the exact Workflow-owned turn through claim/settle', async () => {
    const f = await fixture(); const pending = f.action()
    await f.dispatcher().deliver(pending); await f.dispatcher().deliver(pending)
    expect(f.rpc.interruptTurn).toHaveBeenCalledTimes(1)
    expect(f.rpc.interruptTurn).toHaveBeenCalledWith(thread, 'owned-turn', f.signal)
    expect(f.action().status).toBe('accepted')
    expect(await f.receipts.read('action-1')).toMatchObject({ turnId: 'owned-turn', status: 'accepted' })
  })
  it('does not interrupt ordinary input, reads, failed calls or a later mid-turn steer', async () => {
    for (const items of [[user()], [user(), { ...acquire(), tool: 'workflow.state' }],
      [user(), { ...acquire(), error: { message: 'rejected' } }], [user(), acquire(), user('later-input')]]) {
      const f = await fixture(); f.turns([{ id: 'user-turn', status: 'inProgress', items }])
      await f.dispatcher().deliver(f.action())
      expect(f.rpc.interruptTurn).not.toHaveBeenCalled(); expect(f.action().status).toBe('failed')
    }
  })
  it('rejects another Workflow or a consumed/superseded cancellation', async () => {
    const other = await fixture(); other.turns([{ id: 'other-turn', status: 'inProgress', items: [user(), acquire('run-2')] }])
    await other.dispatcher().deliver(other.action()); expect(other.rpc.interruptTurn).not.toHaveBeenCalled()
    const resumed = await fixture(); resumed.update({ continuation: 'continue' })
    await resumed.dispatcher().deliver(resumed.action()); expect(resumed.rpc.interruptTurn).not.toHaveBeenCalled()
  })
  it('never interrupts a replacement turn after a native target race', async () => {
    const f = await fixture()
    f.rpc.interruptTurn.mockImplementation(async (_thread, id) => {
      expect(id).toBe('owned-turn'); f.turns([{ id: 'new-user-turn', status: 'inProgress', items: [user()] }])
      throw new RpcError(-32602, 'stale turn')
    })
    await f.dispatcher().deliver(f.action())
    expect(f.action().status).toBe('unknown'); expect(f.rpc.interruptTurn).toHaveBeenCalledOnce()
    await f.dispatcher().deliver(f.action()); expect(f.rpc.interruptTurn).toHaveBeenCalledOnce()
  })
  it('acknowledges an already idle thread without a native interruption', async () => {
    const f = await fixture(); f.turns([{ id: 'done', status: 'completed', items: [user(), acquire()] }])
    await f.dispatcher().deliver(f.action()); expect(f.action().status).toBe('accepted'); expect(f.rpc.interruptTurn).not.toHaveBeenCalled()
  })
  it('starts one native turn, records real IDs and does not invent a native event sequence', async () => {
    const f = await fixture('continue'); f.turns([]); const pending = f.action()
    await f.dispatcher().deliver(pending); await f.dispatcher().deliver(pending)
    expect(f.rpc.startTurn).toHaveBeenCalledOnce()
    const receipt = await f.receipts.read('action-1'); expect(receipt).toMatchObject({ runId: 'run-1', threadId: thread, turnId: 'native-turn', status: 'accepted' })
    expect(f.bridge.settle).toHaveBeenCalledWith('action-1', 'instance', 'token', 'accepted', 0, '', f.signal)
  })
  it('does not send input when the thread is busy or the notification is superseded', async () => {
    const f = await fixture('continue'); await f.dispatcher().deliver(f.action()); expect(f.rpc.startTurn).not.toHaveBeenCalled()
    const stale = await fixture('continue'); stale.turns([]); stale.update({ continuation: 'awaiting_user' })
    await stale.dispatcher().deliver(stale.action()); expect(stale.rpc.startTurn).not.toHaveBeenCalled()
  })
  it('can stop its admitted native turn before the first MCP acquisition', async () => {
    const f = await fixture('continue'); f.turns([])
    await f.dispatcher().deliver(f.action())
    f.updateAction({ id: 'cancel-1', kind: 'cancel', status: 'pending' }); f.update({ continuation: 'stopped' })
    await f.dispatcher().deliver(f.action())
    expect(f.rpc.interruptTurn).toHaveBeenCalledWith(thread, 'native-turn', f.signal)
    expect(f.action().status).toBe('accepted')
  })
  it('a user copying the action marker cannot acquire native cancellation ownership', async () => {
    const f = await fixture('continue'); f.turns([]); await f.dispatcher().deliver(f.action())
    f.turns([{ id: 'copied-user-turn', status: 'inProgress', items: [
      { type: 'userMessage', clientId: 'new-user-id', content: [{ type: 'text', text: '[LazyMind action_id=action-1]\nuser discussion' }] },
    ] }])
    f.updateAction({ id: 'cancel-1', kind: 'cancel', status: 'pending' }); f.update({ continuation: 'stopped' })
    await f.dispatcher().deliver(f.action()); expect(f.rpc.interruptTurn).not.toHaveBeenCalled()
  })
  it('never replays an uncertain native start, including after process reconstruction', async () => {
    const f = await fixture('continue'); f.turns([])
    f.rpc.startTurn.mockRejectedValue(new RpcUnavailable('lost response', true))
    await f.dispatcher().deliver(f.action()); expect(f.action().status).toBe('unknown')
    expect(await f.receipts.read('action-1')).toMatchObject({ status: 'unknown' })
    await f.dispatcher().deliver(f.action()); expect(f.rpc.startTurn).toHaveBeenCalledOnce()
  })
  it('keeps a successful start unknown when subsequent ownership inspection fails', async () => {
    const f = await fixture('continue'); f.turns([])
    const original = f.rpc.startTurn.getMockImplementation()!
    f.rpc.startTurn.mockImplementation(async (...args) => {
      const response = await original(...args)
      f.rpc.readThread.mockRejectedValue(new RpcError(-32602, 'thread temporarily unavailable'))
      return response
    })
    await f.dispatcher().deliver(f.action())
    expect(f.action().status).toBe('unknown')
    expect(await f.receipts.read('action-1')).toMatchObject({ status: 'unknown' })
    expect(f.rpc.startTurn).toHaveBeenCalledOnce()
  })
  it('rechecks a changed Core decision immediately before sending native input', async () => {
    const f = await fixture('continue'); f.turns([])
    f.bridge.action = vi.fn().mockResolvedValueOnce({ action: { ...f.action(), status: 'dispatching' }, control: f.control() })
      .mockResolvedValue({ action: { ...f.action(), status: 'dispatching' }, control: { ...f.control(), continuation: 'stopped' } })
    await f.dispatcher().deliver(f.action())
    expect(f.rpc.startTurn).not.toHaveBeenCalled()
    expect(f.action().status).toBe('failed')
  })
  it('does not claim a different binding or a turn that start joined instead of creating', async () => {
    const f = await fixture('continue'); f.turns([]); f.update({ binding: { bound: true, driver_session_id: 'other', provider: 'codex', connector_id: 'connector-1' } })
    await f.dispatcher().deliver(f.action()); expect(f.rpc.startTurn).not.toHaveBeenCalled()
    const joined = await fixture('continue'); joined.turns([{ id: 'old-turn', status: 'completed' }])
    joined.rpc.startTurn.mockResolvedValue({ turn: { id: 'old-turn', status: 'inProgress' } })
    await joined.dispatcher().deliver(joined.action()); expect(joined.action().status).toBe('unknown')
  })
  it('recognizes the successful native MCP receipt but not an inconsistent execution receipt', () => {
    expect(workflowTurnOwner({ id: 'turn', status: 'inProgress', items: [user(), acquire()] })).toBe('run-1')
    expect(workflowTurnOwner({ id: 'turn', status: 'inProgress', items: [{ ...acquire(), arguments: { session_id: 'other' } }] })).toBeUndefined()
  })
})
