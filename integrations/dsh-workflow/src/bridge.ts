import { readFile, stat } from 'node:fs/promises'
import { object } from '../../workflow-agent-core/src/protocol'
import { HostBridge, type Pairing } from '../../workflow-agent-core/src/transport'
export * from '../../workflow-agent-core/src/transport'

export interface ChatRun {
  run_id: string
  action: 'start' | 'resume' | 'regenerate' | 'recover' | 'finalize'
  provider: string
  host_id: string
  provider_thread_id?: string
  prompt: string
  lease_token: string
}
export interface ChatEvent {
  type: 'thread_started' | 'message' | 'turn_completed' | 'completed' | 'failed'
  provider_thread_id?: string
  text?: string
  error?: string
}

export class DshBridge extends HostBridge {
  claimChat(status: {ready: boolean; unavailable_reason?: string}, signal: AbortSignal) {
    return this.request<{run: ChatRun | null}>('/chat/claim', signal, status)
  }
  heartbeatChat(run: ChatRun, signal: AbortSignal) {
    return this.request<{stop_requested: boolean}>(`/chat/${encodeURIComponent(run.run_id)}/heartbeat`, signal, { lease_token: run.lease_token })
  }
  async publishChat(run: ChatRun, id: string, event: ChatEvent, signal: AbortSignal) {
    await this.request(`/chat/${encodeURIComponent(run.run_id)}/event`, signal, { lease_token: run.lease_token, event_id: id, ...event })
  }
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
  return { connector_id: value.connector_id, token: value.token }
}
