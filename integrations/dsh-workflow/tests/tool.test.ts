import { Context } from '@deepseek-ai/cordis'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime, { type ToolDefinition, type ToolResult, type ToolExecutionInput } from '@deepseek-ai/dsh-tools'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { interaction, presentationRun, workflowOperation } from '../src/protocol'
import { workflowTool } from '../src/tool'

type ToolCallId = ToolExecutionInput['callId']
const cleanup: Array<() => Promise<void>> = []
afterEach(async () => { for (const dispose of cleanup.splice(0).reverse()) await dispose() })

async function runtime() {
  const ctx = new Context()
  const prompt = ctx.plugin(SystemPrompt)
  cleanup.push(() => prompt.dispose())
  await prompt.await()
  const tools = ctx.plugin(ToolRuntime)
  cleanup.push(() => tools.dispose())
  await tools.await()
  return ctx
}

function definition(continuation = 'awaiting_user'): ToolDefinition {
  return {
    name: 'mcp__lazymind__workflow_step_complete',
    description: 'Submit fixture',
    parameters: { type: 'object', properties: {} },
    output: { schema: { type: 'object' }, render: () => [{ type: 'text', text: 'success' }] },
    async execute() {
      return { structuredContent: {
        session_id: 'run-1',
        control: { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 3,
          continuation, admission: { can_begin: continuation !== 'awaiting_user' } },
      } }
    },
  }
}

describe('released DSH tool contract', () => {
  it('concludes a successful human result through the public runtime', async () => {
    const ctx = await runtime()
    const original = definition()
    const finalize = vi.fn(() => [{ type: 'text' as const, text: 'original rich content' }])
    original.finalizeContent = finalize
    ctx.tools.register(workflowTool(original, {}))
    const result = await ctx.tools.execute({ callId: 'call-1' as ToolCallId,
      name: original.name, arguments: {}, signal: new AbortController().signal })
    expect(result.isError).toBe(false)
    expect(result).toMatchObject({ concludesTurn: true, content: [{ type: 'text', text: 'original rich content' }] })
    expect(finalize).toHaveBeenCalledOnce()
    expect(presentationRun(result.meta, 'http://localhost:8090')).toEqual({ runId: 'run-1', url: 'http://localhost:8090/workflow-runs/run-1' })
    expect(JSON.stringify(result)).not.toContain('http://localhost:8090')
    expect(Object.isFrozen(result)).toBe(true)
  })

  it('keeps a continuing Workflow result nonterminal', async () => {
    const ctx = await runtime()
    const original = definition('continue')
    ctx.tools.register(workflowTool(original, {}))
    const input = { callId: 'call-1' as ToolCallId, name: original.name,
      arguments: {}, signal: new AbortController().signal }
    expect(await ctx.tools.execute(input)).not.toHaveProperty('concludesTurn')
  })

  it('does not conclude failed tools', async () => {
    const ctx = await runtime()
    const original = definition()
    original.execute = async () => { throw new Error('submission rejected') }
    ctx.tools.register(workflowTool(original, {}))
    const result = await ctx.tools.execute({ callId: 'call-1' as ToolCallId,
      name: original.name, arguments: {}, signal: new AbortController().signal })
    expect(result.isError).toBe(true)
    expect(result).not.toHaveProperty('concludesTurn')
  })
})

describe('presentation protocol', () => {
  it('records model job identity and status for shared recovery', () => {
    const wrapped = workflowTool(definition(), { operation: 'generation_claim', hostSessionId: 'native-1' })
    const value = { structuredContent: { session_id: 'run-1', job_id: 'model-job', status: 'running' } }
    const meta = wrapped.output.presentationMeta?.({}, value)
    expect(presentationRun(meta, 'http://localhost:8090')).toMatchObject({
      runId: 'run-1', hostSessionId: 'native-1', operation: 'generation_claim', modelJobId: 'model-job', modelJobStatus: 'running',
    })
  })
  it('matches server names literally', () => {
    expect(workflowOperation('mcp__lazy.mind__workflow_start', 'lazy.mind')).toBe('start')
    expect(workflowOperation('mcp__lazyXmind__workflow_start', 'lazy.mind')).toBeNull()
  })
  it('rejects untrusted origins and mismatched run paths', () => {
    expect(interaction({ structuredContent: { session_id: 'r', interaction_url: 'https://evil.example/workflow-runs/r' } }, 'https://trusted.example')).toBeNull()
    expect(interaction({ structuredContent: { session_id: 'r', interaction_url: 'https://trusted.example/workflow-runs/other' } })).toBeNull()
  })
})


it('keeps opaque execution handles out of call titles and forwards original presentation metadata', () => {
  const original = definition()
  const presenter = vi.fn((_args: unknown, _result: ToolResult) => undefined)
  original.presentResult = presenter
  const wrapped = workflowTool(original, {operation: 'step_complete'})
  expect(JSON.stringify(wrapped.presentCall?.({execution_handle: 'private-handle', session_id: 'run-1'}))).not.toContain('private-handle')
  wrapped.presentResult?.({}, {content: [], isError: false, meta: {lazymind_workflow: {}, original: {rich: true}}})
  expect(presenter.mock.calls[0][1].meta).toEqual({rich: true})
})

it('uses the issuing DSH session and stable retry identity, then reports the confirmed binding', async () => {
  const original = definition('binding_required')
  const execute = vi.fn(original.execute)
  const wrapped = workflowTool({ ...original, execute }, {
    operation: 'start', hostSessionId: 'native-chat-A',
    afterResult: async () => ({
      protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 4, continuation: 'continue',
      admission: { can_begin: true }, active_execution_ids: [],
      binding: { bound: true, driver_session_id: 'native-chat-A' },
    }),
  })
  const exec = { callId: 'call-1' as ToolCallId } as Parameters<typeof wrapped.execute>[1]
  const value = await wrapped.execute({ workflow_id: 'workflow-A', driver_session_id: 'model-supplied-chat-B' }, exec)
  await wrapped.execute({ workflow_id: 'workflow-A' }, exec)
  expect(execute.mock.calls[0][0]).toMatchObject({ driver_session_id: 'native-chat-A', idempotency_key: expect.stringMatching(/^dsh-[a-f0-9]{64}$/) })
  expect(execute.mock.calls[0][0]).toEqual(execute.mock.calls[1][0])
  expect(value).toMatchObject({ structuredContent: { session_id: 'run-1', binding_status: 'confirmed' } })
})
