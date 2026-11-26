import { createHash } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import type { Context } from '@deepseek-ai/cordis'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { SessionId } from '@deepseek-ai/dsh-session'
import { BridgeError, type ChatEvent, type ChatRun, type DshBridge, type Pairing } from './bridge'
import { isManagedThread, managedThreadId, visibleText } from './conversation'
import { object } from './protocol'

type ChatTransport = Pick<DshBridge, 'claimChat' | 'heartbeatChat' | 'publishChat'>
type RequestId = Parameters<Context['sessionController']['prompt']>[0]['requestId']
const digest = (input: string) => createHash('sha256').update(input).digest('hex')

// Only return the turn containing our exact SDK prompt receipt. Ignore
// private reasoning, tool results and other users' turns in the same thread.
function readTurn(agent: Agent, requestId: RequestId) {
  let matched = false, otherUser = false
  let text: string[] = []
  for (const event of agent.session.ownEvents()) {
    if (event.type === 'turn/start') { matched = otherUser = false; text = [] }
    if (event.type === 'user/message') {
      const source = object(event.data.source)
      if (source?.kind === 'user') {
        if (source.rpcId === requestId) matched = true
        else otherUser = true
      }
    }
    if (!matched) continue
    if (event.type === 'assistant/message') {
      const content = visibleText(event.data.message)
      if (content) text.push(content)
    }
    if (event.type === 'turn/end') return { matched, otherUser, reason: event.data.reason.kind, text }
  }
  return { matched, otherUser, reason: undefined, text }
}

function queued(agent: Agent, requestId: RequestId) {
  return [...agent.inbox.nextTurn, ...agent.inbox.nextStep].find(item => object(item.source)?.rpcId === requestId)
}

async function interrupt(ctx: Context, agent: Agent, id: RequestId) {
  const sessionId = agent.session.id
  const pending = queued(agent, id)
  if (pending) {
    await ctx.sessionController.updateQueue({ sessionId, itemId: pending.id, action: { kind: 'remove' } })
  } else {
    const turn = readTurn(agent, id)
    if (turn.matched && !turn.reason && !turn.otherUser) ctx.sessionController.cancel({ sessionId })
  }
}

async function untilAborted<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  signal.throwIfAborted()
  const ended = Promise.withResolvers<never>()
  const abort = () => ended.reject(signal.reason)
  signal.addEventListener('abort', abort, { once: true })
  try { return await Promise.race([promise, ended.promise]) }
  finally { signal.removeEventListener('abort', abort) }
}

class ChatFailure extends Error {}
export async function executeChat(ctx: Context, bridge: ChatTransport, pairing: Pairing, run: ChatRun, lifetime: AbortSignal) {
  if (run.provider !== 'deepseek-harness' || run.host_id !== pairing.connector_id) return
  const lease = new AbortController()
  const signal = AbortSignal.any([lease.signal, lifetime])
  const requestId = digest('dsh-prompt:' + run.run_id) as RequestId
  let agent: Agent | undefined
  async function heartbeat() {
    if ((await bridge.heartbeatChat(run, signal)).stop_requested) lease.abort()
  }
  async function emit(event: ChatEvent, part = 0) {
    const id = digest(`${run.run_id}:${event.type}:${part}`)
    for (;;) {
      signal.throwIfAborted()
      try { await bridge.publishChat(run, id, event, signal); return }
      catch (error) {
        if (error instanceof BridgeError && error.status < 500 && error.status !== 429) {
          if ([401, 403, 409, 410].includes(error.status)) lease.abort()
          throw error
        }
        await delay(250, undefined, { signal })
      }
    }
  }
  const monitoring = (async () => {
    while (!signal.aborted) { await delay(2000, undefined, { signal }); await heartbeat() }
  })().catch(() => lease.abort())
  try {
    await heartbeat()
    signal.throwIfAborted()
    if (run.action !== 'finalize') {
      const id = run.provider_thread_id || managedThreadId(run.run_id, pairing)
      if (!isManagedThread(id, pairing)) throw new ChatFailure('DSH thread does not belong to this connection')
      if (!run.provider_thread_id) {
        if (run.action !== 'start' && run.action !== 'regenerate') throw new ChatFailure('Missing DSH thread')
        const created = await ctx.sessionController.create({ sessionId: id as SessionId })
        if (created.sessionId !== id) throw new ChatFailure('DSH returned an unexpected thread')
      }
      const resolved = await ctx.sessionController.resolveAgent(id as SessionId)
      if ('error' in resolved || resolved.agent.session.header.origin === 'subagent' || resolved.agent.session.header.delegationDepth) {
        throw new ChatFailure('DSH thread is unavailable')
      }
      agent = resolved.agent
      await emit({ type: 'thread_started', provider_thread_id: id })
      let turn = readTurn(agent, requestId)
      if (!turn.matched && !queued(agent, requestId)) {
        if (run.action === 'recover') throw new ChatFailure('DSH could not verify delivery of the previous prompt')
        signal.throwIfAborted()
        await ctx.sessionController.prompt({ sessionId: agent.session.id, requestId, mode: 'queue',
          content: [{ type: 'text', text: run.prompt }] }, signal)
      }
      if (!turn.reason) {
        await untilAborted(agent.whenIdle(), signal)
        turn = readTurn(agent, requestId)
      }
      if (turn.otherUser || turn.reason !== 'completed' || !turn.text.length) throw new ChatFailure('DSH did not complete this prompt with a separate text response')
      // Bound each UTF-8 event below Core's 1 MiB limit without splitting a code point.
      let part = 0
      for (const chunk of turn.text.join('\n\n').matchAll(/[\s\S]{1,65536}/gu)) await emit({ type: 'message', text: chunk[0] }, part++)
      await emit({ type: 'turn_completed' })
    }
    await emit({ type: 'completed' })
  } catch (error) {
    if (!signal.aborted) await emit({ type: 'failed', error: error instanceof ChatFailure ? error.message : 'DSH execution failed' })
  } finally {
    const interrupted = signal.aborted
    lease.abort()
    await monitoring
    if (interrupted && agent) await interrupt(ctx, agent, requestId)
  }
}

export async function pollChat(ctx: Context, bridge: ChatTransport, pairing: Pairing, signal: AbortSignal) {
  let status: { ready: boolean; unavailable_reason?: string } = { ready: false }
  let refreshAt = 0
  while (!signal.aborted) {
    try {
      if (Date.now() >= refreshAt) {
        try {
          const catalog = await untilAborted(ctx.sessionController.modelCatalog(), AbortSignal.any([signal, AbortSignal.timeout(5000)]))
          status = catalog.default?.model && catalog.routableProviders.includes(catalog.default.provider) &&
            catalog.groups.some(group => group.id === catalog.default.provider && group.models.some(model => model.id === catalog.default.model))
            ? { ready: true } : { ready: false, unavailable_reason: 'DSH default model unavailable' }
        } catch { status = { ready: false, unavailable_reason: 'DSH execution unavailable' } }
        refreshAt = Date.now() + 15_000
      }
      const { run } = await bridge.claimChat(status, signal)
      if (run && status.ready) await executeChat(ctx, bridge, pairing, run, signal)
      else await delay(1000, undefined, { signal })
    } catch (error) {
      if (signal.aborted) break
      const backoff = error instanceof BridgeError && error.status === 404 ? 15_000 : 2000
      await delay(backoff, undefined, { signal }).catch(() => {})
    }
  }
}
