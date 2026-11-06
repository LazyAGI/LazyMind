import type { Context } from '@deepseek-ai/cordis'
import type {} from '@deepseek-ai/dsh-host-webserver'
import { object } from '../../workflow-agent-core/src/protocol'
import { presentationLink } from './events'

export const PRESENTATION_CONFIG_PATH = '/lazymind-workflow/config'
const CONFIG_KEY = '__LAZYMIND_WORKFLOW_PRESENTATION__'
export interface PresentationConfig { serverName: string; webUrl: string }

/** DSH loads browser plugins independently of the Node plugin's config. */
export function installPresentationConfig(ctx: Context, source: PresentationConfig | (() => PresentationConfig | undefined)): void {
  const current = () => {
    const config = typeof source === 'function' ? source() : source
    return config && presentationLink('config-check', config.webUrl)
      ? { serverName: config.serverName, webUrl: config.webUrl } : undefined
  }
  // Only public presentation data crosses this unauthenticated DSH route.
  ctx.inject(['webServer'], scope => {
    scope.effect(() => scope.webServer.register({ kind: 'exact', path: PRESENTATION_CONFIG_PATH,
      handler(req, res) {
        res.setHeader('Cache-Control', 'no-store')
        res.setHeader('Content-Type', 'application/json')
        if (req.method !== 'GET') { res.statusCode = 405; res.end(); return }
        res.end(JSON.stringify({ config: current() ?? null }))
      },
    }))
  })
  ctx.on('webserver/index-inject', table => {
    const config = current()
    if (!config) return
    table.push({ kind: 'global', name: CONFIG_KEY, value: {
      serverName: config.serverName, webUrl: config.webUrl,
    } })
  })
}

export function readPresentationConfig(scope: unknown): PresentationConfig {
  const value = object(object(scope)?.[CONFIG_KEY])
  if (typeof value?.serverName !== 'string' || !value.serverName
    || typeof value.webUrl !== 'string' || !presentationLink('config-check', value.webUrl)) {
    throw new Error('Reconnect LazyMind Workflow and reload DSH to load its panel configuration')
  }
  return { serverName: value.serverName, webUrl: value.webUrl }
}

/** Optional connections must not prevent the rest of the DSH client from booting. */
export function observePresentationConfig(
  scope: unknown,
  update: (config: PresentationConfig | undefined) => void,
  request: typeof fetch = fetch,
  interval = 3000,
): () => void {
  const lifetime = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  try { update(readPresentationConfig(scope)) } catch { update(undefined) }
  const poll = async () => {
    try {
      const response = await request(PRESENTATION_CONFIG_PATH, { signal: lifetime.signal, cache: 'no-store' })
      if (response.ok) {
        const { config } = await response.json() as { config: unknown }
        if (!lifetime.signal.aborted) {
          if (config === null) update(undefined)
          else update(readPresentationConfig({ [CONFIG_KEY]: config }))
        }
      }
    } catch { /* A transient transport failure preserves the last public config. */ }
    if (!lifetime.signal.aborted) timer = setTimeout(() => { void poll() }, interval)
  }
  // Electron's file carrier receives its initial injection via the host boot
  // payload; relative HTTP polling is used only by the browser carrier.
  if (typeof location !== 'undefined' && /^https?:$/.test(location.protocol)) void poll()
  return () => { lifetime.abort(); clearTimeout(timer) }
}
