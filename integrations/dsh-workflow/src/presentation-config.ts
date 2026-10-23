import type { Context } from '@deepseek-ai/cordis'
import type {} from '@deepseek-ai/dsh-host-webserver'
import { object } from '../../workflow-agent-core/src/protocol'
import { presentationLink } from './events'

const CONFIG_KEY = '__LAZYMIND_WORKFLOW_PRESENTATION__'
export interface PresentationConfig { serverName: string; webUrl: string }

/** DSH loads browser plugins independently of the Node plugin's config. */
export function installPresentationConfig(ctx: Context, source: PresentationConfig | (() => PresentationConfig | undefined)): void {
  ctx.on('webserver/index-inject', table => {
    const config = typeof source === 'function' ? source() : source
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
