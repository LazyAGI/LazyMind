import type { RuntimeAdapter, ToolCall, HostInput } from './adapter'
import { BridgeError, type HostTransport, type ActionClaim } from './transport'
import { interaction, object, readControl, type RunLink, type WorkflowControl } from './protocol'

const READS = new Set(['list', 'get', 'input_get', 'state', 'session_list', 'artifact_list', 'artifact_get'])
const ACQUIRE = new Set(['step_begin', 'step_claim', 'step_resume'])
const PAUSED = new Set(['awaiting_user', 'awaiting_executor', 'draining', 'stopped', 'binding_required'])

export interface Scope<A> {
  agent: A
  runId?: string
  control?: WorkflowControl
  unknown: boolean
  grants: Set<string>
  manual: boolean
  activeOwned: boolean
  automatic: boolean
  goalId?: string
  returnPending: boolean
  actionId?: string
  turn?: number
}

/** Coordinates host execution using Core decisions; does not proxy MCP calls. */
export function createCoordinator<A>(runtime: RuntimeAdapter<A>, bridge: HostTransport, webUrl: string, lifetime: AbortSignal) {
  const scopes = new Map<A, Scope<A>>()
  const ptcLinks = new Map<string, RunLink>()
  const actionCache = new Map<string, ActionClaim>()
  const signal = (caller?: AbortSignal) => caller ? AbortSignal.any([caller, lifetime]) : lifetime
  const driver = (agent: A): A => {
    const visited = new Set<A>()
    while (!visited.has(agent)) {
      visited.add(agent)
      const parent = runtime.parent?.(agent)
      if (!parent) return agent
      agent = parent
    }
    throw new Error('Workflow driver ownership contains a cycle')
  }
  const paused = (scope: Scope<A>) => scope.unknown || !!scope.control && PAUSED.has(scope.control.continuation)
  const completion = (scope: Scope<A>) => driver(scope.agent) !== scope.agent && scope.returnPending
    && !!runtime.canReturnResult?.(scope.agent)

  function publish(runId: string, control: WorkflowControl) {
    if (control.protocol !== 'workflow.control.v1' || control.session_id !== runId) throw new Error('Invalid workflow control response')
    for (const scope of scopes.values()) {
      if (scope.runId !== runId) continue
      scope.unknown = false
      if (scope.control && scope.control.state_version > control.state_version) continue
      scope.control = control
      if (control.active_execution_ids) {
        for (const id of scope.grants) if (!control.active_execution_ids.includes(id) || control.native_execution_ids?.includes(id)) scope.grants.delete(id)
      }
    }
  }

  function suspendGoal(scope: Scope<A>) {
    if (!scope.runId || !scope.automatic || driver(scope.agent) !== scope.agent || !scope.control
      || !PAUSED.has(scope.control.continuation)) return
    runtime.suspendGoal?.(scope.agent, { runId: scope.runId, goalId: scope.goalId, continuation: scope.control.continuation })
  }

  function resumeGoal(scope: Scope<A>) {
    if (scope.runId) runtime.resumeGoal?.(scope.agent, scope.runId)
  }

  function remember(scope: Scope<A>) {
    const root = driver(scope.agent)
    const finished = new Set<string>()
    let ownedAt = 0
    let ownedSeq = -1
    let latestInputSeq = -1
    for (const event of [...(runtime.history?.(scope.agent) ?? [])].reverse()) {
      if (event.user && latestInputSeq < 0) latestInputSeq = event.seq
      const run = event.run
      if (!run || run.hostSessionId !== runtime.id(root) || !run.operation || !['start', 'step_begin', 'step_claim', 'step_resume', 'step_complete'].includes(run.operation)) continue
      if (!scope.runId) { scope.runId = run.runId; scope.automatic = true; ownedAt = event.time; ownedSeq = event.seq }
      if (run.runId !== scope.runId || !run.executionId) continue
      if (run.operation === 'step_complete') finished.add(run.executionId)
      else if (ACQUIRE.has(run.operation) && !finished.has(run.executionId)) scope.grants.add(run.executionId)
    }
    // Restore ownership of a still-running workflow turn after plugin reload.
    // A later explicit user message belongs to the user, not this workflow.
    if (latestInputSeq > ownedSeq) scope.automatic = false
    scope.activeOwned = !!runtime.isRunning?.(scope.agent) && scope.automatic
    if (!scope.runId && root !== scope.agent) scope.runId = ensure(root).runId
    const goal = runtime.goal?.(root)
    if (goal && ownedAt && goal.createdAt <= ownedAt) scope.goalId = goal.id
    else if (goal && ownedAt && goal.createdAt > ownedAt) scope.automatic = false
  }

  function denial(scope: Scope<A>, exec: Readonly<ToolCall<A>>): string | undefined {
    const operation = exec.operation
    if (operation && (READS.has(operation) || operation === 'session_stop')) return undefined
    if (!scope.runId) return undefined
    if (exec.returnsResult && completion(scope)) return undefined
    if (scope.unknown && (scope.activeOwned || operation)) return 'Workflow state is unavailable; retry after reconnecting LazyMind.'
    if (!paused(scope)) return undefined
    if (scope.control?.continuation === 'stopped') return operation || scope.activeOwned ? 'This Workflow has been stopped.' : undefined
    if (operation === 'artifact_publish' || operation === 'step_complete' || operation === 'step_resume' || operation === 'step_claim') {
      const id = object(exec.arguments)?.execution_id
      return typeof id === 'string' && scope.control?.active_execution_ids?.includes(id) ? undefined : 'Only an already granted execution may finish while review is pending.'
    }
    if (operation) return 'Review the submitted artifacts in the LazyMind panel before starting new Workflow work.'
    if (scope.grants.size > 0 || scope.manual) return undefined
    if (scope.activeOwned) return 'This Workflow is waiting for user review.'
    return undefined
  }

  function ensure(agent: A): Scope<A> {
    const existing = scopes.get(agent)
    if (existing) return existing
    const scope: Scope<A> = { agent, unknown: false, grants: new Set(), manual: false,
      activeOwned: false, automatic: false, returnPending: false }
    scopes.set(agent, scope)
    remember(scope)
    return scope
  }

  async function refresh(scope: Scope<A>, caller?: AbortSignal): Promise<WorkflowControl | undefined> {
    if (!scope.runId) return undefined
    try {
      let state = await bridge.state(scope.runId, signal(caller))
      const root = driver(scope.agent)
      // Repair only an unbound run created by this driver, proven by its own
      // persisted tool receipt. Reading an arbitrary run never grants ownership.
      const createdHere = state.continuation === 'binding_required' && !state.binding?.bound
        && !state.binding?.driver_session_id && (runtime.history?.(root) ?? []).some(event => {
          const run = event.run
          return !!run && run.runId === scope.runId && run.operation === 'start' && run.hostSessionId === runtime.id(root)
        })
      if (createdHere) state = await bridge.bind(scope.runId, runtime.id(root), signal(caller))
      if (state.binding?.driver_session_id && state.binding.driver_session_id !== runtime.id(driver(scope.agent))) {
        scope.control = undefined
        scope.runId = undefined
        scope.grants.clear()
        return undefined
      }
      publish(scope.runId, state)
      return scope.control
    } catch (error) { scope.unknown = true; throw error }
  }

  async function afterResult(value: unknown, exec: ToolCall<A>): Promise<WorkflowControl | null> {
    if (!exec.agent || runtime.isLive?.(exec.agent) === false || lifetime.aborted) return null
    const scope = ensure(exec.agent)
    const root = driver(exec.agent)
    const rootScope = ensure(root)
    const operation = exec.operation
    const returned = readControl(value)
    const fields = object(object(value)?.structuredContent)
    const run = interaction(value, webUrl) ?? (returned ? { runId: returned.session_id,
      url: new URL(`/workflow-runs/${encodeURIComponent(returned.session_id)}`, webUrl).href } : null)
    const runId = returned?.session_id ?? run?.runId
    if (!runId) return null
    // Reading another run is discovery, not ownership of its driver session.
    if (operation && READS.has(operation) && runId !== scope.runId && runId !== rootScope.runId) return returned
    const execution = object(fields?.execution)
    if (operation && ACQUIRE.has(operation) && typeof execution?.execution_id === 'string') {
      if (execution.executor_host !== 'lazymind') scope.grants.add(execution.execution_id)
      scope.returnPending = execution.executor_host === 'lazymind'
      scope.activeOwned = scope.automatic = true
      scope.manual = false
    }
    if (operation === 'step_complete' && typeof fields?.execution_id === 'string') {
      scope.grants.delete(fields.execution_id)
      scope.returnPending = true
      scope.activeOwned = scope.automatic = true
      scope.manual = false
    }
    if (operation === 'start') {
      scope.activeOwned = scope.automatic = rootScope.activeOwned = rootScope.automatic = true
      scope.manual = rootScope.manual = false
    }
    try {
      const fresh = operation === 'start'
        ? await bridge.bind(runId, runtime.id(root), signal(exec.signal))
        : returned ?? await bridge.state(runId, signal(exec.signal))
      if (fresh.binding?.driver_session_id !== runtime.id(root)) return null
      if (rootScope.runId && rootScope.runId !== runId && operation !== 'start') {
        if (scope !== rootScope) { scope.runId = runId; publish(runId, fresh); return scope.control ?? fresh }
        return null
      }
      scope.runId = rootScope.runId = runId
      if (operation === 'start') { rootScope.activeOwned = rootScope.automatic = true; rootScope.manual = false; rootScope.goalId = runtime.goal?.(root)?.id }
      publish(runId, fresh)
      suspendGoal(rootScope)
      if (exec.nested && run) ptcLinks.set(exec.callId, { ...run, hostSessionId: runtime.id(root), operation: operation ?? undefined,
        ...(typeof fields?.execution_id === 'string' ? { executionId: fields.execution_id } : typeof execution?.execution_id === 'string' ? { executionId: execution.execution_id } : {}) })
      return scope.control ?? fresh
    } catch (error) {
      scope.runId = runId
      scope.unknown = true
      if (!rootScope.runId || rootScope.runId === runId || operation === 'start') { rootScope.runId = runId; rootScope.unknown = true }
      runtime.warn(`lazymind-workflow: result committed, control synchronization failed: ${String(error)}`)
      // A committed Workflow outcome remains a successful tool result.
      return null
    }
  }

  async function lookupInput(agent: A, messages: readonly HostInput[], caller: AbortSignal): Promise<ActionClaim | undefined> {
    for (const message of messages) {
      const source = message
      if (!source.user || typeof source.requestId !== 'string') continue
      let claim = actionCache.get(source.requestId)
      try { claim = await bridge.action(source.requestId, signal(caller)) }
      catch (error) {
        if (error instanceof BridgeError && (error.status === 404 || error.status === 403)) continue
        if (error instanceof BridgeError && error.code === 'BINDING_STALE') throw error
        if (!claim) continue
      }
      if (claim && claim.action.native_session_id === runtime.id(agent)) { actionCache.set(source.requestId, claim); return claim }
    }
    return undefined
  }

  async function beforeTurn(payload: { agent: A; turn: number; messages: readonly HostInput[]; signal: AbortSignal }): Promise<boolean> {
    const scope = ensure(payload.agent)
    if (scope.turn !== payload.turn) { scope.turn = payload.turn; scope.manual = false; scope.activeOwned = scope.automatic; scope.actionId = undefined }
    try {
      const action = await lookupInput(payload.agent, payload.messages, payload.signal)
      if (action) {
        if (action.action.status === 'superseded' || action.action.binding_generation !== action.control.binding?.generation
          || action.action.consumed_at && scope.actionId !== action.action.id) return false
        scope.runId = action.action.session_id
        scope.actionId = action.action.id
        scope.activeOwned = scope.automatic = true
        scope.manual = false
        publish(scope.runId, action.control)
      } else if (payload.messages.some(message => message.user)) {
        scope.manual = true; scope.activeOwned = scope.automatic = false
      }
      if ((!scope.manual || scope.activeOwned) && !completion(scope)) await refresh(scope, payload.signal)
      suspendGoal(ensure(driver(scope.agent)))
      if (scope.runId && scope.activeOwned && paused(scope) && !scope.manual && scope.grants.size === 0 && !completion(scope)) return false
      return true
    } catch (error) {
      runtime.warn(`lazymind-workflow: control gate deferred a step: ${String(error)}`)
      return false
    }
  }

  async function beforeTool(exec: ToolCall<A>): Promise<string | undefined> {
    if (!exec.agent) return undefined
    const scope = ensure(exec.agent)
    if (exec.returnsResult && completion(scope)) return undefined
    const operation = exec.operation
    const runId = object(exec.arguments)?.session_id
    if (operation && !READS.has(operation) && typeof runId === 'string' && runId !== scope.runId) {
      try {
        const fresh = await bridge.state(runId, signal(exec.signal))
        if (fresh.binding?.driver_session_id !== runtime.id(driver(exec.agent))) return 'This Workflow belongs to another driver session.'
        scope.runId = runId
        publish(runId, fresh)
      } catch (error) { return `Workflow state unavailable: ${String(error)}` }
    }
    if (scope.runId && (scope.activeOwned || operation && !READS.has(operation))) {
      try { await refresh(scope, exec.signal) } catch { return 'Workflow state is unavailable; reconnect LazyMind.' }
    }
    return denial(scope, exec)
  }

  return {
    ensure, driver, publish, suspendGoal, resumeGoal, afterResult, beforeTurn, beforeTool,
    cacheClaim(claim: ActionClaim) { actionCache.set(claim.action.id, claim) },
    denial(agent: A, exec: ToolCall<A>) { return denial(ensure(agent), exec) },
    shouldConclude(agent: A, control: WorkflowControl | null) {
      const scope = ensure(agent)
      return scope.activeOwned && !completion(scope) && (scope.unknown || !!scope.runId
        && ['awaiting_user', 'awaiting_executor'].includes(control?.continuation ?? ''))
    },
    takeNestedLink(callId: string) { const link = ptcLinks.get(callId); ptcLinks.delete(callId); return link },
    idle(agent: A) { const scope = scopes.get(agent); if (scope) { scope.activeOwned = false; scope.manual = false } },
    forget(agent: A) { scopes.delete(agent) },
    dispose() { scopes.clear(); ptcLinks.clear(); actionCache.clear() },
  }
}

export type Coordinator<A> = ReturnType<typeof createCoordinator<A>>
