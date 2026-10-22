import type { Context } from '@deepseek-ai/cordis'
import { randomUUID } from 'node:crypto'
import { HostBridge, loadPairing } from './bridge'
import { installHost } from './host'
import { dispatcherLock } from './runtime-lock'
import { installPresentationConfig, type PresentationConfig } from './presentation-config'
import { resolveConnection, watchConnection, type ConnectionConfig } from './connection'

export type Config = ConnectionConfig
export const inject = ['tools', 'agents', 'sessionController']

/** Pairing and live addresses are resolved together; inherited endpoint env is ignored. */
export async function apply(ctx: Context, config: Config): Promise<void> {
  let presentation: PresentationConfig | undefined
  let lastError = ""
  installPresentationConfig(ctx, () => presentation)
  const stop = watchConnection(signal => resolveConnection(config, signal), async current => {
    const pairing = await loadPairing(current.pairingFile)
    const instance = randomUUID()
    const release = await dispatcherLock(current.pairingFile, instance)
    try {
      const dispose = installHost(ctx, new HostBridge(current.bridgeUrl, pairing), current, instance)
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
  try { await stop.ready } catch (error) { await stop(); throw error }
}
