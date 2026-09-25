/** Data owned by LazyMind, carried by the host's standard tool-result event. */
export interface RunLink { runId: string; url: string; hostSessionId?: string; operation?: string; executionId?: string }

export interface WorkflowControl {
  protocol: 'workflow.control.v1'
  session_id: string
  state_version: number
  continuation: string
  admission: { can_begin: boolean; reason?: string }
  native_execution_ids?: string[]
  active_execution_ids?: string[]
  active_executions?: number
  binding?: { provider?: string; connector_id?: string; driver_session_id?: string; generation: number; bound: boolean }
}

export function object(value: unknown): Record<string, unknown> | null {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown> : null
}

export function interaction(value: unknown, trustedOrigin?: string): RunLink | null {
  const result = object(object(value)?.structuredContent)
  const fields = typeof result?.session_id === 'string' ? result : object(result?.state)
  if (!fields || typeof fields.session_id !== 'string' || !fields.session_id
    || typeof fields.interaction_url !== 'string') return null
  try {
    const url = new URL(fields.interaction_url)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash || url.search) return null
    if (trustedOrigin && url.origin !== new URL(trustedOrigin).origin) return null
    if (url.pathname !== `/workflow-runs/${encodeURIComponent(fields.session_id)}`) return null
    return { runId: fields.session_id, url: url.href }
  } catch { return null }
}

export function readControl(value: unknown): WorkflowControl | null {
  const fields = object(object(value)?.structuredContent)
  const control = object(fields?.control) ?? object(object(fields?.state)?.control)
  const admission = object(control?.admission)
  if (control?.protocol !== 'workflow.control.v1' || typeof control.session_id !== 'string'
    || !Number.isSafeInteger(control.state_version) || typeof control.continuation !== 'string'
    || typeof admission?.can_begin !== 'boolean') return null
  return control as unknown as WorkflowControl
}

export function presentationRun(meta: unknown): RunLink | null {
  const value = object(object(meta)?.lazymind_workflow)
  if (!value) return null
  const run = interaction({ structuredContent: { session_id: value.runId, interaction_url: value.url } })
  return run ? { ...run,
    ...(typeof value.hostSessionId === 'string' ? { hostSessionId: value.hostSessionId } : {}),
    ...(typeof value.operation === 'string' ? { operation: value.operation } : {}),
    ...(typeof value.executionId === 'string' ? { executionId: value.executionId } : {}),
  } : null
}
