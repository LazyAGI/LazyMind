import { createHash, createHmac, timingSafeEqual } from 'node:crypto'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import type { Pairing } from './bridge'
import { object } from './protocol'

interface NativeTurn { turn_id: string; user: string; assistant: string; created_at: string }
interface NativeConversation {
  thread_id: string; cwd: string; display_name: string; native_updated_at: string; turns: NativeTurn[]
}

// Only visible text from human inputs and assistant messages belongs in conversation history.
export function visibleText(message: unknown): string {
  const content = object(message)?.content
  if (!Array.isArray(content)) return ''
  return content.flatMap(block => block?.type === 'text' && typeof block.text === 'string' ? [block.text] : []).join('\n').trim()
}

// The native ID survives a plugin restart. A hand-chosen prefix or a user
// prompt must never mark an unrelated native thread as a LazyMind execution.
function signedThread(digest: string, pairing: Pairing): string {
  const tag = createHmac('sha256', pairing.token).update(`dsh-chat:${pairing.connector_id}:${digest}`).digest('hex').slice(0, 32)
  return `session-lm-${digest}-${tag}`
}
export function managedThreadId(runId: string, pairing: Pairing): string {
  return signedThread(createHash('sha256').update(runId).digest('hex').slice(0, 32), pairing)
}
export function isManagedThread(id: string, pairing: Pairing): boolean {
  const match = /^session-lm-([0-9a-f]{32})-[0-9a-f]{32}$/.exec(id)
  return !!match && match[0] === id && timingSafeEqual(Buffer.from(id), Buffer.from(signedThread(match[1], pairing)))
}

export function nativeConversation(session: Agent['session']): NativeConversation | undefined {
  if (session.header.origin === 'subagent' || session.header.delegationDepth) return
  const turns: NativeTurn[] = []
  let active: NativeTurn | undefined
  let title = ''
  let updated = session.header.createdAt
  for (const event of session.ownEvents()) {
    const data = object(event.data)
    if (!data) continue
    if (event.type === 'session/title' && typeof data.title === 'string') title = data.title
    if (event.type === 'turn/start') active = undefined
    if (event.type === 'user/message') {
      const message = object(data.message) ?? data
      const user = visibleText(message)
      if (object(message.source)?.kind !== 'user' || !user) continue
      active = { turn_id: typeof message.id === 'string' ? message.id : 'user-' + event.seq,
        user, assistant: '', created_at: new Date(event.time).toISOString() }
      turns.push(active)
    } else if (event.type === 'assistant/message' && active) {
      const answer = visibleText(data.message)
      if (answer) active.assistant += (active.assistant ? '\n\n' : '') + answer
    }
    updated = event.time
  }
  if (!turns.length) return
  return { thread_id: session.id, cwd: session.header.cwd ?? '', display_name: title || turns[0].user,
    native_updated_at: new Date(updated).toISOString(), turns }
}

export function nativeConversationTool(original: ToolDefinition, pairing: Pairing, observed: WeakSet<Agent>): ToolDefinition {
  return {
    ...original,
    async execute(args, exec) {
      if (!exec.agent) return original.execute(args, exec)
      const session = nativeConversation(exec.agent.session)
      if (!session) return original.execute(args, exec)
      const latest = session.turns[session.turns.length - 1]
      // The DSH MCP client forwards arguments but has no per-call metadata hook.
      // Attach the paired host's evidence after model argument validation. The local
      // proxy verifies and removes it before forwarding or recording any arguments.
      const body = JSON.stringify({ thread_id: session.thread_id, turn_id: latest.turn_id,
        message: latest.user.slice(0, 8192), cwd: session.cwd, issued_at: Date.now(), tool: original.name,
        ...(isManagedThread(session.thread_id, pairing) ? { managed: true } : {}) })
      const mac = createHmac('sha256', pairing.token).update(body).digest('hex')
      const value = await original.execute({ ...object(args), __lazymind_dsh_source: { body, mac } }, exec)
      observed.add(exec.agent)
      return value
    },
    output: { ...original.output, presentationMeta(args, value) {
      const meta = original.output.presentationMeta?.(args, value)
      return { ...object(meta), lazymind_conversation_connector: pairing.connector_id } as ReturnType<NonNullable<typeof original.output.presentationMeta>>
    } },
  }
}

export function hasNativeConversation(agent: Agent, connectorId: string): boolean {
  return agent.session.ownEvents().some(event => event.type === 'tool/result' &&
    object(object(event.data)?.meta)?.lazymind_conversation_connector === connectorId)
}
