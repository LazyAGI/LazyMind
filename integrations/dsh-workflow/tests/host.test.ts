import { createHash } from 'node:crypto'
import { Context } from '@deepseek-ai/cordis'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { createScope, scopeTarget } from '@deepseek-ai/dsh-scope'
import { Session, type SessionId, type UserMessage } from '@deepseek-ai/dsh-session'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime, { type ToolDefinition, type ToolExecutionInput } from '@deepseek-ai/dsh-tools'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BridgeError, type HostTransport } from '../src/bridge'
import { installHost } from '../src/host'
import type { WorkflowControl } from '../src/protocol'

// Match the released MCP client's publicToolName contract, including identity suffixes.
const publicName = (name: string) => name.startsWith('mcp__lazymind__workflow_')
  ? `${name}_${createHash('sha256').update(`lazymind\0${name.slice('mcp__lazymind__'.length).replaceAll('_', '.')}`).digest('hex').slice(0, 12)}` : name

const cleanup: Array<() => Promise<unknown>> = []
afterEach(async () => { for (const dispose of cleanup.splice(0).reverse()) await dispose() })

async function fixture({ history, native = false }: { history?: 'start' | 'completion'; native?: boolean } = {}) {
  const ctx = new Context()
  const prompt = ctx.plugin(SystemPrompt)
  await prompt.await(); cleanup.push(() => prompt.dispose())
  const tools = ctx.plugin(ToolRuntime)
  await tools.await(); cleanup.push(() => tools.dispose())
  let toolsContext = ctx
  const access = ctx.inject(['tools'], injected => { toolsContext = injected })
  await access.await(); cleanup.push(() => access.dispose())
  const agents: Agent[] = []
  const parents = new Map<Agent, Agent>()
  // Fake lifecycle handles at the host boundary; scope routing and tool execution use the released SDK.
  const makeAgent = (id: string, parent?: Agent) => {
    const handle = { session: Session.create(id as SessionId), status: 'running', ctx }
    const agent = handle as unknown as Agent
    const scope = createScope(toolsContext, agent, parent ? { parent } : undefined)
    handle.ctx = scope.ctx
    agents.push(agent)
    if (parent) parents.set(agent, parent)
    cleanup.push(() => scope.dispose())
    return agent
  }
  const root = makeAgent('native-root')
  const child = makeAgent('native-child', root)
  const unrelated = makeAgent('native-unrelated')
  ctx.provide('agents', { list: () => agents, get: (id: string) => agents.find(a => a.session.id === id),
    isOwnedBy: (id: string, parent: Agent) => parents.get(agents.find(a => a.session.id === id)!) === parent })
  let control: WorkflowControl = { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: 'continue', admission: { can_begin: true }, active_execution_ids: [], active_executions: 0,
    binding: { driver_session_id: 'native-root', bound: true } }
  const bridge: HostTransport = {
    bind: vi.fn(async () => control), state: vi.fn(async () => control),
    actions: vi.fn(async () => ({ actions: [] })),
    action: vi.fn(async () => { throw new BridgeError('WORKFLOW_NOT_FOUND', 'not a host action', 404) }),
    claim: vi.fn(), settle: vi.fn(),
  }
  const definition = (name: string, execute: ToolDefinition['execute']): ToolDefinition => ({
    name: publicName(name), description: name, parameters: { type: 'object', properties: {} },
    output: { schema: { type: 'object' }, render: () => [{ type: 'text', text: 'ok' }] }, execute,
  })
  ctx.tools.register(definition('mcp__lazymind__workflow_state', async () => ({ structuredContent: {
    session_id: 'unrelated-run', interaction_url: 'http://localhost:8090/workflow-runs/unrelated-run',
  } })))
  ctx.tools.register(definition('mcp__lazymind__workflow_start', async () => ({ structuredContent: {
    session_id: 'run-1', interaction_url: 'http://localhost:8090/workflow-runs/run-1', control,
  } })))
  const begin = vi.fn<ToolDefinition['execute']>(async () => {
    control = { ...control, state_version: 2, active_execution_ids: ['attempt-1'], active_executions: 1, ...(native ? { native_execution_ids: ['attempt-1'], continuation: 'awaiting_executor', admission: { can_begin: false } } : {}) }
    return { structuredContent: { execution: { execution_id: 'attempt-1', executor_host: native ? 'lazymind' : 'external-agent' }, state: { control } } }
  })
  ctx.tools.register(definition('mcp__lazymind__workflow_step_begin', begin))
  const publish = vi.fn<ToolDefinition['execute']>(async () => ({ structuredContent: { saved: true } }))
  ctx.tools.register(definition('mcp__lazymind__workflow_artifact_publish', publish))
  ctx.tools.register(definition('mcp__lazymind__workflow_step_complete', async () => {
    control = { ...control, state_version: 3, active_execution_ids: [], active_executions: 0,
      continuation: 'awaiting_user', admission: { can_begin: false, reason: 'review_pending' } }
    return { structuredContent: { execution_id: 'attempt-1', control } }
  }))
  const shell = vi.fn(async () => ({ done: true }))
  ctx.tools.register(definition('shell', shell))
  child.ctx.tools.register(definition('structured_output', async (_args, exec) => { exec.concludeTurn(); return { recorded: true } }))
  if (history === 'completion') {
    control = { ...control, continuation: 'awaiting_user', state_version: 3, admission: { can_begin: false } }
    root.session.append('tool/ptc-dispatch', {
      rootCallId: 'code' as ToolExecutionInput['callId'], parentCallId: 'code' as ToolExecutionInput['callId'],
      subCallId: 'code:1' as ToolExecutionInput['callId'], name: publicName('mcp__lazymind__workflow_step_complete'),
      arguments: {}, isError: false, content: [{type: 'text', text: JSON.stringify({lazymind_workflow: {
        runId: 'run-1', url: 'http://localhost:8090/workflow-runs/run-1', hostSessionId: 'native-root',
        operation: 'step_complete', executionId: 'attempt-1',
      }})}],
    })
  }
  if (history === 'start') root.session.append('tool/ptc-dispatch', {
    rootCallId: 'start' as ToolExecutionInput['callId'], parentCallId: 'start' as ToolExecutionInput['callId'],
    subCallId: 'start:1' as ToolExecutionInput['callId'], name: publicName('mcp__lazymind__workflow_start'),
    arguments: {}, isError: false, content: [{ type: 'text', text: JSON.stringify({ lazymind_workflow: {
      runId: 'run-1', url: 'http://localhost:8090/workflow-runs/run-1', hostSessionId: root.session.id, operation: 'start',
    } }) }],
  })
  const installed = ctx.inject(['tools', 'agents'], injected => {
    const dispose = installHost(injected, bridge, { serverName: 'lazymind', webUrl: 'http://localhost:8090' }, 'instance-1')
    injected.effect(() => dispose)
  })
  await installed.await()
  cleanup.push(() => installed.dispose())
  let call = 0
  const execute = (name: string, agent = root, args: unknown = {}) => ctx.tools.execute({
    callId: `call-${++call}` as ToolExecutionInput['callId'], name: publicName(name), agent, arguments: args, signal: new AbortController().signal,
  })
  const turn = (agent = root, messages: UserMessage[] = []) => ctx.waterfall(scopeTarget(agent, agent), 'agent/pre-step', {
    agent, turn: 2, step: 0, messages, signal: new AbortController().signal,
  }, async () => ({ kind: 'enter' as const, messages }))
  return { turn, ctx, root, child, unrelated, bridge, shell, execute, begin, publish }
}

