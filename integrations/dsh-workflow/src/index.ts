import type { Context } from '@deepseek-ai/cordis'
import { randomUUID } from 'node:crypto'
import { HostBridge, loadPairing } from './bridge'
import { installHost } from './host'
import { dispatcherLock } from './runtime-lock'

export interface Config {
  bridgeUrl: string
  webUrl: string
  pairingFile: string
  serverName: string
}
export const inject = ['tools', 'agents', 'sessionController']

/** Install only public DSH extensions. The pairing secret stays in a private host file. */
export async function apply(ctx: Context, config: Config): Promise<void> {
  if (!config.webUrl || !config.serverName) throw new Error('Reconnect DSH from LazyMind to configure the workflow bundle')
  const pairing = await loadPairing(config.pairingFile)
  const instance = randomUUID()
  const release = await dispatcherLock(config.pairingFile, instance)
  try {
    const dispose = installHost(ctx, new HostBridge(config.bridgeUrl, pairing), config, instance)
    ctx.effect(() => async () => { try { await dispose() } finally { await release() } })
  } catch (error) { await release(); throw error }
}
