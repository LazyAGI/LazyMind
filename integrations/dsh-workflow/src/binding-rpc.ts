import type { Context } from '@deepseek-ai/cordis'
import type {} from '@deepseek-ai/dsh-api-session-controller'
import type { BindingResult, LookupBinding } from '../../workflow-agent-core/src/adapter'
import { object } from '../../workflow-agent-core/src/protocol'
import type { PresentationConfig } from './presentation-config'
import { connectorBuild } from './connector-build'

const channel = '/api'
const endpoint = 'lazymind/workflow-binding'
const identifier = /^[A-Za-z0-9_-]{1,256}$/
const buildIdentifier = /^[0-9][A-Za-z0-9.-]{0,63}\+[a-f0-9]{16}$/

interface HostConnection {
  fetch?: { register(route: {
    path: string; methods: ['POST']; requestBody: 'buffered'; fetch(request: Request): Promise<Response>
  }): () => Promise<void> }
}

interface ClientConnection {
  rpc?: { call(channel: string, endpoint: string, payload: unknown, signal: AbortSignal): Promise<unknown> }
}

interface BindingSource { lookup: LookupBinding; config: PresentationConfig }

export function installBindingRPC(
  ctx: Context, source: () => BindingSource | undefined,
  report: (status: BindingResult['status'] | 'waiting-session', clientBuild?: string) => void,
): void {
  ctx.inject(['connection', 'sessionController'], scope => {
    const connection = scope.get('connection') as HostConnection | undefined
    connection?.fetch?.register({
      path: channel + '/' + endpoint, methods: ['POST'], requestBody: 'buffered',
      async fetch(request) {
        const body = object(await request.json().catch(() => undefined))
        const payload = object(body?.payload)
        const sessionId = payload?.sessionId
        const clientBuild = typeof payload?.clientBuild === 'string' && buildIdentifier.test(payload.clientBuild)
          ? payload.clientBuild : undefined
        if (body?.type !== 'client-request' || body.method !== endpoint || typeof body.rpcId !== 'string'
          || !identifier.test(body.rpcId) || sessionId !== null && (typeof sessionId !== 'string' || !identifier.test(sessionId))) {
          return new Response(null, { status: 400 })
        }
        const response = (binding: BindingResult, config: PresentationConfig | null) => {
          if (!request.signal.aborted) report(sessionId === null ? 'waiting-session' : binding.status, clientBuild)
          return Response.json({
            type: 'server-response', rpcId: body.rpcId, result: { ok: true, value: {
              sessionId, binding, config: config && { serverName: config.serverName, webUrl: config.webUrl },
            } },
          }, { headers: { 'Cache-Control': 'no-store' } })
        }
        const active = source()
        if (!active) return response({ status: 'access-denied' }, null)
        if (sessionId === null) return response({ status: 'unbound' }, active.config)
        if (clientBuild && clientBuild !== connectorBuild) return response({ status: 'unavailable' }, active.config)
        try {
          // The shared Connection API applies the carrier's authorization. The
          // host still limits the lookup to a native chat in this profile.
          const { items } = await scope.sessionController.list({}, request.signal)
          if (!items.some(item => item.sessionId === sessionId)) return response({ status: 'unavailable' }, active.config)
          const binding = await active.lookup(sessionId, request.signal)
          if (active !== source()) return response({ status: 'access-denied' }, null)
          return response(binding, active.config)
        } catch {
          return active === source() ? response({ status: 'unavailable' }, active.config) : response({ status: 'access-denied' }, null)
        }
      },
    })
  })
}

function callBinding(ctx: Context, sessionId: string | null, signal: AbortSignal): Promise<unknown> | undefined {
  const connection = ctx.get?.('connection') as ClientConnection | undefined
  return connection?.rpc?.call(channel, endpoint, { sessionId, clientBuild: connectorBuild }, signal)
}

export function reportClientBuild(ctx: Context, signal: AbortSignal): void {
  void callBinding(ctx, null, signal)?.catch(() => {})
}

export function clientBindingLookup(
  ctx: Context, updateConfig: (value: unknown) => void,
): LookupBinding {
  return async (sessionId, signal) => {
    try {
      const result = object(await callBinding(ctx, sessionId, signal))
      signal.throwIfAborted()
      const value = object(result?.value)
      if (result?.ok !== true || value?.sessionId !== sessionId) return { status: 'unavailable' }
      updateConfig(value.config)
      const binding = object(value.binding)
      switch (binding?.status) {
        case 'bound':
          if (typeof binding.runId === 'string' && identifier.test(binding.runId)) return { status: 'bound', runId: binding.runId }
          return { status: 'unavailable' }
        case 'unbound':
        case 'access-denied':
        case 'unavailable':
          return { status: binding.status, ...(typeof binding.retryAt === 'number' ? { retryAt: binding.retryAt } : {}) }
        default:
          return { status: 'unavailable' }
      }
    } catch (error) {
      signal.throwIfAborted()
      return error instanceof Error && /HTTP (401|403)$/.test(error.message) ? { status: 'access-denied' } : { status: 'unavailable' }
    }
  }
}