describe('workflow host isolation through DSH public scopes', () => {
  it('concludes human submission and gates the next automatic step without guarding tools', async () => {
    const f = await fixture()
    await f.execute('mcp__lazymind__workflow_start')
    await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })
    const result = await f.execute('mcp__lazymind__workflow_step_complete', f.root, { session_id: 'run-1', execution_id: 'attempt-1' })
    expect(result).toMatchObject({ isError: false, concludesTurn: true })
    expect((await f.turn()).kind).toBe('reject')
    expect((await f.turn(f.unrelated)).kind).toBe('enter')
    // Direct tool calls reach their implementation; Core owns Workflow request validation.
    expect((await f.execute('shell')).isError).toBe(false)
    expect((await f.execute('shell', f.unrelated)).isError).toBe(false)
    expect(f.shell).toHaveBeenCalledTimes(2)
  })

  it('lets a child report its required structured output while its parent remains paused', async () => {
    const f = await fixture()
    await f.execute('mcp__lazymind__workflow_start')
    await f.execute('mcp__lazymind__workflow_step_begin', f.child, { session_id: 'run-1' })
    const submitted = await f.execute('mcp__lazymind__workflow_step_complete', f.child, { session_id: 'run-1', execution_id: 'attempt-1' })
    expect(submitted.isError).toBe(false)
    expect(submitted).not.toHaveProperty('concludesTurn')
    expect((await f.turn()).kind).toBe('reject')
    vi.mocked(f.bridge.state).mockClear().mockRejectedValue(new Error('bridge offline'))
    expect((await f.turn(f.child)).kind).toBe('enter')
    expect(f.bridge.state).not.toHaveBeenCalled()
    expect(await f.execute('structured_output', f.child)).toMatchObject({ isError: false, concludesTurn: true })
  })

  it('allows user conversation and forwards Core rejection without a host tool gate', async () => {
    const f = await fixture()
    await f.execute('mcp__lazymind__workflow_start')
    await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })
    await f.execute('mcp__lazymind__workflow_step_complete', f.root, { session_id: 'run-1', execution_id: 'attempt-1' })
    const messages = [{ source: { kind: 'user', rpcId: 'manual-1' }, content: [{ type: 'text', text: 'Check something else' }] }] as unknown as UserMessage[]
    const decision = await f.turn(f.root, messages)
    expect(decision.kind).toBe('enter')
    expect((await f.execute('shell', f.root)).isError).toBe(false)
    vi.mocked(f.bridge.state).mockClear()
    f.begin.mockRejectedValueOnce(new Error('WORKFLOW_ADMISSION_DENIED: review pending'))
    const args = { session_id: 'run-1', step_id: 'next' }
    const rejected = await f.execute('mcp__lazymind__workflow_step_begin', f.root, args)
    expect(f.begin).toHaveBeenLastCalledWith(args, expect.anything())
    expect(rejected.isError).toBe(true)
    expect(JSON.stringify(rejected.content)).toContain('WORKFLOW_ADMISSION_DENIED: review pending')
    expect(f.bridge.state).not.toHaveBeenCalled()
  })
})

