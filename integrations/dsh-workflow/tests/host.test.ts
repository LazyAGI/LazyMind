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

async function fixture(seedPTC = false, native = false) {
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
    binding: { driver_session_id: 'native-root', generation: 1, bound: true } }
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
  ctx.tools.register(definition('mcp__lazymind__workflow_start', async () => ({ structuredContent: {
    session_id: 'run-1', interaction_url: 'http://localhost:8090/workflow-runs/run-1', control,
  } })))
  ctx.tools.register(definition('mcp__lazymind__workflow_step_begin', async () => {
    control = { ...control, state_version: 2, active_execution_ids: ['attempt-1'], active_executions: 1, ...(native ? { native_execution_ids: ['attempt-1'], continuation: 'awaiting_executor', admission: { can_begin: false } } : {}) }
    return { structuredContent: { execution: { execution_id: 'attempt-1', executor_host: native ? 'lazymind' : 'external-agent' }, state: { control } } }
  }))
  ctx.tools.register(definition('mcp__lazymind__workflow_step_submit', async () => {
    control = { ...control, state_version: 3, active_execution_ids: [], active_executions: 0,
      continuation: 'awaiting_user', admission: { can_begin: false, reason: 'review_pending' } }
    return { structuredContent: { execution_id: 'attempt-1', control } }
  }))
  const shell = vi.fn(async () => ({ done: true }))
  ctx.tools.register(definition('shell', shell))
  child.ctx.tools.register(definition('structured_output', async (_args, exec) => { exec.concludeTurn(); return { recorded: true } }))
  if (seedPTC) {
    control = { ...control, continuation: 'awaiting_user', state_version: 3, admission: { can_begin: false } }
    root.session.append('tool/code-dispatch', {
      rootCallId: 'code' as ToolExecutionInput['callId'], parentCallId: 'code' as ToolExecutionInput['callId'],
      subCallId: 'code:1' as ToolExecutionInput['callId'], name: publicName('mcp__lazymind__workflow_step_submit'),
      arguments: {}, isError: false, content: [{type: 'text', text: JSON.stringify({lazymind_workflow: {
        runId: 'run-1', url: 'http://localhost:8090/workflow-runs/run-1', hostSessionId: 'native-root',
        operation: 'step_submit', executionId: 'attempt-1',
      }})}],
    })
  }
  const installed = ctx.inject(['tools', 'agents'], injected => {
    const dispose = installHost(injected, bridge, { serverName: 'lazymind', webUrl: 'http://localhost:8090' }, 'instance-1')
    injected.effect(() => dispose)
  })
  await installed.await()
  cleanup.push(() => installed.dispose())
  let call = 0
  const execute = (name: string, agent = root, args: unknown = {}, parent?: ToolExecutionInput['parent']) => ctx.tools.execute({
    callId: `call-${++call}` as ToolExecutionInput['callId'], name: publicName(name), agent, arguments: args, parent, signal: new AbortController().signal,
  })
  return { ctx, root, child, unrelated, bridge, shell, execute, definition }
}

