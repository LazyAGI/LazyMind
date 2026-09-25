import { interaction, object, presentationRun, type RunLink } from '../../workflow-agent-core/src/protocol'

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
  if (value?.type !== 'tool/ptc-dispatch' || data?.isError !== false || typeof data.name !== 'string'
    || !['start', 'state', 'step_begin', 'step_claim', 'step_resume', 'step_complete'].includes(workflowOperation(data.name, serverName) ?? '')
    || !Array.isArray(data.content)) return null
  for (const raw of [...data.content].reverse()) {
    const content = object(raw)
    if (typeof content?.text !== 'string') continue
    const run = runFromToolText(content.text)
    if (run) return run
  }
  return null
}
