import { mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { createHash, randomUUID } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { AdmissionRejected, type RuntimeAdapter } from '../../workflow-agent-core/src/adapter'
import type { ActionClaim, HostTransport } from '../../workflow-agent-core/src/transport'
import { object } from '../../workflow-agent-core/src/protocol'
import { AppServer, RpcError, RpcUnavailable, type NativeTurn } from './app-server'

interface Receipt {
  actionId: string; runId: string; threadId: string; kind: 'continue' | 'cancel'
  clientId?: string; turnId?: string; status: 'sending' | 'accepted' | 'unknown'
}
const threadPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i

/** Receipts contain identifiers only. A pre-write prevents replay after process death. */
export class NativeReceipts {
  constructor(private directory: string) {}
  private path(id: string) { return join(this.directory, createHash('sha256').update(id).digest('hex') + '.json') }
  async read(id: string): Promise<Receipt | undefined> {
    try {
      const value = JSON.parse(await readFile(this.path(id), 'utf8')) as Receipt
      if (value.actionId !== id || !value.runId || !threadPattern.test(value.threadId)) throw new Error('Invalid native receipt')
      return value
    } catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return undefined; throw error }
  }
  async write(receipt: Receipt) {
    await mkdir(this.directory, { recursive: true, mode: 0o700 })
    const path = this.path(receipt.actionId), temp = `${path}.${randomUUID()}.tmp`
    await writeFile(temp, JSON.stringify(receipt), { mode: 0o600, flag: 'wx' })
    await rename(temp, path)
  }
}

/** Only native MCP receipts after the latest user input establish execution ownership. */
export function workflowTurnOwner(turn: NativeTurn): string | undefined {
  let run: string | undefined
  for (const value of turn.items ?? []) {
    const item = object(value)
    if (!item) continue
    if (item.type === 'userMessage') { run = undefined; continue }
    if (item.type !== 'mcpToolCall' || !['lazymind', 'lazymind-workflow'].includes(String(item.server))
      || !['workflow.start', 'workflow.step.begin', 'workflow.step.claim', 'workflow.step.resume', 'workflow.step.complete'].includes(String(item.tool))
      || item.status !== 'completed' || item.error) continue
    const result = object(item.result), fields = object(result?.structuredContent)
    if (!fields || result?.isError === true) continue
    const state = object(fields.state), execution = object(fields.execution)
    const id = fields.session_id ?? state?.session_id
    if (typeof id !== 'string' || !id) continue
    // An error or a read of another run never claims a turn. Acquisitions must
    // have a real execution, and their argument/result session must agree.
    if (item.tool !== 'workflow.start' && object(item.arguments)?.session_id !== id) continue
    if (['workflow.step.begin', 'workflow.step.claim', 'workflow.step.resume'].includes(String(item.tool)) && typeof execution?.execution_id !== 'string') continue
    run = id
  }
  return run
}

