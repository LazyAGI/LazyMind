import type { WorkflowControl } from './protocol'

export interface Pairing { connector_id: string; token: string }
export interface HostAction {
  id: string
  session_id: string
  kind: 'continue' | 'cancel'
  native_session_id: string
  /** Legacy wire field, ignored by delivery and runtime control. */
  binding_generation?: number
  execution_id?: string
  status: string
  created_at?: string
  consumed_at?: string
}
export interface ActionClaim { action: HostAction; dispatch_token?: string; control: WorkflowControl }
export type HostTransport = Pick<HostBridge, 'bind' | 'state' | 'actions' | 'action' | 'claim' | 'settle'>

export class BridgeError extends Error {
  constructor(readonly code: string, message: string, readonly status: number) { super(message) }
}

export function isHostAuthorizationError(error: unknown): error is BridgeError {
  // Core can reject an individual action or receipt with 403 (including HOST_AUTH_REQUIRED).
  // The Bridge reports a missing LazyMind session with 401 and pairing failures with explicit codes.
  return error instanceof BridgeError && (error.status === 401
    || ['HOST_PAIRING_MISSING', 'HOST_PAIRING_DISABLED', 'HOST_ACCOUNT_MISMATCH',
      'HOST_CREDENTIAL_MISMATCH', 'HOST_PAIRING_INVALID', 'HOST_PAIRING_UNREADABLE'].includes(error.code))
}

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
    const response = await fetch(this.base + path, {
      method: body === undefined ? 'GET' : 'POST', signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]),
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
}
