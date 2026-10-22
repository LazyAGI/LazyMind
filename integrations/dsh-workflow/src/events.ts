import { interaction, object, readControl, type RunLink } from '../../workflow-agent-core/src/protocol'

// The host's configured frontend owns the URL; tool results carry run identity.
export function presentationLink(runId: unknown, webUrl: string): RunLink | null {
  if (typeof runId !== 'string' || !/^[a-zA-Z0-9_-]{1,256}$/.test(runId)) return null
  try {
    const base = new URL(webUrl)
    if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) return null
    return { runId, url: new URL(`workflow-runs/${encodeURIComponent(runId)}`, base.href.replace(/\/?$/, "/")).href }
  } catch { return null }
}

export function presentationRun(meta: unknown, webUrl: string): RunLink | null {
  const value = object(object(meta)?.lazymind_workflow)
  const run = presentationLink(value?.runId, webUrl)
  if (!run || !value) return null
  return { ...run,
    ...(typeof value.hostSessionId === 'string' ? { hostSessionId: value.hostSessionId } : {}),
    ...(typeof value.operation === 'string' ? { operation: value.operation } : {}),
    ...(typeof value.executionId === 'string' ? { executionId: value.executionId } : {}),
  }
}

const OPERATIONS = new Set(['list', 'get', 'input_import', 'input_get', 'start', 'state', 'session_list',
  'session_stop', 'session_resume', 'step_begin', 'step_claim', 'step_resume', 'step_complete', 'artifact_publish', 'artifact_list', 'artifact_get'])

export function workflowOperation(name: string, serverName: string): string | null {
  const prefix = `mcp__${serverName}__workflow_`
  if (!name.startsWith(prefix)) return null
  // DSH appends a 12-hex identity hash whenever dots are normalized.
  const operation = name.slice(prefix.length).replace(/_[0-9a-f]{12}$/, '')
  return OPERATIONS.has(operation) ? operation : null
}

function visitTexts(value: unknown, into: string[]): void {
  const item = object(value)
  if (!item) return
  if (typeof item.text === 'string') into.push(item.text)
  if (Array.isArray(item.content)) for (const child of item.content) visitTexts(child, into)
}

function runFromToolText(text: string, webUrl: string): RunLink | null {
  const hasRun = text.includes('"session_id"') || text.includes('"lazymind_workflow"')
  if (!hasRun && text.length > 8192 || text.length > 512 * 1024) return null
  try {
    const parsed = JSON.parse(text)
    const value = { structuredContent: object(parsed)?.result ?? parsed }
    const control = readControl(value)
    // Historical URL-bearing receipts remain readable, scoped to configured origin.
    return presentationRun(parsed, webUrl)
      ?? presentationLink(control?.session_id, webUrl)
      ?? interaction(value, webUrl)
  } catch {
    return null
  }
}

/** DSH web logs MCP JSON in tool-result message text. Meta is optional and often absent. */
export function eventRun(event: unknown, serverName: string, webUrl: string): RunLink | null {
  if (!presentationLink('config-check', webUrl)) return null
  const value = object(event)
  const data = object(value?.data)
  if (value?.type === 'tool/result') {
    const fromMeta = presentationRun(data?.meta, webUrl)
    if (fromMeta) return fromMeta
    const texts: string[] = []
    visitTexts(object(data?.message), texts)
    if (Array.isArray(data?.content)) for (const child of data.content) visitTexts(child, texts)
    for (const text of texts.reverse()) {
      const run = runFromToolText(text, webUrl)
      if (run) return run
    }
    return null
  }
  if (value?.type !== 'tool/ptc-dispatch' || data?.isError !== false || typeof data.name !== 'string'
    || !['start', 'state', 'step_begin', 'step_claim', 'step_resume', 'step_complete'].includes(workflowOperation(data.name, serverName) ?? '')
    || !Array.isArray(data.content)) return null
  for (const raw of [...data.content].reverse()) {
    const content = object(raw)
    if (typeof content?.text !== 'string') continue
    const run = runFromToolText(content.text, webUrl)
    if (run) return run
  }
  return null
}
