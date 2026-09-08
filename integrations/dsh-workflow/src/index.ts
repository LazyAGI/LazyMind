import type { Context } from '@deepseek-ai/cordis'
import type { SessionEventMap } from '@deepseek-ai/dsh-session'

declare module '@deepseek-ai/dsh-session' {
  interface SessionEventMap {
    'lazymind-workflow/open': { runId: string; url: string }
  }
}

export interface Config { bridgeUrl: string; dshUrl: string; serverName: string }

function interaction(value: unknown): { runId: string; url: string } | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null
  const result = value as Record<string, unknown>
  const structured = result.structuredContent
  if (typeof structured !== 'object' || structured === null || Array.isArray(structured)) return null
  const fields = structured as Record<string, unknown>
  if (typeof fields.session_id !== 'string' || typeof fields.interaction_url !== 'string') return null
  try {
    const url = new URL(fields.interaction_url)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return null
    return { runId: fields.session_id, url: url.href }
  } catch { return null }
}

/** Persist LazyMind run links and bind each run to its owning DSH Session. */
export function apply(ctx: Context, config: Config): void {
  ctx.on('tools/result', (exec, result) => {
    if (exec.agent === undefined || result.isError || !new RegExp(`^mcp__${config.serverName}__workflow_start(?:_|$)`).test(exec.name)) return
    const run = interaction(result.value)
    if (run === null) return
    const sessionId = exec.agent.session.id
    void fetch(`${config.bridgeUrl.replace(/\/$/, '')}/v1/workflow-runs/${encodeURIComponent(run.runId)}/binding`, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ session_id: sessionId, dsh_url: config.dshUrl }), signal: exec.signal,
    }).then(response => {
      if (!response.ok) throw new Error(`LazyMind binding returned HTTP ${response.status}`)
      exec.agent?.session.append('lazymind-workflow/open', run, { ignorable: true })
    }).catch(error => ctx.logger.warn(`lazymind-workflow: ${String(error)}`))
  })
}
