import type { Context } from '@deepseek-ai/cordis'
import { randomUUID } from 'node:crypto'
import { HostBridge, loadPairing } from './bridge'
import { installHost } from './host'
import { dispatcherLock } from './runtime-lock'
import { installPresentationConfig, type PresentationConfig } from './presentation-config'
import { resolveConnection, watchConnection, type ConnectionConfig } from './connection'
import { mcpReadiness } from './mcp-readiness'
import { connectorBuild } from './connector-build'
import { installBindingRPC } from './binding-rpc'
import type { BindingResult, LookupBinding } from '../../workflow-agent-core/src/adapter'

export { connectorBuild }
const processStartedAt = Math.round(Date.now() - process.uptime() * 1000)
const processInstance = randomUUID()

export type Config = ConnectionConfig
export const inject = ['tools', 'agents', 'sessionController']

/** Pairing and live addresses are resolved together; inherited endpoint env is ignored. */
export async function apply(ctx: Context, config: Config): Promise<void> {
  let presentation: PresentationConfig | undefined
  let binding: { lookup: LookupBinding; config: PresentationConfig } | undefined
  let panel: { status: BindingResult['status'] | 'waiting-session'; at: number } | undefined
  let clientBuild: string | undefined
  let lastError = ""
  const readiness = new Map<string, ReturnType<typeof mcpReadiness>>()
  installPresentationConfig(ctx, () => presentation)
  installBindingRPC(ctx, () => binding, (status, build) => {
    if (status !== 'waiting-session') panel = { status, at: Date.now() }
    if (build) clientBuild = build
  })
  const stop = watchConnection(signal => resolveConnection(config, signal), async current => {
    const pairing = await loadPairing(current.pairingFile)
    const instance = randomUUID()
    const release = await dispatcherLock(current.pairingFile, instance)
    try {
      let mcpReady = readiness.get(current.serverName)
      if (!mcpReady) { mcpReady = mcpReadiness(ctx, current.serverName); readiness.set(current.serverName, mcpReady) }
      const host = new HostBridge(current.bridgeUrl, pairing, async signal => ({
        'X-LazyMind-MCP-Ready': await mcpReady(signal) ? '1' : '0',
        'X-LazyMind-DSH-Build': connectorBuild,
        'X-LazyMind-DSH-Process': processInstance,
        'X-LazyMind-DSH-Process-Started-At': String(processStartedAt),
        'X-LazyMind-DSH-Panel': panel && Date.now() - panel.at < 20_000 ? panel.status : 'waiting-session',
        ...(clientBuild ? { 'X-LazyMind-DSH-Client-Build': clientBuild } : {}),
      }))
      const dispose = installHost(ctx, host, current, instance)
      presentation = { serverName: current.serverName, webUrl: current.webUrl }
      binding = { lookup: host.lookupBinding('deepseek-harness'), config: presentation }
      lastError = ""
      return async () => { try { await dispose() } finally { await release() } }
    } catch (error) { await release(); throw error }
  }, error => {
    presentation = undefined
    binding = undefined
    panel = undefined
    clientBuild = undefined
    const message = error instanceof Error ? error.message : ''
    if (message && message !== lastError) console.warn('LazyMind Workflow:', message)
    if (message) lastError = message
  })
  ctx.effect(() => stop)
  // Login and the local runtime may recover after DSH has booted. Keep the
  // resolver alive rather than making an optional connection fatal to DSH.
  await stop.ready.catch(() => {})
}