it('restores a review barrier from a standard PTC event before an automatic step after restart', async () => {
  const f = await fixture({ history: 'completion' })
  expect((await f.turn()).kind).toBe('reject')
})

it('concludes a created workflow when synchronous binding is unavailable', async () => {
  const f = await fixture()
  vi.mocked(f.bridge.bind).mockRejectedValueOnce(new Error('bridge offline'))
  vi.mocked(f.bridge.state).mockResolvedValue({protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1, continuation: 'binding_required', admission: {can_begin: false}, binding: {bound: false}})
  const result = await f.execute('mcp__lazymind__workflow_start')
  expect(result).toMatchObject({isError: false, concludesTurn: true})
  expect((await f.turn()).kind).toBe('reject')
})

it('yields automatic execution while Core runs a native step', async () => {
  const f = await fixture({ native: true })
  await f.execute('mcp__lazymind__workflow_start')
  expect(await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })).toMatchObject({ isError: false, concludesTurn: true })
  expect((await f.turn()).kind).toBe('reject')
  expect(f.shell).not.toHaveBeenCalled()
  expect((await f.execute('shell', f.unrelated)).isError).toBe(false)
})

it('does not bind historical discovery reads to the current driver', async () => {
  const f = await fixture()
  vi.mocked(f.bridge.state).mockRejectedValue(new Error('legacy run has no control binding'))
  expect((await f.execute('mcp__lazymind__workflow_state', f.root, { session_id: 'unrelated-run' })).isError).toBe(false)
  expect(f.bridge.state).not.toHaveBeenCalled()
  expect((await f.execute('mcp__lazymind__workflow_start')).isError).toBe(false)
})