describe('workflow host isolation through DSH public scopes', () => {
  it('ends human submission and blocks the next tool in the same batch without blocking other sessions', async () => {
    const f = await fixture()
    await f.execute('mcp__lazymind__workflow_start')
    await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })
    const result = await f.execute('mcp__lazymind__workflow_step_submit', f.root, { session_id: 'run-1', execution_id: 'attempt-1' })
    expect(result).toMatchObject({ isError: false, concludesTurn: true })
    expect((await f.execute('shell')).isError).toBe(true)
    expect(f.shell).not.toHaveBeenCalled()
    expect((await f.execute('shell', f.unrelated)).isError).toBe(false)
    expect(f.shell).toHaveBeenCalledOnce()
  })

  it('lets a child report its required structured output while its parent remains paused', async () => {
    const f = await fixture()
    await f.execute('mcp__lazymind__workflow_start')
    await f.execute('mcp__lazymind__workflow_step_begin', f.child, { session_id: 'run-1' })
    const submitted = await f.execute('mcp__lazymind__workflow_step_submit', f.child, { session_id: 'run-1', execution_id: 'attempt-1' })
    expect(submitted.isError).toBe(false)
    expect(submitted).not.toHaveProperty('concludesTurn')
    expect((await f.execute('shell', f.child)).isError).toBe(true)
    expect(await f.execute('structured_output', f.child)).toMatchObject({ isError: false, concludesTurn: true })
    expect((await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })).isError).toBe(true)
  })

  it('accepts an unrelated explicit user turn while the workflow remains guarded', async () => {
    const f = await fixture()
    await f.execute('mcp__lazymind__workflow_start')
    await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })
    await f.execute('mcp__lazymind__workflow_step_submit', f.root, { session_id: 'run-1', execution_id: 'attempt-1' })
    const messages = [{ source: { kind: 'user', rpcId: 'manual-1' }, content: [{ type: 'text', text: 'Check something else' }] }] as unknown as UserMessage[]
    const decision = await f.ctx.waterfall(scopeTarget(f.root, f.root), 'agent/pre-step', {
      agent: f.root, turn: 2, step: 0, messages, signal: new AbortController().signal,
    }, async () => ({ kind: 'enter' as const, messages }))
    expect(decision.kind).toBe('enter')
    expect((await f.execute('shell', f.root)).isError).toBe(false)
    expect((await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })).isError).toBe(true)
  })
})


it('restores a review barrier from a standard PTC event before an automatic step after restart', async () => {
  const f = await fixture(true)
  const decision = await f.ctx.waterfall(scopeTarget(f.root, f.root), 'agent/pre-step', {
    agent: f.root, turn: 2, step: 0, messages: [], signal: new AbortController().signal,
  }, async () => ({kind: 'enter' as const, messages: []}))
  expect(decision.kind).toBe('reject')
  expect((await f.execute('shell')).isError).toBe(true)
})


it('concludes a created workflow when synchronous binding is unavailable', async () => {
  const f = await fixture()
  vi.mocked(f.bridge.bind).mockRejectedValueOnce(new Error('bridge offline'))
  vi.mocked(f.bridge.state).mockResolvedValue({protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1, continuation: 'binding_required', admission: {can_begin: false}, binding: {generation: 0, bound: false}})
  const result = await f.execute('mcp__lazymind__workflow_start')
  expect(result).toMatchObject({isError: false, concludesTurn: true})
  expect((await f.execute('shell')).isError).toBe(true)
})

it('lets a child return its committed result even when the subsequent Bridge read is unavailable', async () => {
  const f = await fixture()
  await f.execute('mcp__lazymind__workflow_start')
  await f.execute('mcp__lazymind__workflow_step_begin', f.child, {session_id: 'run-1'})
  const read = vi.mocked(f.bridge.state)
  // Pre-execute read succeeds; only the committed result's synchronization fails.
  const current = await f.bridge.state('run-1', new AbortController().signal)
  read.mockResolvedValueOnce(current).mockRejectedValue(new Error('bridge offline'))
  const submitted=await f.execute('mcp__lazymind__workflow_step_submit',f.child,{session_id:'run-1',execution_id:'attempt-1'})
  expect(submitted.isError).toBe(false)
  expect((await f.execute('structured_output', f.child)).isError).toBe(false)
})


it('yields native tool steps without granting DSH permission to imitate their tools', async () => {
  const f = await fixture(false, true)
  await f.execute('mcp__lazymind__workflow_start')
  expect(await f.execute('mcp__lazymind__workflow_step_begin', f.root, { session_id: 'run-1' })).toMatchObject({ isError: false, concludesTurn: true })
  expect((await f.execute('shell')).isError).toBe(true)
  expect(f.shell).not.toHaveBeenCalled()
  expect((await f.execute('shell', f.unrelated)).isError).toBe(false)
})
