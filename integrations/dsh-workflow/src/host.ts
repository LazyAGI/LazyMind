import type { Context } from '@deepseek-ai/cordis'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { ToolDefinition, ToolExecution, ToolRunContext } from '@deepseek-ai/dsh-tools'
import type { HostTransport } from '../../workflow-agent-core/src/transport'
import type { ToolCall } from '../../workflow-agent-core/src/adapter'
import { createCoordinator } from '../../workflow-agent-core/src/coordinator'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import { object } from '../../workflow-agent-core/src/protocol'
import { workflowOperation } from './events'
import { dshRuntime } from './host-adapter'
import { workflowTool } from './tool'

interface Registration {
  context?: Context
  ready: Promise<unknown>
  dispose(): Promise<void>
  disposeGuard(): void
  wrappers: Map<string, { original: ToolDefinition; dispose(): void }>
}
export interface HostConfig { serverName: string; webUrl: string }

/** Wire DSH public hooks to the shared coordinator. MCP execution remains in workflowTool. */
export function installHost(ctx: Context, bridge: HostTransport, config: HostConfig, instanceId: string): () => Promise<void> {
  const lifetime = new AbortController()
  const runtime = dshRuntime(ctx, config.serverName)
  const coordinator = createCoordinator(runtime, bridge, config.webUrl, lifetime.signal)
  const registrations = new Map<Agent, Registration>()
  const tracked = new Set<Promise<unknown>>()
  const own = <T>(promise: Promise<T>): Promise<T> => {
    tracked.add(promise)
    void promise.then(() => tracked.delete(promise), () => tracked.delete(promise))
    return promise
  }
  const call = (exec: Readonly<ToolExecution> | ToolRunContext): ToolCall<Agent> => ({
    agent: exec.agent, operation: workflowOperation(exec.name, config.serverName),
    arguments: 'arguments' in exec ? exec.arguments : undefined,
    returnsResult: exec.name === 'structured_output', signal: exec.signal,
    callId: exec.callId, nested: !!exec.parent,
  })

  function wrap(agent: Agent, registration: Registration) {
    const context = registration.context
    if (!context) return
    const live = new Set<string>()
    for (const schema of ctx.tools.schemas()) {
      const operation = workflowOperation(schema.name, config.serverName)
      if (operation === null) continue
      live.add(schema.name)
      const original = ctx.tools.get(schema.name)
      const previous = registration.wrappers.get(schema.name)
      if (!original || previous?.original === original) continue
      previous?.dispose()
      const definition = workflowTool(original, { trustedOrigin: config.webUrl,
        hostSessionId: runtime.id(coordinator.driver(agent)), operation,
        afterResult: (value, exec) => own(coordinator.afterResult(value, call(exec))),
        shouldConclude: control => coordinator.shouldConclude(agent, control),
      })
      registration.wrappers.set(schema.name, { original, dispose: context.tools.register(definition) })
    }
    for (const [name, value] of registration.wrappers) if (!live.has(name)) { value.dispose(); registration.wrappers.delete(name) }
  }

  function ensure(agent: Agent): Registration {
    const existing = registrations.get(agent)
    if (existing) return existing
    coordinator.ensure(agent)
    const entry: Registration = { ready: Promise.resolve(), dispose: async () => {}, disposeGuard: () => {}, wrappers: new Map() }
    registrations.set(agent, entry)
    // Inherit the real Agent context's scope tag, never a second copy of dsh-scope.
    const registration = agent.ctx.inject(['tools'], injected => {
      entry.context = injected
      entry.disposeGuard = injected.tools.guard(exec => coordinator.denial(exec.agent ?? agent, call(exec)))
      wrap(agent, entry)
    })
    entry.ready = registration.await()
    entry.dispose = () => registration.dispose()
    return entry
  }

  ctx.on('agent/pre-step', (payload, next) => own((async () => {
    const registration = ensure(payload.agent)
    await registration.ready
    wrap(payload.agent, registration)
    const allowed = await coordinator.beforeTurn({ ...payload, messages: payload.messages.map(message => {
      const source = object(object(message)?.source)
      return { user: source?.kind === 'user', requestId: typeof source?.rpcId === 'string' ? source.rpcId : undefined }
    }) })
    return allowed ? next() : { kind: 'reject' as const }
  })()))
  ctx.on('tools/pre-execute', (exec, next) => own((async () => {
    if (exec.agent) await ensure(exec.agent).ready
    const reason = await coordinator.beforeTool(call(exec))
    return reason ? { kind: 'deny' as const, reason } : next()
  })()))
  ctx.on('tools/ptc-dispatch-log', async (dispatch, next) => {
    const content = await next()
    const run = coordinator.takeNestedLink(dispatch.subCallId)
    return run ? [...content, { type: 'text', text: JSON.stringify({ lazymind_workflow: run }) }] : content
  })
  ctx.on('agent/status', ({ agent, status }) => { if (status === 'idle') coordinator.idle(agent) })
  ctx.on('agent/disposed', ({ agent }) => {
    const entry = registrations.get(agent)
    if (entry) {
      entry.disposeGuard()
      for (const wrapper of entry.wrappers.values()) wrapper.dispose()
      registrations.delete(agent)
      void own(entry.dispose())
    }
    coordinator.forget(agent)
  })
  for (const agent of ctx.agents.list()) ensure(agent)

  const polling = createDispatcher(runtime, coordinator, bridge, instanceId, lifetime.signal).poll()
  return async () => {
    lifetime.abort()
    await polling
    await Promise.allSettled([...tracked])
    for (const entry of registrations.values()) {
      entry.disposeGuard()
      for (const wrapper of entry.wrappers.values()) wrapper.dispose()
      await entry.dispose()
    }
    registrations.clear()
    coordinator.dispose()
  }
}
