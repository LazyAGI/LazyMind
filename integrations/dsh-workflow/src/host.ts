import type { Context } from '@deepseek-ai/cordis'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { GoalService } from '@deepseek-ai/dsh-goal'
import type {} from '@deepseek-ai/dsh-api-session-controller'
import type { SessionId } from '@deepseek-ai/dsh-session'
import type { ToolDefinition, ToolExecution, ToolRunContext } from '@deepseek-ai/dsh-tools'
import { createHash } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { BridgeError, type HostTransport, type ActionClaim, type HostAction } from './bridge'
import { eventRun, interaction, object, readControl, workflowOperation, type RunLink, type WorkflowControl } from './protocol'
import { workflowTool } from './tool'

const READS = new Set(['list', 'get', 'input_get', 'state', 'session_list', 'artifact_list', 'artifact_get'])
const ACQUIRE = new Set(['step_begin', 'step_claim', 'step_resume'])
const PAUSED = new Set(['awaiting_user', 'awaiting_executor', 'draining', 'stopped', 'binding_required'])
const STRUCTURED_OUTPUT = 'structured_output' // Published DSH 0.1.2 subagent completion contract.

interface Scope {
  agent: Agent
  registration?: Context
  ready: Promise<unknown>
  disposeRegistration(): Promise<void>
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
  wrappers: Map<string, { original: ToolDefinition; dispose(): void }>
  disposeGuard(): void
}

export interface HostConfig { serverName: string; webUrl: string }