it('delivers a queued wake through the DSH session API without cancelling the current turn', async () => {
  const f = await fixture()
  await f.execute('mcp__lazymind__workflow_start')
  const control = await f.bridge.state('run-1', new AbortController().signal)
  const cancel = vi.fn()
  const prompt = vi.fn(async (_input: { content: Array<{text: string}> }) => ({}))
  f.ctx.provide('sessionController', { resolveAgent: async () => ({ agent: f.root }), cancel, prompt })
  const action = { id: 'panel-continue', session_id: 'run-1', kind: 'continue' as const,
    native_session_id: f.root.session.id, status: 'pending' }
  const claim = { action: { ...action, status: 'dispatching' }, dispatch_token: 'dispatch', control }
  vi.mocked(f.bridge.claim).mockResolvedValue(claim)
  vi.mocked(f.bridge.settle).mockResolvedValue(undefined)
  vi.mocked(f.bridge.actions).mockResolvedValueOnce({ actions: [action] })
  await vi.waitFor(() => expect(prompt).toHaveBeenCalledOnce(), { timeout: 2500 })
  expect(prompt).toHaveBeenCalledWith(expect.objectContaining({ sessionId: f.root.session.id, requestId: action.id, mode: 'queue' }), expect.any(AbortSignal))
  expect(f.bridge.action).not.toHaveBeenCalled()
  expect(cancel).not.toHaveBeenCalled()
})

it('preserves publication success and Core rejection without concluding the turn', async () => {
  const f = await fixture()
  await f.execute('mcp__lazymind__workflow_start')
  await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })
  const args = { session_id: 'run-1', execution_id: 'attempt-1', output: { slot: 'pages', seq: 1, value: 'page' } }
  const published = await f.execute('mcp__lazymind__workflow_artifact_publish', f.root, args)
  expect(published.isError).toBe(false)
  expect(published.concludesTurn).not.toBe(true)
  f.publish.mockRejectedValueOnce(new Error('INVALID_EXECUTION_HANDLE'))
  const invalid = { ...args, execution_id: 'other' }
  const rejected = await f.execute('mcp__lazymind__workflow_artifact_publish', f.root, invalid)
  expect(f.publish).toHaveBeenLastCalledWith(invalid, expect.anything())
  expect(rejected.isError).toBe(true)
  expect(JSON.stringify(rejected.content)).toContain('INVALID_EXECUTION_HANDLE')
  expect(rejected.concludesTurn).not.toBe(true)
})

it('repairs an unbound run after restart only when the driver owns its persisted start receipt', async () => {
  const f = await fixture({ history: 'start' })
  vi.mocked(f.bridge.state).mockResolvedValue({ protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: 'binding_required', admission: { can_begin: false }, binding: { bound: false } })
  const decision = await f.turn()
  expect(f.bridge.bind).toHaveBeenCalledWith('run-1', f.root.session.id, expect.any(AbortSignal))
  expect(decision.kind).toBe('enter')
})

it('does not repair binding based only on a completion receipt', async () => {
  const f = await fixture({ history: 'completion' })
  vi.mocked(f.bridge.state).mockResolvedValue({ protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
    continuation: 'binding_required', admission: { can_begin: false }, binding: { bound: false } })
  await f.turn()
  expect(f.bridge.bind).not.toHaveBeenCalled()
})