/** Native admission and exact-turn cancellation; never falls back to queue. */
export class NativeCodexAdapter implements RuntimeAdapter<string> {
  readonly cancellation = 'session' as const
  readonly host = {
    cacheClaim: (_claim: ActionClaim) => {},
    cancelSession: (thread: string, claim: ActionClaim) => this.cancelOwned(thread, claim),
  }
  constructor(private rpc: AppServer, private bridge: HostTransport, private receipts: NativeReceipts,
    private signal: AbortSignal, private connectorId: string) {
    rpc.on('serverRequest', request => this.warn(`Codex is awaiting native user input/approval: ${request.method}. No automatic response was sent.`))
  }
  id(thread: string) { return thread }
  warn(message: string) { console.warn(message) }
  async resolve(thread: string) {
    if (!threadPattern.test(thread)) return { error: 'A Codex thread UUID is required' }
    try { await this.rpc.readThread(thread, this.signal); return { agent: thread } }
    catch (error) { if (error instanceof RpcError) return { error: 'Codex thread is unavailable' }; throw error }
  }
  private validate(thread: string, claim: ActionClaim, kind: 'continue' | 'cancel') {
    const { action, control } = claim, binding = control.binding
    if (action.kind !== kind || action.status !== 'dispatching' || action.consumed_at || action.native_session_id !== thread || control.session_id !== action.session_id
      || !binding?.bound || binding.driver_session_id !== thread || binding.provider !== 'codex' || binding.connector_id !== this.connectorId) {
      throw new AdmissionRejected('Native action does not match the current Workflow binding')
    }
    if (kind === 'cancel' ? control.continuation !== 'stopped' : control.continuation !== 'continue') {
      throw new AdmissionRejected('Workflow control notification has been superseded')
    }
  }
  async prompt(thread: string, input: { actionId: string; message: string }, signal: AbortSignal) {
    const claim = await this.bridge.action(input.actionId, signal)
    this.validate(thread, claim, 'continue')
    if (await this.receipts.read(input.actionId)) throw new RpcUnavailable('Native action was already attempted; do not replay it', true)
    const before = await this.rpc.readThread(thread, signal)
    if ((before.turns ?? []).some(turn => turn.status === 'inProgress')) {
      throw new AdmissionRejected('Codex thread is busy; no new input was sent. Retry from the Workflow panel after the current turn ends.')
    }
    signal.throwIfAborted()
    this.validate(thread, await this.bridge.action(input.actionId, signal), 'continue')
    const receipt: Receipt = { actionId: input.actionId, runId: claim.action.session_id, threadId: thread,
      kind: 'continue', clientId: randomUUID(), status: 'sending' }
    await this.receipts.write(receipt)
    let admitted = false
    try {
      const response = await this.rpc.startTurn(thread, `[LazyMind action_id=${input.actionId}]\n${input.message}`, receipt.clientId!, signal)
      admitted = true
      if (!response.turn?.id || !['inProgress', 'completed', 'interrupted', 'failed'].includes(response.turn.status)) throw new Error('Invalid native turn admission')
      if (before.turns?.some(turn => turn.id === response.turn.id)) throw new Error('Native input joined an existing turn; ownership was not acquired')
      const snapshot = await this.rpc.readThread(thread, signal)
      const target = snapshot.turns?.find(turn => turn.id === response.turn.id)
      const inputs = (target?.items ?? []).map(object).filter(item => item?.type === 'userMessage')
      if (inputs.length !== 1 || inputs[0]?.clientId !== receipt.clientId) {
        throw new Error('Native input collided with another user input; ownership was not acquired')
      }
      receipt.turnId = response.turn.id; receipt.status = 'accepted'
      await this.receipts.write(receipt)
      this.warn(`Native Workflow action ${input.actionId} admitted: thread=${thread}, turn=${receipt.turnId}`)
      return 0 // No invented durable event sequence.
    } catch (error) {
      receipt.status = 'unknown'; await this.receipts.write(receipt)
      if (!admitted && (error instanceof RpcError || error instanceof RpcUnavailable && !error.sent)) throw new AdmissionRejected('Native turn was not admitted')
      throw new RpcUnavailable('Native admission outcome is uncertain; do not replay or fall back to queue', true)
    }
  }
  cancel(): never { throw new AdmissionRejected('Native cancellation requires an exact Workflow action and turn') }
  private async owns(turn: NativeTurn, run: string, thread: string) {
    const owner = workflowTurnOwner(turn)
    if (owner) return owner === run
    // A subsequent explicit input cancels this proof, including mid-turn steer.
    const inputs = (turn.items ?? []).map(object).filter(item => item?.type === 'userMessage')
    if (inputs.length !== 1) return false
    const clientId = inputs[0]?.clientId
    if (typeof clientId !== 'string') return false
    // Verify the action marker against the persisted native client ID, not text
    // supplied by a user who happens to mention this Workflow.
    const content = inputs[0]?.content
    if (!Array.isArray(content)) return false
    const text = content.map(object).find(item => item?.type === 'text')?.text
    const actionId = typeof text === 'string' ? /^\[LazyMind action_id=([^\]\n]+)\]/.exec(text)?.[1] : undefined
    if (!actionId) return false
    const receipt = await this.receipts.read(actionId)
    return receipt?.kind === 'continue' && receipt.runId === run && receipt.threadId === thread && receipt.clientId === clientId
      && (!receipt.turnId || receipt.turnId === turn.id)
  }
  async cancelOwned(thread: string, claim: ActionClaim) {
    // Re-read Core immediately before inspecting native ownership.
    claim = await this.bridge.action(claim.action.id, this.signal)
    this.validate(thread, claim, 'cancel')
    if (await this.receipts.read(claim.action.id)) throw new RpcUnavailable('Native cancellation was already attempted; do not replay it', true)
    const snapshot = await this.rpc.readThread(thread, this.signal)
    const active = (snapshot.turns ?? []).filter(turn => turn.status === 'inProgress')
    if (active.length === 0) return // Already idle; nothing to interrupt.
    if (active.length !== 1 || !await this.owns(active[0], claim.action.session_id, thread)) {
      throw new AdmissionRejected('The current Codex turn is not owned by this Workflow; no interruption was sent')
    }
    this.validate(thread, await this.bridge.action(claim.action.id, this.signal), 'cancel')
    const receipt: Receipt = { actionId: claim.action.id, runId: claim.action.session_id, threadId: thread,
      kind: 'cancel', turnId: active[0].id, status: 'sending' }
    await this.receipts.write(receipt)
    await this.rpc.interruptTurn(thread, receipt.turnId!, this.signal)
    // RPC acceptance alone is insufficient: confirm the same target's state.
    for (let attempt = 0; attempt < 30; attempt++) {
      const state = await this.rpc.readThread(thread, this.signal)
      const target = state.turns?.find(turn => turn.id === receipt.turnId)
      if (target && target.status !== 'inProgress') {
        if (target.status !== 'interrupted') throw new Error(`Target turn ended as ${target.status}; interruption was not confirmed`)
        receipt.status = 'accepted'; await this.receipts.write(receipt)
        this.warn(`Native Workflow action ${receipt.actionId} interrupted: thread=${thread}, turn=${receipt.turnId}`)
        return
      }
      await delay(100, undefined, { signal: this.signal })
    }
    throw new Error('Native turn interruption is not yet confirmed; do not replay it')
  }
}
