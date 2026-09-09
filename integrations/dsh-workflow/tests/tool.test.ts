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
    name: 'mcp__lazymind__workflow_step_submit',
    description: 'Submit fixture',
    parameters: { type: 'object', properties: {} },
    output: { schema: { type: 'object' }, render: () => [{ type: 'text', text: 'success' }] },
    async execute() {
      return { structuredContent: {
        session_id: 'run-1', interaction_url: 'http://localhost:8090/workflow-runs/run-1',
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
    expect(presentationRun(result.meta)).toEqual({ runId: 'run-1', url: 'http://localhost:8090/workflow-runs/run-1' })
    expect(Object.isFrozen(result)).toBe(true)
  })

  it('keeps automatic steps nonterminal and applies final guards', async () => {
    const ctx = await runtime()
    const original = definition('continue')
    const execute = vi.spyOn(original, 'execute')
    ctx.tools.register(workflowTool(original, {}))
    const input = { callId: 'call-1' as ToolCallId, name: original.name,
      arguments: {}, signal: new AbortController().signal }
    expect(await ctx.tools.execute(input)).not.toHaveProperty('concludesTurn')
    ctx.tools.guard(() => 'review pending')
    expect((await ctx.tools.execute({ ...input, callId: 'call-2' as ToolCallId })).isError).toBe(true)
    expect(execute).toHaveBeenCalledOnce()
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
  const wrapped = workflowTool(original, {operation: 'step_submit'})
  expect(JSON.stringify(wrapped.presentCall?.({execution_handle: 'private-handle', session_id: 'run-1'}))).not.toContain('private-handle')
  wrapped.presentResult?.({}, {content: [], isError: false, meta: {lazymind_workflow: {}, original: {rich: true}}})
  expect(presenter.mock.calls[0][1].meta).toEqual({rich: true})
})
