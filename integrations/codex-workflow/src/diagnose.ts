import { parseArgs } from 'node:util'
import { AppServer, schemaVersion } from './app-server'

// Read-only: never resume, create a turn, interrupt, answer approvals or reload configuration.
const { values } = parseArgs({ options: {
  'app-server-url': { type: 'string' }, 'thread': { type: 'string' }, 'codex-home': { type: 'string' },
  'experimental': { type: 'boolean', default: false },
} })
async function main() {
  const endpoint = values['app-server-url'] ?? process.env.CODEX_APP_SERVER_WS_URL
  if (!endpoint) throw new Error('Provide --app-server-url or CODEX_APP_SERVER_WS_URL')
  const { client, identity } = await AppServer.connect(endpoint, { expectedHome: values['codex-home'], experimental: values.experimental })
  try {
    const result: Record<string, unknown> = { schemaVersion, identity }
    if (values.thread) {
      const thread = await client.readThread(values.thread)
      result.thread = { id: thread.id, status: thread.status,
        turns: thread.turns?.map(turn => ({ id: turn.id, status: turn.status })) }
    }
    const mcp = await client.request<{ data: Array<{ name: string; tools: Record<string, unknown>; runtimeStatus?: string; error?: unknown }>; nextCursor?: string }>('mcpServerStatus/list', {})
    result.mcp = { servers: mcp.data.map(server => ({ name: server.name, runtimeStatus: server.runtimeStatus,
      toolCount: Object.keys(server.tools ?? {}).length })), hasMore: !!mcp.nextCursor }
    console.log(JSON.stringify(result, null, 2))
  } finally { client.close() }
}
main().catch(() => { console.error('App Server read-only diagnosis failed; check endpoint, profile and server availability.'); process.exitCode = 1 })
