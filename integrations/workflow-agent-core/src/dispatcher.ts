import { AdmissionRejected, type RuntimeAdapter } from './adapter'
import type { Coordinator } from './coordinator'
import { BridgeError, type HostTransport, type HostAction } from './transport'

/** Polling and delivery policy are shared; only host admission/history live in the adapter. */
export function createDispatcher<A>(runtime: RuntimeAdapter<A>, coordinator: Coordinator<A>, bridge: HostTransport,
  instanceId: string, signal: AbortSignal) {
  const { ensure, publish, suspendGoal, resumeGoal } = coordinator
  async function deliver(action: HostAction) {
    if (action.kind === 'cancel' && action.status !== 'pending') {
      if (runtime.supportsCancel === false) return
      const current = await bridge.action(action.id, signal)
      if (current.control.binding?.generation !== action.binding_generation || current.control.continuation !== 'stopped') return
      // Cancellation is idempotent. After restart reconcile against the actual driver,
      // never replay a prompt or cancel a different explicit user/workflow turn.
      const resolved = await runtime.resolve(action.native_session_id)
      if ('error' in resolved) return
      const scope = ensure(resolved.agent)
      if (scope.runId === action.session_id && (scope.activeOwned || scope.grants.size > 0)) await runtime.cancel(resolved.agent)
      if (scope.runId === action.session_id) suspendGoal(scope)
      const seq = runtime.eventSeq?.(resolved.agent) ?? 0
      if (seq > 0) await bridge.settle(action.id, instanceId, '', 'accepted', seq, '', signal)
      return
    }
    if (action.kind === 'continue' && action.status !== 'pending') {
      const current = await bridge.action(action.id, signal)
      if (!['pending', 'dispatching', 'unknown'].includes(current.action.status)) return
      if (current.action.status !== 'pending') {
        const seq = await runtime.reconcile?.(action.native_session_id, action.id, signal) ?? 0
        if (seq > 0) { await bridge.settle(action.id, instanceId, '', 'accepted', seq, '', signal); return }
      }
    }
    const claim = await bridge.claim(action.id, instanceId, signal)
    coordinator.cacheClaim(claim)
    if (!claim.dispatch_token || claim.action.status !== 'dispatching') return
    const resolved = await runtime.resolve(action.native_session_id)
    if ('error' in resolved) { await bridge.settle(action.id, instanceId, claim.dispatch_token, 'failed', 0, resolved.error, signal); return }
    const scope = ensure(resolved.agent)
    if (scope.runId === action.session_id) publish(action.session_id, claim.control)
    // Revalidate immediately before the host call; pre-step/guards cover the remaining race.
    const current = await bridge.action(action.id, signal)
    if (current.action.consumed_at || current.action.status !== 'dispatching'
      || current.control.binding?.generation !== action.binding_generation) return
    const validControl = action.kind === 'cancel' ? current.control.continuation === 'stopped'
      : action.execution_id ? !['stopped', 'binding_required'].includes(current.control.continuation)
        : current.control.continuation === 'continue' && current.control.admission.can_begin
    if (!validControl) {
      await bridge.settle(action.id, instanceId, claim.dispatch_token, 'failed', 0, 'Workflow control changed before host admission', signal)
      return
    }
    if (action.kind === 'cancel' && runtime.supportsCancel === false) {
      await bridge.settle(action.id, instanceId, claim.dispatch_token, 'failed', 0,
        'This host does not support interrupting the current turn; Workflow is stopped in Core.', signal)
      return
    }
    let seq = 0
    try {
      if (action.kind === 'cancel') {
        if (scope.runId === action.session_id && (scope.activeOwned || scope.grants.size > 0)) await runtime.cancel(resolved.agent)
        if (scope.runId === action.session_id) suspendGoal(scope)
      } else {
        // An explicit panel continuation may replace this workflow's pending
        // question/planning turn, while preserving unrelated work and granted executions.
        if (runtime.continuationMode !== 'queue' && !action.execution_id && scope.runId === action.session_id && scope.activeOwned && scope.grants.size === 0) {
          await runtime.cancel(resolved.agent)
        }
        // A queued input gains scope only in pre-step, when that exact input runs.
        seq = await runtime.prompt(resolved.agent, { actionId: action.id, message: action.execution_id
            ? `LazyMind workflow ${action.session_id} has an execution update. Call workflow.state, then workflow.step.claim with execution_id=${action.execution_id}. If an execution_handle is returned, execute the granted contract and submit with that handle. If executor_host is lazymind, only observe. Do not create a new workflow.`
            : `The user clicked Continue in the LazyMind panel for workflow ${action.session_id} and has finished the current review. Call workflow.state, then workflow.step.begin for a ready step when control.continuation=continue and admission.can_begin=true. A human step requires review AFTER execution; its mode or requires_approval flag does not require another confirmation before begin. Continue until awaiting_user, awaiting_executor, stopped, or completed. Execute each granted step_contract and submit using execution_handle. Do not ask the user to confirm the review again or create a new workflow.` }, signal)
        if (scope.runId === action.session_id) resumeGoal(scope)
      }
      if (action.kind === 'cancel') seq = runtime.eventSeq?.(resolved.agent) ?? 0
      await bridge.settle(action.id, instanceId, claim.dispatch_token, 'accepted', seq, '', signal)
    } catch (error) {
      // Once a host call begins, failure cannot prove that the prompt was not admitted.
      await bridge.settle(action.id, instanceId, claim.dispatch_token,
        error instanceof AdmissionRejected ? 'failed' : 'unknown', 0, String(error), signal)
    }
  }

  async function poll() {
    while (!signal.aborted) {
      try {
        let after = ''
        do {
          const page = await bridge.actions(after, signal)
          for (const action of page.actions) {
            try { await deliver(action) }
            catch (error) {
              if (!(error instanceof BridgeError && ['DELIVERY_PENDING', 'ACTION_CONSUMED', 'BINDING_STALE', 'WORKFLOW_ADMISSION_DENIED'].includes(error.code)) && !signal.aborted) runtime.warn(`lazymind-workflow: delivery pending: ${String(error)}`)
            }
          }
          after = page.next_page_token ?? ''
        } while (after && !signal.aborted)
      } catch (error) { if (!signal.aborted) runtime.warn(`lazymind-workflow: reconnecting Bridge: ${String(error)}`) }
      try { await delay(1000, signal) } catch { break }
    }
  }
  return { deliver, poll }
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return }
    const abort = () => { clearTimeout(timer); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, ms)
    signal.addEventListener('abort', abort, { once: true })
  })
}
