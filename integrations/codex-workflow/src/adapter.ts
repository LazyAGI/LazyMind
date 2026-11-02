import { randomUUID } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { AdmissionRejected, type RuntimeAdapter } from '../../workflow-agent-core/src/adapter'
import type { ActionClaim, HostTransport } from '../../workflow-agent-core/src/transport'
import { CodexNative } from './native'
import { NativeReceipts } from './receipts'
import { workflowTurnEvidence } from './ownership'

export class CodexAdapter implements RuntimeAdapter<string> {
  readonly cancellation = 'session' as const
  private claims = new Map<string, ActionClaim>()
  readonly host = {
    cacheClaim: (claim: ActionClaim) => { this.claims.set(claim.action.id, claim) },
    cancelSession: (thread: string, claim: ActionClaim) => this.cancelOwned(thread, claim),
  }
  constructor(private native: Pick<CodexNative, 'read' | 'start' | 'interrupt'>,
    private receipts: NativeReceipts, private bridge: HostTransport, private signal: AbortSignal,
    private recovery?: { available(): void; unavailable(thread?: string): Promise<void> }) {}
  id(thread: string) { return thread }
  warn(message: string) { console.warn(message) }
  async resolve(thread: string) {
    return /^[a-f0-9-]{36}$/i.test(thread) ? { agent: thread } : { error: 'A Codex thread UUID is required' }
  }
  async ready(thread: string, signal: AbortSignal) {
    try {
      const state = await this.native.read(thread, signal)
      this.recovery?.available()
      return state.ready !== false && !state.active
    } catch {
      if (!signal.aborted) await this.recovery?.unavailable(thread)
      return false
    }
  }
  private validate(thread: string, claim: ActionClaim, kind: 'continue' | 'cancel') {
    const { action, control } = claim
    if (action.kind !== kind || action.status !== 'dispatching' || action.consumed_at || action.native_session_id !== thread
      || control.session_id !== action.session_id || !control.binding?.bound || control.binding.driver_session_id !== thread
      || control.binding.provider !== 'codex') throw new AdmissionRejected('Notification does not match the bound Codex chat')
    if (kind === 'cancel' && control.continuation !== 'stopped') throw new AdmissionRejected('Workflow is no longer stopped')
  }
  async prompt(thread: string, input: { actionId: string; message: string }, signal: AbortSignal) {
    const claim = this.claims.get(input.actionId)
    this.claims.delete(input.actionId)
    if (!claim) throw new AdmissionRejected('Workflow notification has not been claimed')
    this.validate(thread, claim, 'continue')
    if (await this.receipts.read(input.actionId)) throw new Error('Message was already attempted; do not resend')
    const before = await this.native.read(thread, signal)
    if (before.active) throw new AdmissionRejected('Codex chat became busy; no message was sent')
    signal.throwIfAborted()
    const receipt = { actionId: input.actionId, runId: claim.action.session_id, threadId: thread,
      kind: 'continue' as const, clientId: randomUUID(), status: 'sending' as 'sending' | 'accepted' | 'unknown' | 'rejected', turnId: undefined as string | undefined }
    await this.receipts.write(receipt)
    try {
      const message = `[LazyMind action_id=${input.actionId}]\n${input.message}`
      let result = await this.native.start(thread, receipt.clientId, message, signal)
      // Retry only an explicit, definitely unsent readiness rejection. A lost
      // response must never cause another send, and retries keep the same ID.
      for (let retries = 0; result?.rejected && result.retryable === true && retries < 2; retries++) {
        receipt.status = 'rejected'
        await this.receipts.write(receipt)
        await delay(250, undefined, { signal })
        this.validate(thread, await this.bridge.action(input.actionId, signal), 'continue')
        signal.throwIfAborted()
        receipt.status = 'sending'
        await this.receipts.write(receipt)
        result = await this.native.start(thread, receipt.clientId, message, signal)
      }
      if (result?.rejected) { receipt.status = 'rejected'; throw new AdmissionRejected(result.rejected) }
      if (!result?.accepted || !result.turnId || before.turns.some(turn => turn.id === result.turnId)) throw new Error('Native message receipt is uncertain')
      receipt.turnId = result.turnId
      receipt.status = 'accepted'
      await this.receipts.write(receipt)
      return 0
    } catch (error) { if (receipt.status !== 'rejected') receipt.status = 'unknown'; await this.receipts.write(receipt); throw error }
  }
  cancel(): never { throw new AdmissionRejected('Stop requires a Workflow-owned turn') }
  private async owns(evidence: ReturnType<typeof workflowTurnEvidence>, turnId: string, run: string, thread: string) {
    const { runId: owner, inputs } = evidence
    if (owner) return owner === run
    if (inputs.length !== 1 || inputs[0].type !== 'userMessage') return false
    const text = inputs[0].content?.find((part: any) => part.type === 'text')?.text
    const id = typeof text === 'string' ? /^\[LazyMind action_id=([^\]\n]+)\]/.exec(text)?.[1] : undefined
    if (!id) return false
    const receipt = await this.receipts.read(id)
    return receipt?.kind === 'continue' && receipt.runId === run && receipt.threadId === thread
      && receipt.clientId === inputs[0].clientId && (!receipt.turnId || receipt.turnId === turnId)
  }
  private async cancelOwned(thread: string, claim: ActionClaim) {
    this.claims.delete(claim.action.id)
    this.validate(thread, claim, 'cancel')
    if (await this.receipts.read(claim.action.id)) throw new Error('Stop was already attempted; do not replay it')
    const state = await this.native.read(thread, this.signal)
    const active = state.turns.filter(turn => turn.status === 'inProgress')
    if (!active.length && !state.active) return
    const target = active[0]
    if (active.length !== 1 || !target.id) throw new AdmissionRejected('Current Codex work is not the target of this Stop')
    const evidence = workflowTurnEvidence(target), expectedOwnership = JSON.stringify(evidence)
    const stoppedAt = Date.parse(claim.action.created_at ?? '')
    if (!await this.owns(evidence, target.id, claim.action.session_id, thread)
      || !Number.isFinite(stoppedAt) || !Number.isFinite(target.startedAt) || target.startedAt! > stoppedAt) {
      throw new AdmissionRejected('Current Codex work is not the target of this Stop')
    }
    this.validate(thread, await this.bridge.action(claim.action.id, this.signal), 'cancel')
    await this.receipts.write({ actionId: claim.action.id, runId: claim.action.session_id, threadId: thread,
      kind: 'cancel', turnId: target.id, status: 'sending' })
    const result = await this.native.interrupt(thread, target.id, expectedOwnership, this.signal)
    if (result?.rejected) throw new AdmissionRejected(result.rejected)
    if (result.interruptedTurnId !== target.id) throw new Error('The target turn interruption was not confirmed')
    await this.receipts.write({ actionId: claim.action.id, runId: claim.action.session_id, threadId: thread,
      kind: 'cancel', turnId: target.id, status: 'accepted' })
  }
}
