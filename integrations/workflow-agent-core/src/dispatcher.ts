import { AdmissionRejected, type RuntimeAdapter } from './adapter'
import type { Coordinator } from './coordinator'
import { BridgeError, isHostAuthorizationError, type HostTransport, type HostAction } from './transport'
import type { WorkflowControl } from './protocol'

/** Polling and delivery policy are shared; only host admission/history live in the adapter. */
export function createDispatcher<A>(runtime: RuntimeAdapter<A>, host: Pick<Coordinator<A>, 'cacheClaim' | 'cancelSession'> | undefined, bridge: HostTransport,
  instanceId: string, signal: AbortSignal) {
  async function deliver(action: HostAction) {
    // Offline/busy hosts retain pending work without spending a delivery lease.
    // Cancel never waits behind a continuation's readiness check.
    if (action.status === 'pending' && action.kind !== 'cancel' && runtime.ready
      && !await runtime.ready(action.native_session_id, signal)) return
    if (action.status !== 'pending') {
      // Reconcile delivery receipts, not Workflow execution eligibility.
      const current = await bridge.action(action.id, signal)
      if (!['pending', 'dispatching', 'unknown'].includes(current.action.status)) return
      if (current.action.status !== 'pending' && action.kind !== 'cancel') {
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
        seq = await runtime.prompt(resolved.agent, { actionId: action.id, message: continuationMessage(action, claim.control) }, signal)
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
    let failures = 0, lastWarning = ''
    while (!signal.aborted) {
      let retryMs = 1000
      try {
        let after = ''
        do {
          const page = await bridge.actions(after, signal)
          for (const action of [...page.actions].sort((a, b) => Number(b.kind === 'cancel') - Number(a.kind === 'cancel'))) {
            try { await deliver(action) }
            catch (error) {
              if (isHostAuthorizationError(error)) throw error
              if (!(error instanceof BridgeError && ['DELIVERY_PENDING', 'ACTION_CONSUMED'].includes(error.code)) && !signal.aborted) runtime.warn('lazymind-workflow: delivery pending; checking the previous receipt before retrying')
            }
          }
          after = page.next_page_token ?? ''
        } while (after && !signal.aborted)
        failures = 0
        lastWarning = ''
      } catch (error) {
        if (signal.aborted) break
        const needsAuthorization = isHostAuthorizationError(error)
        // A renewed login or repaired pairing can recover without restarting the
        // host. Probe slowly while waiting; never replay a claimed delivery.
        retryMs = needsAuthorization ? 30_000 : Math.min(30_000, 1000 * 2 ** Math.min(failures, 5))
        failures++
        const code = error instanceof BridgeError && /^[A-Z][A-Z0-9_]{0,63}$/.test(error.code) ? error.code : 'BRIDGE_UNAVAILABLE'
        const warning = needsAuthorization
          ? `lazymind-workflow: authorization required (${code}); sign in or repair the connection in LazyMind`
          : `lazymind-workflow: reconnecting Bridge (${code})`
        if (warning !== lastWarning) runtime.warn(warning)
        lastWarning = warning
      }
      try { await delay(retryMs, signal) } catch { break }
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

function continuationMessage(action: HostAction, control?: WorkflowControl): string {
  if (action.kind === 'model_generate') return `LazyMind model request: session_id=${action.session_id}, job_id=${action.execution_id}. ` +
    'Call workflow.generation.claim with a stable command_id. Only with status=running, execute the exact generation_request as a model worker. ' +
    'LazyMind has reserved one of at most two workers for this Workflow session. Claim ready_job_ids too and run up to two native model workers concurrently when available. Keep worker_token in the controller. ' +
    'Use the supplied messages, tool definitions and response format. Generate an OpenAI assistant message with content or tool_calls. ' +
    'Treat each job as a fresh model call. Read every message in its original order, including earlier data, schemas and guidance; the final message may contain only task parameters or a repair error. Generate the response from this request, including each repair request. ' +
    'LazyMind runs the tools, business logic, validation and publication. After the worker ends, return its message with workflow.generation.respond and unchanged worker_token and request_hash. ' +
    'For cancel_requested, acknowledge cancelled=true without generating. For already_claimed or terminal status, END this turn. ' +
    'Do not begin or complete Workflow steps, publish artifacts, approve reviews, or call a LazyMind model. Wait for the next notification.'
  if (action.kind === 'artifact_edit') return `LazyMind artifact edit: session_id=${action.session_id}, job_id=${action.execution_id}. ` +
    'Call workflow.action.get. When status=waiting_agent, execute the returned generation_request using a native subagent when available, or the current external agent otherwise. ' +
    'Use its exact LazyMind system_prompt and user_prompt; do not substitute your own business Skill. ' +
    'Submit only the external answer with workflow.action.respond using the unchanged version and request_hash. ' +
    'Repeat only if another generation_request is returned. When ready or cancelled, END this turn: the user confirms the preview in LazyMind. ' +
    'Do not begin, rewind or rerun workflow steps, and do not commit the preview yourself.'

  const automaticRetry = control?.continuation === 'continue' && control.admission?.can_begin && control.automatic_retry_steps?.length
  const request = automaticRetry
    ? `Core has automatic retry budget for ${JSON.stringify(control.automatic_retry_steps)}. After reading workflow.state, if control.continuation=continue and admission.can_begin=true, call workflow.step.begin for a step in the returned control.automatic_retry_steps with retry_origin=automatic and the current state version. Inspect the failed attempt and use its actual error for repair.`
    : action.execution_id
    ? `When applicable, call workflow.step.claim with execution_id=${action.execution_id}. For a completed internal execution, inspect its result and the current state instead of executing it again.`
    : 'When control.continuation=continue and admission.can_begin=true, call workflow.step.begin for a ready step.'
  return `LazyMind workflow ${action.session_id} has a control notification. Call workflow.state first; the notification may be delayed. ${request} ` +
    'Execute only a granted external step_contract and publish/submit using its execution_handle. If executor_host is lazymind, let Core execute it. ' +
    'After submission, use the returned state to continue. Yield when awaiting_user, awaiting_executor, draining, stopped, binding_required, completed, or failed; do not poll while waiting. ' +
    'When control.input_request is present, show its exact questions and wait for the user; submit their actual answers with workflow.input.answer against that artifact revision. An input answer does not itself approve a separate result review. ' +
    'A human step requires review AFTER execution. Do not assume this notification means a review was approved. Do not create a new workflow.'
}
