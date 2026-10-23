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
        const hostSessionId = hooks.hostSessionId
        return control ? { lazymind_workflow: { runId: control.session_id,
          ...(hostSessionId ? { hostSessionId } : {}), ...(hooks.operation ? { operation: hooks.operation } : {}),
          ...(typeof executionId === 'string' ? { executionId } : {}),
        }, original: previous ?? null } : previous ?? null
      },
    },
    async execute(args, exec) {
      const value = await original.execute(args, exec)
      const control = hooks.afterResult ? await hooks.afterResult(value, exec) : readControl(value)
      if (hooks.shouldConclude ? hooks.shouldConclude(control, exec) : control?.continuation === 'awaiting_user') exec.concludeTurn()
      const record = object(value)
      const fields = object(record?.structuredContent)
      const earlier = readControl(value)
      if (control && fields && record && (earlier?.state_version !== control.state_version || earlier?.continuation !== control.continuation)) {
        // Binding is established synchronously after start; make that fresh fact
        // visible to the model while keeping the original tool receipt intact.
        return { ...record, structuredContent: { ...fields, control },
          ...(Array.isArray(record.content) ? { content: [...record.content, { type: 'text', text: JSON.stringify({ control }) }] } : {}),
        }
      }
      return value
    },
  }
}
