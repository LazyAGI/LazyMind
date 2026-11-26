import { RequestRetry, waitUntil } from '../../../workflow-agent-core/src/request-retry'
import { BridgeError, HostBridge, isHostAuthorizationError, type Pairing } from '../../../workflow-agent-core/src/transport'
import type { WorkBuddyAdapter } from './adapter'
import type { Diagnostic } from './diagnostics'

interface IdentityRequest {
  id: string
  arguments: unknown
}

export function workBuddyConnection(bridgeURL: string, pairing: Pairing, adapter: Pick<WorkBuddyAdapter, 'identify'>, log: Diagnostic, signal: AbortSignal) {
  const bridge = new HostBridge(bridgeURL, pairing)
  const retry = new RequestRetry()
  let lastIssue = ''

  async function request<T>(route: string, body?: unknown): Promise<T> {
    const response = await fetch(new URL('/v1/workbuddy/' + route, bridgeURL), {
      method: body === undefined ? 'GET' : 'POST', redirect: 'error',
      signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]),
      headers: { Authorization: `Bearer ${pairing.token}`, 'X-LazyMind-Connector-Id': pairing.connector_id,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
    const value = await response.json().catch(() => {
      if (!response.ok) return {}
      throw new Error('Invalid WorkBuddy bridge response')
    })
    if (!response.ok) throw new BridgeError(typeof value?.code === 'string' ? value.code : 'BRIDGE_ERROR', 'WorkBuddy bridge unavailable', response.status)
    signal.throwIfAborted()
    return value
  }

  async function identify(item: IdentityRequest) {
    const started = performance.now()
    let identity: Awaited<ReturnType<WorkBuddyAdapter['identify']>>
    try { identity = await adapter.identify(item.arguments, signal) }
    catch { return } // Native tool visibility can lag dispatch. Only retry discovery.
    signal.throwIfAborted()
    try { await request('identity-receipts', { id: item.id, ...identity }) }
    catch (error) {
      // A caller may have timed out while the renderer was identifying it.
      if (error instanceof BridgeError && error.status < 500 && !isHostAuthorizationError(error)) return
      throw error
    }
    log('identity.resolved', { identity_request_id: item.id, native_session_id: identity.sessionId,
      native_request_id: identity.requestId, tool_call_id: identity.toolCallId, status: 'resolved', duration_ms: performance.now() - started })
  }

  async function identities() {
    while (!signal.aborted) {
      let issue = ''
      try {
        const page = await request<{ requests: IdentityRequest[] }>('identity-requests')
        for (const item of page.requests) await identify(item)
        if (signal.aborted) return
        retry.succeeded()
      } catch (error) {
        if (signal.aborted) return
        issue = retry.failed(error).issue
      }
      if (lastIssue !== issue) {
        log('connector.warning', { kind: 'identity-bridge', status: issue || 'connected' })
        lastIssue = issue
      }
      try { await waitUntil(retry.retryAt || Date.now() + 250, signal) } catch { break }
    }
  }

  return { bridge, identities, lookupBinding: bridge.lookupBinding('workbuddy') }
}