/** DSH-specific lifecycle work stays here; business decisions stay in Core. */
export function installHost(ctx: Context, bridge: HostTransport, config: HostConfig, instanceId: string): () => Promise<void> {
  const lifetime = new AbortController()
  const scopes = new Map<Agent, Scope>()
  const tracked = new Set<Promise<unknown>>()
  const ptcLinks = new Map<string, RunLink>()
  const actionCache = new Map<string, ActionClaim>()
  let goals: GoalService | undefined
  ctx.inject(['goals'], goalCtx => {
    goals = goalCtx.goals
    goalCtx.effect(() => () => { goals = undefined })
  })
  const own = <T>(promise: Promise<T>): Promise<T> => {
    tracked.add(promise)
    void promise.then(() => tracked.delete(promise), () => tracked.delete(promise))
    return promise
  }
  const signal = (caller?: AbortSignal) => caller ? AbortSignal.any([caller, lifetime.signal]) : lifetime.signal
  const driver = (agent: Agent): Agent => {
    const visited = new Set<Agent>()
    while (!visited.has(agent)) {
      visited.add(agent)
      const parent = ctx.agents.list().find(candidate => candidate !== agent && ctx.agents.isOwnedBy(agent.session.id, candidate))
      if (!parent) return agent
      agent = parent
    }
    throw new Error('Workflow driver ownership contains a cycle')
  }
  const paused = (scope: Scope) => scope.unknown || !!scope.control && PAUSED.has(scope.control.continuation)
  const completion = (scope: Scope) => driver(scope.agent) !== scope.agent && scope.returnPending
    && !!ctx.tools.get(STRUCTURED_OUTPUT, scope.agent)

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

  function goalReason(runId: string, revision: number, stopped = false) {
    const run = createHash('sha256').update(runId).digest('hex').slice(0, 16)
    return `lazymind-${stopped ? 'stopped' : 'review'}-${run}-r${revision}`
  }

  function suspendGoal(scope: Scope) {
    if (!goals || !scope.runId || !scope.automatic || driver(scope.agent) !== scope.agent || !scope.control
      || !PAUSED.has(scope.control.continuation)) return
    const goal = goals.get(scope.agent)
    if (goal?.phase === 'active' && goal.activation === 'armed' && (!scope.goalId || scope.goalId === goal.id)) {
      goals.block(scope.agent, goal, { code: goalReason(scope.runId, goal.revision + 1, scope.control?.continuation === 'stopped'),
        message: scope.control.continuation === 'awaiting_executor' ? 'LazyMind is executing this workflow step.' : 'This LazyMind workflow needs user action before automatic work can continue.' })
    }
  }

  function resumeGoal(scope: Scope) {
    if (!goals || !scope.runId) return
    const goal = goals.get(scope.agent)
    if (goal?.phase === 'blocked' && [goalReason(scope.runId, goal.revision), goalReason(scope.runId, goal.revision, true)].includes(goal.blockedReason?.code ?? '')) goals.resume(scope.agent, goal)
  }

  function remember(scope: Scope) {
    const root = driver(scope.agent)
    const finished = new Set<string>()
    let ownedAt = 0
    for (const event of [...scope.agent.session.ownEvents()].reverse()) {
      const run = eventRun(event, config.serverName)
      if (!run || run.hostSessionId !== root.session.id || !run.operation || !['start', 'step_begin', 'step_claim', 'step_resume', 'step_submit'].includes(run.operation)) continue
      if (!scope.runId) { scope.runId = run.runId; scope.automatic = true; ownedAt = event.time }
      if (run.runId !== scope.runId || !run.executionId) continue
      if (run.operation === 'step_submit') finished.add(run.executionId)
      else if (ACQUIRE.has(run.operation) && !finished.has(run.executionId)) scope.grants.add(run.executionId)
    }
    if (!scope.runId && root !== scope.agent) scope.runId = ensure(root).runId
    const goal = goals?.get(root)
    if (goal && ownedAt && goal.createdAt <= ownedAt) scope.goalId = goal.id
    else if (goal && ownedAt && goal.createdAt > ownedAt) scope.automatic = false
  }

  function denial(scope: Scope, exec: Readonly<ToolExecution>): string | undefined {
    const operation = workflowOperation(exec.name, config.serverName)
    if (operation && (READS.has(operation) || operation === 'session_stop')) return undefined
    if (!scope.runId) return undefined
    if (exec.name === STRUCTURED_OUTPUT && completion(scope)) return undefined
    if (scope.unknown && (scope.activeOwned || operation)) return 'Workflow state is unavailable; retry after reconnecting LazyMind.'
    if (!paused(scope)) return undefined
    if (scope.control?.continuation === 'stopped') return operation || scope.activeOwned ? 'This Workflow has been stopped.' : undefined
    if (operation === 'step_submit' || operation === 'step_resume' || operation === 'step_claim') {
      const id = object(exec.arguments)?.execution_id
      return typeof id === 'string' && scope.control?.active_execution_ids?.includes(id) ? undefined : 'Only an already granted execution may finish while review is pending.'
    }
    if (operation) return 'Review the submitted artifacts in the LazyMind panel before starting new Workflow work.'
    if (scope.grants.size > 0 || scope.manual) return undefined
    if (scope.activeOwned) return 'This Workflow is waiting for user review.'
    return undefined
  }

  function ensure(agent: Agent): Scope {
    const existing = scopes.get(agent)
    if (existing) return existing
    const scope: Scope = { agent, ready: Promise.resolve(), disposeRegistration: async () => {}, unknown: false, grants: new Set(), manual: false, activeOwned: false, automatic: false,
      returnPending: false, wrappers: new Map(), disposeGuard: () => {} }
    scopes.set(agent, scope)
    remember(scope)
    // Inherit the real Agent context's scope tag, never a second copy of dsh-scope.
    const registration = agent.ctx.inject(['tools'], injected => {
      scope.registration = injected
      // Parent guards also see descendants. Decide using the actual caller's grants.
      scope.disposeGuard = injected.tools.guard(exec => denial(exec.agent ? scopes.get(exec.agent) ?? scope : scope, exec))
      wrap(scope)
    })
    scope.ready = registration.await()
    scope.disposeRegistration = () => registration.dispose()
    return scope
  }

  async function refresh(scope: Scope, caller?: AbortSignal): Promise<WorkflowControl | undefined> {
    if (!scope.runId) return undefined
    try {
      const state = await bridge.state(scope.runId, signal(caller))
      if (state.binding?.driver_session_id && state.binding.driver_session_id !== driver(scope.agent).session.id) {
        scope.control = undefined
        scope.runId = undefined
        scope.grants.clear()
        return undefined
      }
      publish(scope.runId, state)
      return scope.control
    } catch (error) { scope.unknown = true; throw error }
  }

  async function afterResult(value: unknown, exec: ToolRunContext): Promise<WorkflowControl | null> {
    if (!exec.agent || ctx.agents.get(exec.agent.session.id) !== exec.agent || lifetime.signal.aborted) return null
    const scope = ensure(exec.agent)
    const root = driver(exec.agent)
    const rootScope = ensure(root)
    const operation = workflowOperation(exec.name, config.serverName)
    const returned = readControl(value)
    const fields = object(object(value)?.structuredContent)
    const run = interaction(value, config.webUrl) ?? (returned ? { runId: returned.session_id,
      url: new URL(`/workflow-runs/${encodeURIComponent(returned.session_id)}`, config.webUrl).href } : null)
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
    if (operation === 'step_submit' && typeof fields?.execution_id === 'string') {
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
        ? await bridge.bind(runId, root.session.id, signal(exec.signal))
        : returned ?? await bridge.state(runId, signal(exec.signal))
      if (fresh.binding?.driver_session_id !== root.session.id) return null
      if (rootScope.runId && rootScope.runId !== runId && operation !== 'start') {
        if (scope !== rootScope) { scope.runId = runId; publish(runId, fresh); return scope.control ?? fresh }
        return null
      }
      scope.runId = rootScope.runId = runId
      if (operation === 'start') { rootScope.activeOwned = rootScope.automatic = true; rootScope.manual = false; rootScope.goalId = goals?.get(root)?.id }
      publish(runId, fresh)
      suspendGoal(rootScope)
      if (exec.parent && run) ptcLinks.set(exec.callId, { ...run, hostSessionId: root.session.id, operation: operation ?? undefined,
        ...(typeof fields?.execution_id === 'string' ? { executionId: fields.execution_id } : typeof execution?.execution_id === 'string' ? { executionId: execution.execution_id } : {}) })
      return scope.control ?? fresh
    } catch (error) {
      scope.runId = runId
      scope.unknown = true
      if (!rootScope.runId || rootScope.runId === runId || operation === 'start') { rootScope.runId = runId; rootScope.unknown = true }
      ctx.logger.warn(`lazymind-workflow: result committed, control synchronization failed: ${String(error)}`)
      // A committed Workflow outcome remains a successful tool result.
      return null
    }
  }

  function wrap(scope: Scope) {
    const registration = scope.registration
    if (!registration) return
    const live = new Set<string>()
    for (const schema of ctx.tools.schemas()) {
      const operation = workflowOperation(schema.name, config.serverName)
      if (operation === null) continue
      live.add(schema.name)
      const original = ctx.tools.get(schema.name)
      const previous = scope.wrappers.get(schema.name)
      if (!original || previous?.original === original) continue
      previous?.dispose()
      const definition = workflowTool(original, { trustedOrigin: config.webUrl, hostSessionId: driver(scope.agent).session.id,
        operation,
        afterResult: (value, exec) => own(afterResult(value, exec)),
        shouldConclude: (control) => scope.activeOwned && !completion(scope) && (scope.unknown || !!scope.runId && ['awaiting_user', 'awaiting_executor'].includes(control?.continuation ?? '')),
      })
      scope.wrappers.set(schema.name, { original, dispose: registration.tools.register(definition) })
    }
    for (const [name, value] of scope.wrappers) if (!live.has(name)) { value.dispose(); scope.wrappers.delete(name) }
  }

  async function lookupInput(agent: Agent, messages: readonly unknown[], caller: AbortSignal): Promise<ActionClaim | undefined> {
    for (const message of messages) {
      const source = object(object(message)?.source)
      if (source?.kind !== 'user' || typeof source.rpcId !== 'string') continue
      let claim = actionCache.get(source.rpcId)
      try { claim = await bridge.action(source.rpcId, signal(caller)) }
      catch (error) {
        if (error instanceof BridgeError && (error.status === 404 || error.status === 403)) continue
        if (error instanceof BridgeError && error.code === 'BINDING_STALE') throw error
        if (!claim) continue
      }
      if (claim && claim.action.native_session_id === agent.session.id) { actionCache.set(source.rpcId, claim); return claim }
    }
    return undefined
  }

  ctx.on('agent/pre-step', (payload, next) => own((async () => {
    const scope = ensure(payload.agent)
    await scope.ready
    wrap(scope)
    if (scope.turn !== payload.turn) { scope.turn = payload.turn; scope.manual = false; scope.activeOwned = scope.automatic; scope.actionId = undefined }
    try {
      const action = await lookupInput(payload.agent, payload.messages, payload.signal)
      if (action) {
        if (action.action.status === 'superseded' || action.action.consumed_at && scope.actionId !== action.action.id) return { kind: 'reject' as const }
        scope.runId = action.action.session_id
        scope.actionId = action.action.id
        scope.activeOwned = scope.automatic = true
        scope.manual = false
        publish(scope.runId, action.control)
      } else if (payload.messages.some(message => object(object(message)?.source)?.kind === 'user')) {
        scope.manual = true; scope.activeOwned = scope.automatic = false
      }
      if ((!scope.manual || scope.activeOwned) && !completion(scope)) await refresh(scope, payload.signal)
      suspendGoal(ensure(driver(scope.agent)))
      if (scope.runId && scope.activeOwned && paused(scope) && !scope.manual && scope.grants.size === 0 && !completion(scope)) return { kind: 'reject' as const }
      return next()
    } catch (error) {
      ctx.logger.warn(`lazymind-workflow: control gate deferred a step: ${String(error)}`)
      return { kind: 'reject' as const }
    }
  })()))
  ctx.on('tools/pre-execute', (exec, next) => own((async () => {
    if (!exec.agent) return next()
    const scope = ensure(exec.agent)
    await scope.ready
    if (exec.name === STRUCTURED_OUTPUT && completion(scope)) return next()
    const operation = workflowOperation(exec.name, config.serverName)
    const runId = object(exec.arguments)?.session_id
    if (operation && !READS.has(operation) && typeof runId === 'string' && runId !== scope.runId) {
      try {
        const fresh = await bridge.state(runId, signal(exec.signal))
        if (fresh.binding?.driver_session_id !== driver(exec.agent).session.id) return { kind: 'deny' as const, reason: 'This Workflow belongs to another driver session.' }
        scope.runId = runId
        publish(runId, fresh)
      } catch (error) { return { kind: 'deny' as const, reason: `Workflow state unavailable: ${String(error)}` } }
    }
    if (scope.runId && (scope.activeOwned || operation && !READS.has(operation))) {
      try { await refresh(scope, exec.signal) } catch { return { kind: 'deny' as const, reason: 'Workflow state is unavailable; reconnect LazyMind.' } }
    }
    const reason = denial(scope, exec)
    return reason ? { kind: 'deny' as const, reason } : next()
  })()))
  ctx.on('tools/ptc-dispatch-log', async (dispatch, next) => {
    const content = await next()
    const run = ptcLinks.get(dispatch.subCallId)
    ptcLinks.delete(dispatch.subCallId)
    return run ? [...content, { type: 'text', text: JSON.stringify({ lazymind_workflow: run }) }] : content
  })
  ctx.on('agent/status', ({ agent, status }) => { if (status === 'idle') { const scope = scopes.get(agent); if (scope) { scope.activeOwned = false; scope.manual = false } } })
  ctx.on('agent/disposed', ({ agent }) => {
    const scope = scopes.get(agent)
    if (!scope) return
    scope.disposeGuard()
    for (const wrapper of scope.wrappers.values()) wrapper.dispose()
    scopes.delete(agent)
    void own(scope.disposeRegistration())
  })
  for (const agent of ctx.agents.list()) ensure(agent)

  function inputSeq(events: readonly { type: string; seq: number; data: unknown }[], requestId: string): number {
    for (const event of events) {
      if (event.type !== 'user/message') continue
      const source = object(object(event.data)?.message)?.source ?? object(event.data)?.source
      if (object(source)?.rpcId === requestId) return event.seq
    }
    return 0
  }

  async function reconcile(action: HostAction): Promise<number> {
    const controller = new AbortController()
    try {
      const frames = ctx.sessionController.follow({ address: { kind: 'session', sessionId: action.native_session_id as SessionId }, maxMessages: 100 }, signal(controller.signal))
      for await (const frame of frames) {
        if (frame.type !== 'snapshot') continue
        const scan = (records: typeof frame.records) => inputSeq(records.flatMap(record => record.type === 'event' ? [record.event] : []), action.id)
        let found = scan(frame.records)
        let records = frame.records
        let more = frame.hasMore
        while (!found && more) {
          const seqs = records.map(record => record.event.seq)
          if (!seqs.length) break
          const page = await ctx.sessionController.page({ address: { kind: 'session', sessionId: action.native_session_id as SessionId },
            throughSeq: frame.cursor, beforeSeq: Math.min(...seqs), maxMessages: 100 }, signal(controller.signal))
          records = page.records
          found = scan(records)
          more = page.hasMore
        }
        return found
      }
      return 0
    } finally { controller.abort() }
  }

  async function deliver(action: HostAction) {
    if (action.kind === 'cancel' && action.status !== 'pending') {
      // Cancellation is idempotent. After restart reconcile against the actual driver,
      // never replay a prompt or cancel a different explicit user/workflow turn.
      const resolved = await ctx.sessionController.resolveAgent(action.native_session_id as SessionId)
      if ('error' in resolved) return
      const scope = ensure(resolved.agent)
      if (scope.runId === action.session_id && (scope.activeOwned || scope.grants.size > 0)) ctx.sessionController.cancel({ sessionId: resolved.agent.session.id })
      if (scope.runId === action.session_id) suspendGoal(scope)
      const seq = Math.max(0, resolved.agent.session.seq - 1)
      if (seq > 0) await bridge.settle(action.id, instanceId, '', 'accepted', seq, '', signal())
      return
    }
    if (action.kind === 'continue' && action.status !== 'pending') {
      const seq = await reconcile(action)
      if (seq > 0) { await bridge.settle(action.id, instanceId, '', 'accepted', seq, '', signal()); return }
    }
    const claim = await bridge.claim(action.id, instanceId, signal())
    actionCache.set(action.id, claim)
    if (!claim.dispatch_token || claim.action.status !== 'dispatching') return
    const resolved = await ctx.sessionController.resolveAgent(action.native_session_id as SessionId)
    if ('error' in resolved) { await bridge.settle(action.id, instanceId, claim.dispatch_token, 'failed', 0, resolved.error.message, signal()); return }
    const scope = ensure(resolved.agent)
    if (scope.runId === action.session_id) publish(action.session_id, claim.control)
    // Revalidate immediately before the host call; pre-step/guards cover the remaining race.
    const current = await bridge.action(action.id, signal())
    if (current.action.consumed_at || current.control.binding?.generation !== action.binding_generation) return
    try {
      if (action.kind === 'cancel') {
        if (scope.runId === action.session_id && (scope.activeOwned || scope.grants.size > 0)) ctx.sessionController.cancel({ sessionId: resolved.agent.session.id })
        if (scope.runId === action.session_id) suspendGoal(scope)
      } else {
        // An explicit panel continuation may replace this workflow's pending
        // question/planning turn, while preserving unrelated work and granted executions.
        if (!action.execution_id && scope.runId === action.session_id && scope.activeOwned && scope.grants.size === 0) {
          ctx.sessionController.cancel({ sessionId: resolved.agent.session.id })
        }
        // A queued input gains scope only in pre-step, when that exact input runs.
        await ctx.sessionController.prompt({ sessionId: resolved.agent.session.id, requestId: action.id as Parameters<typeof ctx.sessionController.prompt>[0]['requestId'],
          mode: 'queue', content: [{ type: 'text', text: action.execution_id
            ? `LazyMind workflow ${action.session_id} has an execution update. Call workflow.step.claim with execution_id=${action.execution_id} to inspect or acquire it. If executor_host is lazymind, it is managed by LazyMind: read the latest state and continue only ready steps when permitted; do not execute or submit it. Otherwise execute the granted contract and submit with execution_handle. Do not create a new workflow.`
            : `The user confirmed LazyMind workflow ${action.session_id}. Read its latest state through MCP and continue the ready steps. Use execution_handle on each submit and honor control.continuation. Do not create a new workflow.` }],
        }, signal())
        if (scope.runId === action.session_id) resumeGoal(scope)
      }
      const seq = action.kind === 'continue' ? inputSeq(resolved.agent.session.snapshotEvents(), action.id) : Math.max(0, resolved.agent.session.seq - 1)
      await bridge.settle(action.id, instanceId, claim.dispatch_token, 'accepted', seq, '', signal())
    } catch (error) {
      // Once a host call begins, failure cannot prove that the prompt was not admitted.
      await bridge.settle(action.id, instanceId, claim.dispatch_token, 'unknown', 0, String(error), signal())
    }
  }

  const polling = (async () => {
    while (!lifetime.signal.aborted) {
      try {
        let after = ''
        do {
          const page = await bridge.actions(after, signal())
          for (const action of page.actions) {
            try { await deliver(action) }
            catch (error) {
              if (!(error instanceof BridgeError && ['DELIVERY_PENDING', 'ACTION_CONSUMED', 'BINDING_STALE', 'WORKFLOW_ADMISSION_DENIED'].includes(error.code)) && !lifetime.signal.aborted) ctx.logger.warn(`lazymind-workflow: delivery pending: ${String(error)}`)
            }
          }
          after = page.next_page_token ?? ''
        } while (after && !lifetime.signal.aborted)
      } catch (error) { if (!lifetime.signal.aborted) ctx.logger.warn(`lazymind-workflow: reconnecting Bridge: ${String(error)}`) }
      try { await delay(1000, undefined, { signal: lifetime.signal }) } catch { break }
    }
  })()
  return async () => {
    lifetime.abort()
    await polling
    await Promise.allSettled([...tracked])
    for (const scope of scopes.values()) { scope.disposeGuard(); for (const wrapper of scope.wrappers.values()) wrapper.dispose(); await scope.disposeRegistration() }
    scopes.clear()
  }
}
