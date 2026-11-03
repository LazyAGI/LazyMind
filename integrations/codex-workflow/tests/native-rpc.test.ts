import { afterEach, expect, it, vi } from 'vitest'
import { runInNewContext } from 'node:vm'
import { codexOperation, CodexNative } from '../src/native'
import { workflowTurnEvidence } from '../src/ownership'

const mocked = vi.hoisted(() => ({ targets: vi.fn(), cdp: vi.fn() }))
vi.mock('../src/windows', () => ({ codexTargets: mocked.targets, windowCdp: mocked.cdp, selectCodexTarget: vi.fn() }))

afterEach(() => { vi.unstubAllGlobals(); vi.resetAllMocks() })

function rendererManager(rpc: any) {
  return {
    __lazymindRpc: rpc,
    getHostId: () => 'local',
    getConversation: (id: string) => rpc.getConversation(id),
    getStreamRole: (id: string) => rpc.getStreamRole(id),
    startTurn: (...args: any[]) => rpc.startTurn(...args),
    interruptConversation: (...args: any[]) => rpc.interruptConversation(...args),
  }
}

it('can read a shared main-process manager through two desktop windows', async () => {
  // Both renderer clients subscribe to the same main-process host registry.
  const rpc = Object.assign(() => {}, {
    getHostId: async () => 'local',
    getConversation: async () => ({ id: 'thread', resumeState: 'resumed', turns: [] }),
    getStreamRole: async () => ({ role: 'owner' }),
    startTurn: vi.fn().mockResolvedValue({ turnId: 'next-turn' }),
  })
  mocked.targets.mockResolvedValue([{ id: 'main' }, { id: 'detached' }])
  mocked.cdp.mockImplementation(() => ({
    evaluate: async (expression: string) => runInNewContext(expression, {
      __lazymindCodexManager: rendererManager(rpc),
    }),
  }))
  await expect(new CodexNative(9334).read('thread', new AbortController().signal)).resolves.toMatchObject({ id: 'thread', ready: true })
  await expect(new CodexNative(9334).start('thread', 'client', 'continue', new AbortController().signal)).resolves.toEqual({ accepted: true, turnId: 'next-turn' })
  expect(rpc.startTurn).toHaveBeenCalledOnce()
})

it.each([false, true])('rejects RPC interruption without an atomic ownership guard (steering=%s)', async (steering) => {
  const liveTurn = { id: 'turn', status: 'inProgress', items: [
    { type: 'mcpToolCall', server: 'lazymind', tool: 'workflow.start', status: 'completed', result: { structuredContent: { session_id: 'run' } } },
  ] as any[] }
  const expectedOwnership = JSON.stringify(workflowTurnEvidence(liveTurn))
  const interruptConversation = vi.fn(async () => liveTurn.id)
  const rpc = Object.assign(() => {}, {
    getHostId: async () => {
      if (steering) liveTurn.items.push({ type: 'steeringUserMessage', id: 'new-user-input', status: 'pending', input: [{ type: 'text', text: 'do something else' }] })
      return 'local'
    },
    // RPC transmits a snapshot; it does not share the manager's mutable state.
    getConversation: async () => structuredClone({ id: 'thread', resumeState: 'resumed', turns: [liveTurn] }),
    getStreamRole: async () => ({ role: 'owner' }),
    interruptConversation,
  })
  vi.stubGlobal('__lazymindCodexManager', rendererManager(rpc))
  const result = await codexOperation({ kind: 'interrupt', threadId: 'thread', turnId: 'turn', expectedOwnership })
  expect(result.rejected).toContain('Stop the current response in Codex')
  expect(interruptConversation).not.toHaveBeenCalled()
})
