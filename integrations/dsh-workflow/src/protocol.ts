/** Data owned by LazyMind, carried by the host's standard tool-result event. */
export interface RunLink { runId: string; url: string; hostSessionId?: string; operation?: string; executionId?: string }

export interface WorkflowControl {
  protocol: 'workflow.control.v1'
  session_id: string
  state_version: number
  continuation: string
  admission: { can_begin: boolean; reason?: string }
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

function visitTexts(value: unknown, into: string[]): void {
  const item = object(value)
  if (!item) return
  if (typeof item.text === 'string') into.push(item.text)
  if (Array.isArray(item.content)) for (const child of item.content) visitTexts(child, into)
}

function runFromToolText(text: string): RunLink | null {
  const hasRun = text.includes('"interaction_url"') && text.includes('"session_id"')
  if (!hasRun && text.length > 8192 || text.length > 512 * 1024) return null
  try {
    const parsed = JSON.parse(text)
    return presentationRun(parsed)
      ?? interaction({ structuredContent: parsed })
      ?? interaction({ structuredContent: object(parsed)?.state ?? object(parsed)?.result })
  } catch {
    return null
  }
}

/** DSH web logs MCP JSON in tool-result message text. Meta is optional and often absent. */
export function eventRun(event: unknown, serverName: string): RunLink | null {
  const value = object(event)
  const data = object(value?.data)
  if (value?.type === 'tool/result') {
    const fromMeta = presentationRun(data?.meta)
    if (fromMeta) return fromMeta
    const texts: string[] = []
    visitTexts(object(data?.message), texts)
    if (Array.isArray(data?.content)) for (const child of data.content) visitTexts(child, texts)
    for (const text of texts.reverse()) {
      const run = runFromToolText(text)
      if (run) return run
    }
    return null
  }
  if (value?.type !== 'tool/code-dispatch' || data?.isError !== false || typeof data.name !== 'string'
    || !['start', 'state', 'step_begin', 'step_claim', 'step_resume', 'step_submit'].includes(workflowOperation(data.name, serverName) ?? '')
    || !Array.isArray(data.content)) return null
  for (const raw of [...data.content].reverse()) {
    const content = object(raw)
    if (typeof content?.text !== 'string') continue
    const run = runFromToolText(content.text)
    if (run) return run
  }
  return null
}
