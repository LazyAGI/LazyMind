import type { Context } from '@deepseek-ai/cordis'
import { workflowOperation } from './events'

// Only the public, read-only resource request surface is needed. Keeping this
// structural avoids loading a second copy of DSH's SDK service at runtime.
interface ResourceRuntime {
  request(server: string, request: { method: 'resources/list' }, exec: { agent: undefined; signal: AbortSignal }): Promise<unknown>
}
declare module '@deepseek-ai/cordis' {
  interface Context { mcpResources: ResourceRuntime }
}

/** A cached tool schema alone survives MCP disconnection in DSH. The resource
 * provider checks the current MCP client before answering even when that server
 * has no resource capability, so this also verifies the live connection without
 * invoking a Workflow tool or reading any resource contents. */
export function mcpReadiness(ctx: Context, serverName: string) {
  let resources: ResourceRuntime | undefined
  ctx.inject(['mcpResources'], injected => {
    resources = injected.mcpResources
    injected.effect(() => () => { resources = undefined })
  })
  const toolsReady = () => {
    const operations = new Set(ctx.tools.schemas().filter(schema => ctx.tools.get(schema.name))
      .map(schema => workflowOperation(schema.name, serverName)))
    return ['start', 'state', 'step_begin'].every(operation => operations.has(operation))
  }
  return async (signal: AbortSignal): Promise<boolean> => {
    const current = resources
    if (!current || !toolsReady() || signal.aborted) return false
    const bounded = new AbortController()
    let interrupt!: () => void
    const interrupted = new Promise<never>((_resolve, reject) => {
      interrupt = () => { bounded.abort(); reject(new Error('MCP readiness check interrupted')) }
    })
    const timer = setTimeout(interrupt, 2000)
    signal.addEventListener('abort', interrupt, { once: true })
    try {
      // The bound holds even if an older native provider ignores AbortSignal.
      await Promise.race([interrupted,
        current.request(serverName, { method: 'resources/list' }, { agent: undefined, signal: bounded.signal })])
      return !signal.aborted && current === resources && toolsReady()
    } catch { return false }
    finally { clearTimeout(timer); signal.removeEventListener('abort', interrupt); bounded.abort() }
  }
}
