import { randomUUID } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import type { CodexNative, NativeThread } from './native'
import type { NativeReceipts } from './receipts'

export interface RecoveryIntent {
  id?: string; thread_id: string; tool?: string; arguments?: Record<string, unknown>
}
const threadID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i
const identifier = /^[a-f0-9]{32}$/
const tools = new Set(['workflow.start', 'workflow.step.begin', 'workflow.step.claim', 'workflow.step.resume'])

export function recoveryMessage(request: string, intent: RecoveryIntent) {
  if (!identifier.test(request) || !identifier.test(intent.id ?? '') || !threadID.test(intent.thread_id)
    || !tools.has(intent.tool ?? '') || !intent.arguments || typeof intent.arguments !== 'object' || Array.isArray(intent.arguments)) {
    throw Error('Invalid saved Codex recovery operation')
  }
  const args = intent.arguments
  if (intent.tool === 'workflow.start' && (typeof args.idempotency_key !== 'string' || args.idempotency_key.length < 16
    || args.driver_session_id !== intent.thread_id || typeof args.workflow_id !== 'string')) throw Error('Missing stable Workflow start identity')
  if (intent.tool === 'workflow.step.begin' && (typeof args.command_id !== 'string' || !args.command_id)) throw Error('Missing stable Workflow command identity')
  return `[LazyMind recovery_id=${request} operation_id=${intent.id}]\n` +
    'The user requested the operation below before Codex session control was unavailable, then chose Restart and connect in LazyMind. ' +
    'LazyMind saved this operation BEFORE any Workflow execution mutation. The restart ended the old MCP call; this is its recovery handoff, not a new user task. ' +
    'Continue in this original conversation. Do not ask the user to type Continue again. Preserve every supplied identifier and argument.\n' +
    `Saved operation (JSON data): ${JSON.stringify({ tool: intent.tool, arguments: args })}\n` +
    (intent.tool === 'workflow.start'
      ? 'Retry workflow.start with these exact arguments and the SAME idempotency_key. Use the returned existing/new session. Do not create a second run or invent another retry key. '
      : 'Read workflow.state for the saved session_id first. Reconcile the saved execution/command with current state; use an existing successful result instead of executing it again. ' +
        'For workflow.step.begin, proceed only when current control admission permits it and the saved step is ready or retryable; keep the SAME command_id. ' +
        'For workflow.step.claim or workflow.step.resume, use the SAME execution_id, never begin a replacement execution. ') +
    'Continue using the existing Workflow skill and returned execution contract. The bound panel appears automatically. ' +
    'Keep normal review rules: yield when awaiting_user, stopped, completed, failed or otherwise not admitted; restarting is not approval of a Workflow review.'
}

type Native = Pick<CodexNative, 'visible' | 'presented' | 'read' | 'start'>
type Receipts = Pick<NativeReceipts, 'read' | 'write'>

/** One-shot, invoked only by the LazyMind recovery child after the user's click.
 * Write before native admission; an uncertain send is reconciled, never replayed.
 */
export async function deliverRecovery(request: string, intent: RecoveryIntent, native: Native, receipts: Receipts,
  signal: AbortSignal, pause: (signal: AbortSignal) => Promise<void> = signal => delay(250, undefined, { signal }), authorize = async () => {}, stage = (_stage:string) => {}): Promise<{accepted:boolean}> {
  if (!identifier.test(request) || !threadID.test(intent.thread_id)) throw Error('Invalid Codex recovery target')
  const message = intent.tool ? recoveryMessage(request, intent) : undefined
  const actionId = `recovery:${request}:${intent.id}`
  const saved = message ? await receipts.read(actionId) : undefined
  const previous = saved?.status === 'rejected' ? undefined : saved
  if (previous && (previous.threadId !== intent.thread_id || previous.runId !== request)) throw Error('Recovery receipt does not match this conversation')
  let state: NativeThread
  stage('waiting-chat')
  for (;;) {
    signal.throwIfAborted()
    try {
      if (!message) {
        stage('showing-panel')
        if (await native.presented(intent.thread_id, signal)) return {accepted:true}
        await pause(signal); continue
      }
      if (!await native.visible(intent.thread_id, signal)) { await pause(signal); continue }
      state = await native.read(intent.thread_id, signal)
    } catch (error) {
      if (signal.aborted) throw error
      await pause(signal); continue
    }
    if (previous?.status === 'accepted') return { accepted: true }
    if (state.ready === false) { await pause(signal); continue }
    if (previous) {
      const matches = state.turns.filter(turn => turn.items.some(item => item.type === 'userMessage' && item.clientId === previous.clientId))
      if (matches.length === 1) {
        await receipts.write({ ...previous, turnId: matches[0].id, status: 'accepted' })
        return { accepted: true }
      }
      // The native call may have succeeded before its reply/receipt was lost.
      // Repeating the same recovery button must never submit another message.
      throw Error('Recovery delivery is uncertain; no duplicate request was sent')
    }
    if (!state.active) break
    stage('waiting-idle')
    // Never interrupt other work or inject into an active turn.
    await pause(signal)
  }
  const receipt = { actionId, runId: request, threadId: intent.thread_id, kind: 'continue' as const,
    clientId: randomUUID(), status: 'sending' as 'sending' | 'accepted' | 'unknown' | 'rejected', turnId: undefined as string | undefined }
  signal.throwIfAborted()
  await authorize()
  signal.throwIfAborted()
  await receipts.write(receipt)
  let retrying = false
  try {
    stage('resuming')
    const result = await native.start(intent.thread_id, receipt.clientId, message!, signal)
    if (result?.rejected) {
      // An explicit rejection proves no message was admitted. Retain the record;
      // do not automatically retry a send just because the host became busy.
      receipt.status = 'rejected'
      if (result.retryable) {
        await receipts.write(receipt)
        stage('waiting-chat')
        await pause(signal)
        retrying = true
        return await deliverRecovery(request,intent,native,receipts,signal,pause,authorize,stage)
      }
      throw Error(result.rejected)
    }
    if (!result?.accepted || !result.turnId || state.turns.some(turn => turn.id === result.turnId)) throw Error('Recovery delivery receipt is uncertain')
    receipt.turnId = result.turnId
    receipt.status = 'accepted'
    await receipts.write(receipt)
    return { accepted: true }
  } catch (error) {
    if (retrying) throw error
    if (receipt.status !== 'rejected') receipt.status = 'unknown'
    await receipts.write(receipt)
    throw error
  }
}
