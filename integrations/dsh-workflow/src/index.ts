import type { Context } from '@deepseek-ai/cordis'
import { randomUUID } from 'node:crypto'
import { HostBridge, loadPairing } from './bridge'
import { installHost } from './host'
import { dispatcherLock } from './runtime-lock'
import { installPresentationConfig, type PresentationConfig } from './presentation-config'
import { resolveConnection, watchConnection, type ConnectionConfig } from './connection'
import { mcpReadiness } from './mcp-readiness'

export type Config = ConnectionConfig
export const inject = ['tools', 'agents', 'sessionController']

/** Pairing and live addresses are resolved together; inherited endpoint env is ignored. */
export async function apply(ctx: Context, config: Config): Promise<void> {
  let presentation: PresentationConfig | undefined
  let lastError = ""
  const readiness = new Map<string, ReturnType<typeof mcpReadiness>>()
  installPresentationConfig(ctx, () => presentation)
  const stop = watchConnection(signal => resolveConnection(config, signal), async current => {
    const pairing = await loadPairing(current.pairingFile)
    const instance = randomUUID()
    const release = await dispatcherLock(current.pairingFile, instance)
    try {
      let mcpReady = readiness.get(current.serverName)
      if (!mcpReady) { mcpReady = mcpReadiness(ctx, current.serverName); readiness.set(current.serverName, mcpReady) }
      const dispose = installHost(ctx, new HostBridge(current.bridgeUrl, pairing, async signal => ({
        'X-LazyMind-MCP-Ready': await mcpReady(signal) ? '1' : '0',
      })), current, instance)
      presentation = current
      lastError = ""
      return async () => { try { await dispose() } finally { await release() } }
    } catch (error) { await release(); throw error }
  }, error => {
    presentation = undefined
    const message = error instanceof Error ? error.message : ''
    if (message && message !== lastError) console.warn('LazyMind Workflow:', message)
    if (message) lastError = message
  })
  ctx.effect(() => stop)
  // Login and the local runtime may recover after DSH has booted. Keep the
  // resolver alive rather than making an optional connection fatal to DSH.
  await stop.ready.catch(() => {})
}
