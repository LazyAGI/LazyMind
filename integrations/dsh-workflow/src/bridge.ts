import { readFile, stat } from 'node:fs/promises'
import { object, type WorkflowControl } from './protocol'

export interface Pairing { connector_id: string; token: string; enabled: boolean }
export interface HostAction {
  id: string
  session_id: string
  kind: 'continue' | 'cancel'
  native_session_id: string
  binding_generation: number
  execution_id?: string
  status: string
  consumed_at?: string
}
export interface ActionClaim { action: HostAction; dispatch_token?: string; control: WorkflowControl }
export type HostTransport = Pick<HostBridge, 'bind' | 'state' | 'actions' | 'action' | 'claim' | 'settle'>

export class BridgeError extends Error {
  constructor(readonly code: string, message: string, readonly status: number) { super(message) }
}

/** The only credential loaded is LazyMind's scoped local pairing, never DSH's signing key. */
export async function loadPairing(path: string): Promise<Pairing> {
  if (!path) throw new Error('Reconnect DeepSeek Harness from LazyMind to configure the workflow bundle')
  const info = await stat(path)
  if (!info.isFile() || (process.platform !== 'win32' && (info.mode & 0o077) !== 0)) throw new Error('Workflow pairing must be a private file')
  const value = object(JSON.parse(await readFile(path, 'utf8')))
  if (!value || typeof value.connector_id !== 'string' || typeof value.token !== 'string' || value.token.length !== 64 || value.enabled !== true) {
    throw new Error('Workflow pairing is unavailable; reconnect DeepSeek Harness from LazyMind')
  }
  return { connector_id: value.connector_id, token: value.token, enabled: true }
}

export class HostBridge {
  private readonly base: string
  constructor(url: string, private readonly pairing: Pairing) {
    const parsed = new URL(url)
    if (!['http:', 'https:'].includes(parsed.protocol) || !['localhost', '127.0.0.1', '[::1]'].includes(parsed.hostname)
      || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error('Workflow Bridge must be a loopback URL without credentials')
    this.base = parsed.href.replace(/\/$/, '') + '/v1/workflow-host'
  }

  private async request<T>(path: string, signal: AbortSignal, body?: unknown): Promise<T> {
    const response = await fetch(this.base + path, {
      method: body === undefined ? 'GET' : 'POST', signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]),
      headers: { Authorization: `Bearer ${this.pairing.token}`, 'X-LazyMind-Connector-Id': this.pairing.connector_id,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
    const value = await response.json()
    if (!response.ok) throw new BridgeError(typeof value.code === 'string' ? value.code : 'BRIDGE_ERROR', typeof value.error === 'string' ? value.error : 'Workflow Bridge request failed', response.status)
    return value as T
  }

  async bind(runId: string, driver: string, executor: string, signal: AbortSignal): Promise<WorkflowControl> {
    return (await this.request<{ control: WorkflowControl }>('/bind', signal, {
      run_id: runId, driver_session_id: driver, executor_session_id: executor,
    })).control
  }
  async state(runId: string, signal: AbortSignal): Promise<WorkflowControl> {
    return (await this.request<{ control: WorkflowControl }>(`/runs/${encodeURIComponent(runId)}/control`, signal)).control
  }
  async actions(after: string, signal: AbortSignal): Promise<{ actions: HostAction[]; next_page_token?: string }> {
    return this.request(`/actions${after ? `?after=${encodeURIComponent(after)}` : ''}`, signal)
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
