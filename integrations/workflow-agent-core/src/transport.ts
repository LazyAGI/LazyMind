import type { WorkflowControl } from './protocol'
import type { LookupBinding } from './adapter'
import { RequestRetry } from './request-retry'
import { BridgeError, isHostAuthorizationError } from './transport-errors'
export { BridgeError, isHostAuthorizationError } from './transport-errors'

export interface Pairing { connector_id: string; token: string; runtime_revision?: string }
export interface HostAction {
  id: string
  session_id: string
  kind: 'continue' | 'cancel' | 'artifact_edit' | 'model_generate'
  native_session_id: string
  connector_id?: string
  /** Legacy wire field, ignored by delivery and runtime control. */
  binding_generation?: number
  execution_id?: string
  status: string
  created_at?: string
  consumed_at?: string
}
export interface ActionClaim { action: HostAction; dispatch_token?: string; control: WorkflowControl }
export type HostTransport = Pick<HostBridge, 'bind' | 'state' | 'actions' | 'action' | 'claim' | 'settle'>

export class HostBridge {
  private readonly base: string
  constructor(url: string, private readonly pairing: Pairing,
    private readonly actionHeaders?: (signal: AbortSignal) => Record<string, string> | Promise<Record<string, string>>) {
    const parsed = new URL(url)
    if (!['http:', 'https:'].includes(parsed.protocol) || !['localhost', '127.0.0.1', '[::1]'].includes(parsed.hostname)
      || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error('Workflow Bridge must be a loopback URL without credentials')
    this.base = parsed.href.replace(/\/$/, '') + '/v1/workflow-host'
  }

  private async request<T>(path: string, signal: AbortSignal, body?: unknown, headers?: Record<string, string>): Promise<T> {
    return this.requestURL<T>(this.base + path, signal, body, headers)
  }

  private async requestURL<T>(url: string | URL, signal: AbortSignal, body?: unknown, headers?: Record<string, string>): Promise<T> {
    const response = await fetch(url, {
      method: body === undefined ? 'GET' : 'POST', redirect: 'error', signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]),
      headers: { ...headers, Authorization: `Bearer ${this.pairing.token}`, 'X-LazyMind-Connector-Id': this.pairing.connector_id,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
    const decoded = await response.json().catch(() => {
      // Preserve authentication/transport status even when an upstream gateway
      // sends HTML or an empty response. Never include that body in diagnostics.
      if (!response.ok) return {}
      throw new Error('Invalid Workflow Bridge response')
    })
    const value = decoded && typeof decoded === 'object' && !Array.isArray(decoded) ? decoded : {}
    if (!response.ok) throw new BridgeError(typeof value.code === 'string' ? value.code : 'BRIDGE_ERROR', typeof value.error === 'string' ? value.error : 'Workflow Bridge request failed', response.status)
    return value as T
  }

  async bind(runId: string, driver: string, signal: AbortSignal): Promise<WorkflowControl> {
    return (await this.request<{ control: WorkflowControl }>('/bind', signal, {
      run_id: runId, driver_session_id: driver,
    })).control
  }
  async state(runId: string, signal: AbortSignal): Promise<WorkflowControl> {
    return (await this.request<{ control: WorkflowControl }>(`/runs/${encodeURIComponent(runId)}/control`, signal)).control
  }
  async actions(after: string, signal: AbortSignal): Promise<{ actions: HostAction[]; next_page_token?: string }> {
    return this.request(`/actions${after ? `?after=${encodeURIComponent(after)}` : ''}`, signal, undefined, await this.actionHeaders?.(signal))
  }
  async action(id: string, signal: AbortSignal): Promise<ActionClaim> {
    return this.request(`/actions/${encodeURIComponent(id)}`, signal)
  }
  async claim(id: string, instanceId: string, signal: AbortSignal): Promise<ActionClaim> {
    return this.request(`/actions/${encodeURIComponent(id)}/claim`, signal, { instance_id: instanceId })
  }
  async settle(id: string, instanceId: string, token: string, status: string, seq: number, error: string, signal: AbortSignal): Promise<void> {
    await this.request(`/actions/${encodeURIComponent(id)}/settle`, signal, {
      instance_id: instanceId, dispatch_token: token, status, native_event_seq: seq, error,
    })
  }

  lookupBinding(provider: 'codex' | 'workbuddy'): LookupBinding {
    // Presentation supplies a new signal for each view, including when returning
    // to the same chat. Old requests cannot touch the new view's retry state.
    const retries = new WeakMap<AbortSignal, RequestRetry>()
    return async (chatId, signal) => {
      signal.throwIfAborted()
      let retry = retries.get(signal)
      if (!retry) { retry = new RequestRetry(); retries.set(signal, retry) }
      try {
        const url = new URL(`/v1/${provider}/presentation`, this.base)
        url.searchParams.set('session_id', chatId)
        const response = await this.requestURL<{ session_id: unknown; run_id: unknown }>(url, signal)
        signal.throwIfAborted()
        if (response.session_id !== chatId || typeof response.run_id !== 'string'
          || response.run_id !== '' && !/^[a-zA-Z0-9_-]{1,256}$/.test(response.run_id)) throw new Error('Invalid presentation response')
        retry.succeeded()
        return response.run_id ? { status: 'bound', runId: response.run_id } : { status: 'unbound' }
      } catch (error) {
        signal.throwIfAborted()
        if (error instanceof BridgeError && error.status === 403 && !isHostAuthorizationError(error)) {
          retry.succeeded()
          return { status: 'access-denied' }
        }
        const { authorization } = retry.failed(error)
        return { status: authorization ? 'access-denied' : 'unavailable', retryAt: retry.retryAt }
      }
    }
  }
}
