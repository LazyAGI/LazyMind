import { AdmissionRejected, type RuntimeAdapter } from '../../../workflow-agent-core/src/adapter'
import type { ActionClaim } from '../../../workflow-agent-core/src/transport'
import { Cdp } from './cdp'
import { sdkOperation } from './sdk'
import { workbuddyVersionIssue } from './compatibility'
import type { Diagnostic, DiagnosticFields } from './diagnostics'

const sessionPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i
export class WorkBuddyAdapter implements RuntimeAdapter<string> {
  readonly cancellation = 'session' as const
  constructor(private cdp: Pick<Cdp, 'evaluate'>, private log: Diagnostic = () => {}) {}
  id(id: string) { return id }
  warn(message: string) {
    this.log('connector.warning', { kind: message.includes('reconnecting Bridge') ? 'bridge-unavailable' : 'delivery-pending', status: 'retrying' })
  }
  probe(signal: AbortSignal) { return this.call({ kind: 'probe' }, signal) }
  private call(input: unknown, signal: AbortSignal): Promise<any> {
    return this.cdp.evaluate(`(${sdkOperation.toString()})(${JSON.stringify(input)}, ${workbuddyVersionIssue.toString()})`, signal)
  }
  private async admission(kind: 'send' | 'cancel', sessionId: string, input: object, signal: AbortSignal, fields: DiagnosticFields) {
    const started = performance.now()
    try {
      if (signal.aborted) throw new AdmissionRejected('Aborted before sending')
      const result = await this.call({ kind, sessionId, ...input }, signal)
      if (result.missing || result.rejected) throw new AdmissionRejected(result.rejected ?? 'Bound session does not exist')
      if (!result.accepted) throw new Error('Host admission is unknown')
      this.log(`native.${kind}`, { ...fields, native_session_id: sessionId, status: 'accepted', duration_ms: performance.now() - started })
    } catch (error) {
      this.log(`native.${kind}`, { ...fields, native_session_id: sessionId,
        status: error instanceof AdmissionRejected ? 'failed' : 'unknown', duration_ms: performance.now() - started })
      throw error
    }
  }
  async ready(sessionId: string, signal: AbortSignal) {
    if (!sessionPattern.test(sessionId)) return true // Claim and report an invalid target as failed.
    try {
      const state = await this.call({ kind: 'status', sessionId }, signal)
      // Missing sessions must be claimed and reported failed, never rebound.
      return state.missing === true || state.state === 'idle'
    } catch { return false }
  }
  async resolve(id: string) {
    return sessionPattern.test(id) ? { agent: id } : { error: 'Native WorkBuddy session UUID required' }
  }
  async prompt(sessionId: string, input: { actionId: string; message: string }, signal: AbortSignal) {
    await this.admission('send', sessionId, { ...input,
      message: `[LazyMind action_id=${input.actionId}]\n${input.message}` }, signal, { action_id: input.actionId })
    // Live SDK acknowledgement only; no invented durable event sequence.
    return 0
  }
  async cancel(sessionId: string, fields: DiagnosticFields = {}) {
    if (!sessionPattern.test(sessionId)) throw new AdmissionRejected('Native WorkBuddy session UUID required')
    await this.admission('cancel', sessionId, {}, AbortSignal.timeout(10000), fields)
  }
  // Core's authenticated binding determines the target. WorkBuddy supports
  // session Stop, not an expected-request fence or per-turn ownership hooks.
  control(signal: AbortSignal) {
    return {
      cacheClaim(_claim: ActionClaim) {},
      cancelSession: async (sessionId: string, { action, control }: ActionClaim) => {
        if (action.kind !== 'cancel' || action.native_session_id !== sessionId || action.consumed_at
          || action.status !== 'dispatching' || control.session_id !== action.session_id || control.continuation !== 'stopped') {
          throw new AdmissionRejected('Cancellation no longer applies to this bound Workflow session')
        }
        signal.throwIfAborted()
        await this.cancel(sessionId, { action_id: action.id, run_id: action.session_id, execution_id: action.execution_id })
      },
    }
  }
  async identify(args: unknown, signal: AbortSignal) {
    const result = await this.call({ kind: 'identity', arguments: args }, signal)
    if (!Array.isArray(result.matches) || result.matches.length !== 1) throw new Error('Native workflow.start invocation is missing or ambiguous')
    return result.matches[0] as { sessionId: string; requestId: string; toolCallId: string }
  }
}
