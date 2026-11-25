import { createHash } from 'node:crypto'
import type { ToolDefinition, ToolRunContext } from '@deepseek-ai/dsh-tools'
import { object, readControl, type WorkflowControl } from './protocol'

export interface ToolHooks {
  hostSessionId?: string
  operation?: string
  afterResult?(value: unknown, exec: ToolRunContext): Promise<WorkflowControl | null>
  shouldConclude?(control: WorkflowControl | null, exec: ToolRunContext): boolean
}

/** Preserve the MCP definition's output, cancellation and execution-local finalizer. */
export function workflowTool(original: ToolDefinition, hooks: ToolHooks): ToolDefinition {
  return {
    ...original,
    // A submission is an ordering barrier; review must become visible before the next call.
    isConcurrencySafe: () => false,
    presentCall(args) {
      const fields = object(args)
      const rawInput = fields ? Object.fromEntries(['workflow_id', 'session_id', 'step_id', 'execution_id']
        .filter(key => typeof fields[key] === 'string').map(key => [key, fields[key]])) : undefined
      return { card: 'generic', title: `LazyMind Workflow: ${(hooks.operation ?? 'operation').replaceAll('_', ' ')}`, rawInput }
    },
    presentResult(args, result) {
      const meta = result.meta
      return original.presentResult?.(args, { ...result, meta: meta && typeof meta === 'object' && !Array.isArray(meta) && 'original' in meta ? meta.original : result.meta })
    },
    output: {
      ...original.output,
      presentationMeta(args, value) {
        const previous = original.output.presentationMeta?.(args, value)
        const control = readControl(value)
        const fields = object(object(value)?.structuredContent)
        const executionId = fields?.execution_id ?? object(fields?.execution)?.execution_id
        const modelOperation = hooks.operation === 'generation_claim' || hooks.operation === 'generation_respond'
        const runId = control?.session_id ?? (modelOperation && typeof fields?.session_id === 'string' ? fields.session_id : undefined)
        const hostSessionId = hooks.hostSessionId
        return runId ? { lazymind_workflow: { runId,
          ...(hostSessionId ? { hostSessionId } : {}), ...(hooks.operation ? { operation: hooks.operation } : {}),
          ...(typeof executionId === 'string' ? { executionId } : {}),
          ...(typeof fields?.job_id === 'string' && typeof fields.status === 'string' ? { modelJobId: fields.job_id, modelJobStatus: fields.status } : {}),
        }, original: previous ?? null } : previous ?? null
      },
    },
    async execute(args, exec) {
      const start = hooks.operation === 'start'
      const startArgs = start ? object(args) : undefined
      if (startArgs && hooks.hostSessionId) {
        // The wrapper obtains this ID from the scoped DSH Agent. Model input
        // cannot replace it. DSH retries of the same call use the same key.
        const key = typeof startArgs.idempotency_key === 'string' && startArgs.idempotency_key
          ? startArgs.idempotency_key : 'dsh-' + createHash('sha256').update(hooks.hostSessionId + '\0' + exec.callId).digest('hex')
        args = { ...startArgs, driver_session_id: hooks.hostSessionId, idempotency_key: key }
      }
      const value = await original.execute(args, exec)
      const control = hooks.afterResult ? await hooks.afterResult(value, exec) : readControl(value)
      if (hooks.shouldConclude ? hooks.shouldConclude(control, exec) : control?.continuation === 'awaiting_user') exec.concludeTurn()
      const record = object(value)
      const fields = object(record?.structuredContent)
      const earlier = readControl(value)
      const confirmed = start && control?.binding?.bound === true && control.binding.driver_session_id === hooks.hostSessionId
      if (control && fields && record && (confirmed || earlier?.state_version !== control.state_version || earlier?.continuation !== control.continuation)) {
        const update = { control, ...(confirmed ? { binding_status: 'confirmed', agent_instruction: '' } : {}) }
        return { ...record, structuredContent: { ...fields, ...update },
          ...(Array.isArray(record.content) ? { content: [...record.content, { type: 'text', text: JSON.stringify(update) }] } : {}),
        }
      }
      return value
    },
  }
}
