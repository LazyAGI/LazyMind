import { AdmissionRejected, type RuntimeAdapter } from './adapter'
import type { Coordinator } from './coordinator'
import { BridgeError, type HostTransport, type HostAction } from './transport'

/** Polling and delivery policy are shared; only host admission/history live in the adapter. */
export function createDispatcher<A>(runtime: RuntimeAdapter<A>, host: Pick<Coordinator<A>, 'cacheClaim' | 'cancelSession'> | undefined, bridge: HostTransport,
  instanceId: string, signal: AbortSignal) {
  async function deliver(action: HostAction) {
    if (action.status !== 'pending') {
      // Reconcile delivery receipts, not Workflow execution eligibility.
      const current = await bridge.action(action.id, signal)
      if (!['pending', 'dispatching', 'unknown'].includes(current.action.status)) return
      if (current.action.status !== 'pending' && action.kind === 'continue') {
        const seq = await runtime.reconcile?.(action.native_session_id, action.id, signal) ?? 0
        if (seq > 0) { await bridge.settle(action.id, instanceId, '', 'accepted', seq, '', signal); return }
      }
      // An uncertain cancel is not replayable: a session may now run different work.
      // Claim only expires an outstanding lease; it never reissues an unknown delivery.
    }
    const claim = await bridge.claim(action.id, instanceId, signal)
    if (!claim.dispatch_token || claim.action.status !== 'dispatching') return
    action = claim.action
    host?.cacheClaim(claim)
    let seq = 0
    let admissionStarted = false
    try {
      if (action.kind === 'cancel' && runtime.cancellation === 'none') {
        throw new AdmissionRejected('This host does not support interrupting the current turn; Workflow is stopped in Core.')
      }
      const resolved = await runtime.resolve(action.native_session_id)
      if ('error' in resolved) throw new AdmissionRejected(resolved.error)
      if (action.kind === 'cancel') {
        if (!host) throw new AdmissionRejected('Session cancellation requires a host ownership guard.')
        const current = await bridge.action(action.id, signal)
        admissionStarted = true
        await host.cancelSession(resolved.agent, current)
      } else {
        signal.throwIfAborted()
        admissionStarted = true
        seq = await runtime.prompt(resolved.agent, { actionId: action.id, message: continuationMessage(action) }, signal)
      }
    } catch (error) {
      // Once a host call begins, failure cannot prove that it was not admitted.
      await bridge.settle(action.id, instanceId, claim.dispatch_token,
        !admissionStarted || error instanceof AdmissionRejected ? 'failed' : 'unknown', 0, String(error), signal)
      return
    }
    // Keep receipt failures separate from host failures; the next poll reconciles them.
    await bridge.settle(action.id, instanceId, claim.dispatch_token, 'accepted', seq, '', signal)
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
              if (!(error instanceof BridgeError && ['DELIVERY_PENDING', 'ACTION_CONSUMED'].includes(error.code)) && !signal.aborted) runtime.warn(`lazymind-workflow: delivery pending: ${String(error)}`)
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

function continuationMessage(action: HostAction): string {
  const request = action.execution_id
    ? `When applicable, call workflow.step.claim with execution_id=${action.execution_id}. For a completed internal execution, inspect its result and the current state instead of executing it again.`
    : 'When control.continuation=continue and admission.can_begin=true, call workflow.step.begin for a ready step.'
  return `LazyMind workflow ${action.session_id} has a control notification. Call workflow.state first; the notification may be delayed. ${request} ` +
    'Execute only a granted external step_contract and publish/submit using its execution_handle. If executor_host is lazymind, let Core execute it. ' +
    'After submission, use the returned state to continue. Yield when awaiting_user, awaiting_executor, draining, stopped, binding_required, completed, or failed; do not poll while waiting. ' +
    'A human step requires review AFTER execution. Do not assume this notification means a review was approved. Do not create a new workflow.'
}
