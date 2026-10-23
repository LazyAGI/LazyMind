import { execFile } from 'node:child_process'
import { isAbsolute } from 'node:path'

export interface ConnectionConfig { connectorConfig: string; command: string }
export interface Connection { provider: string; webUrl: string; bridgeUrl: string; pairingFile: string; serverName: string; revision: string }

/** Resolve through the connector so both runtimes share one configuration policy. */
export function resolveConnection(config: ConnectionConfig, signal: AbortSignal): Promise<Connection> {
  if (!isAbsolute(config.connectorConfig ?? '') || !isAbsolute(config.command ?? '')) {
    return Promise.reject(new Error('Reconnect DSH from LazyMind to configure its connection'))
  }
  return new Promise((resolve, reject) => {
    execFile(config.command, ['internal', 'connection', 'resolve'], {
      env: { ...process.env, LAZYMIND_CONNECTOR_CONFIG: config.connectorConfig },
      timeout: 10_000, maxBuffer: 64 * 1024, windowsHide: true, signal,
    }, (error, stdout) => {
      if (error) { reject(new Error(`LazyMind connection unavailable (${config.connectorConfig}); check its configuration or sign in again`)); return }
      try {
        const value = JSON.parse(stdout) as Connection
        if (value.provider !== 'deepseek-harness' || !value.webUrl || !value.bridgeUrl || !value.pairingFile || !value.serverName || !value.revision) throw new Error('Invalid DSH connection')
        resolve(value)
      } catch (error) { reject(error) }
    })
  })
}

/** Serial refresh: never keep an old controller after invalidation or overlap owners. */
export function watchConnection(
  resolve: (signal: AbortSignal) => Promise<Connection>,
  install: (connection: Connection) => Promise<() => Promise<void>>,
  unavailable: (error?: unknown) => void,
  interval = 3000,
): (() => Promise<void>) & { ready: Promise<void> } {
  const lifetime = new AbortController()
  let readyResolve!: () => void
  let readyReject!: (error: unknown) => void
  const ready = new Promise<void>((resolve, reject) => { readyResolve = resolve; readyReject = reject })
  let dispose: (() => Promise<void>) | undefined
  let fingerprint = ''
  let wake: (() => void) | undefined
  const stopCurrent = async (error?: unknown) => {
    const previous = dispose
    dispose = undefined
    fingerprint = ''
    unavailable(error)
    await previous?.()
  }
  const done = (async () => {
    try {
      while (!lifetime.signal.aborted) {
        try {
          const next = await resolve(lifetime.signal)
          if (lifetime.signal.aborted) break
          const key = JSON.stringify(next)
          if (key !== fingerprint) {
            await stopCurrent()
            if (lifetime.signal.aborted) break
            dispose = await install(next)
            fingerprint = key
            readyResolve()
          }
        } catch (error) { readyReject(error); await stopCurrent(error) }
        if (!lifetime.signal.aborted) await new Promise<void>(ready => {
          const timer = setTimeout(ready, interval)
          wake = () => { clearTimeout(timer); ready() }
        })
      }
    } finally { readyResolve(); await stopCurrent() }
  })()
  return Object.assign(async () => { lifetime.abort(); wake?.(); await done }, { ready })
}
