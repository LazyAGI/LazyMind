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

const OPERATIONS = new Set(['list', 'get', 'input_import', 'input_get', 'start', 'state', 'session_list',
  'session_stop', 'session_resume', 'step_begin', 'step_claim', 'step_resume', 'step_submit', 'artifact_list', 'artifact_get'])

export function workflowOperation(name: string, serverName: string): string | null {
  const prefix = `mcp__${serverName}__workflow_`
  if (!name.startsWith(prefix)) return null
  // DSH 0.1.2 appends a 12-hex identity hash whenever dots are normalized.
  const operation = name.slice(prefix.length).replace(/_[0-9a-f]{12}$/, '')
  return OPERATIONS.has(operation) ? operation : null
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

/** Read only our standard-event projection, including PTC's standard nested dispatch log. */
export function eventRun(event: unknown, serverName: string): RunLink | null {
  const value = object(event)
  const data = object(value?.data)
  if (value?.type === 'tool/result') return presentationRun(data?.meta)
  if (value?.type !== 'tool/code-dispatch' || data?.isError !== false || typeof data.name !== 'string'
    || !['start', 'state', 'step_begin', 'step_claim', 'step_resume', 'step_submit'].includes(workflowOperation(data.name, serverName) ?? '')
    || !Array.isArray(data.content)) return null
  for (const raw of [...data.content].reverse()) {
    const content = object(raw)
    if (content?.type !== 'text' || typeof content.text !== 'string' || content.text.length > 8192) continue
    try { const run = presentationRun(JSON.parse(content.text)); if (run) return run } catch { /* ordinary tool text */ }
  }
  return null
}
