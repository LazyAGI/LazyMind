import { CdpClient } from '../../../workflow-agent-core/node/cdp'

/** Loopback-only transport. Never use a page title as a conversation identity. */
export function selectWorkBuddyTarget(targets: any[]) {
  const pages = targets.filter(target => {
    if (target.type !== 'page' || target.title?.toLowerCase() !== 'workbuddy') return false
    try {
      const url = new URL(target.url)
      // WorkBuddy's main and independent settings windows share a title and
      // renderer entry. Match its own main-window discriminator, not list order.
      const appId = url.searchParams.get('windowAppId') ?? url.searchParams.get('windowKind') ?? 'main'
      return url.protocol === 'file:' && url.pathname.endsWith('/renderer/index.html') && appId === 'main'
    } catch { return false }
  })
  if (pages.length !== 1) throw new Error('Expected one WorkBuddy desktop target')
  return pages[0]
}

export class Cdp extends CdpClient {
  constructor(port: number) { super(port, selectWorkBuddyTarget) }
}
