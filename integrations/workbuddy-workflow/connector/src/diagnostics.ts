import type { HostTransport } from '../../../workflow-agent-core/src/transport'

type Event = 'identity.resolved' | 'action.claim' | 'action.settle' | 'native.send' | 'native.cancel' | 'presentation' | 'connector.warning'
export interface DiagnosticFields {
  action_id?: string; run_id?: string; native_session_id?: string; execution_id?: string
  identity_request_id?: string; native_request_id?: string; tool_call_id?: string
  kind?: string; status?: string; page_status?: string; duration_ms?: number
}
export type Diagnostic = (event: Event, fields: DiagnosticFields) => void

// Explicit allowlist: no messages, arguments, handles, tokens or raw errors.
// Logging must never change whether an already admitted action is settled.
export function createDiagnostics(connectorId: string, sink: (line: string) => void = console.log): Diagnostic {
  return (event, fields) => {
    const data: Record<string, string | number> = { time: new Date().toISOString(), component: 'workbuddy', connector_id: connectorId, event }
    for (const key of ['action_id', 'run_id', 'native_session_id', 'execution_id', 'identity_request_id',
      'native_request_id', 'tool_call_id', 'kind', 'status', 'page_status'] as const) {
      if (typeof fields[key] === 'string') data[key] = fields[key]!.slice(0, 256)
    }
    if (Number.isFinite(fields.duration_ms)) data.duration_ms = Math.max(0, Math.round(fields.duration_ms!))
    try { sink(JSON.stringify(data)) } catch { /* Diagnostics cannot affect delivery. */ }
  }
}

export function diagnosedTransport(bridge: HostTransport, log: Diagnostic): HostTransport {
  return {
    bind: (...args) => bridge.bind(...args), state: (...args) => bridge.state(...args),
    actions: (...args) => bridge.actions(...args), action: (...args) => bridge.action(...args),
    async claim(...args) {
      const started = performance.now()
      try {
        const result = await bridge.claim(...args), a = result.action
        log('action.claim', { action_id: a.id, run_id: a.session_id, native_session_id: a.native_session_id,
          execution_id: a.execution_id, kind: a.kind, status: a.status, duration_ms: performance.now() - started })
        return result
      } catch (error) {
        log('action.claim', { action_id: args[0], status: 'unconfirmed', duration_ms: performance.now() - started })
        throw error
      }
    },
    async settle(...args) {
      const started = performance.now()
      try {
        await bridge.settle(...args)
        log('action.settle', { action_id: args[0], status: args[3], duration_ms: performance.now() - started })
      } catch (error) {
        log('action.settle', { action_id: args[0], status: 'unconfirmed', duration_ms: performance.now() - started })
        throw error
      }
    },
  }
}
