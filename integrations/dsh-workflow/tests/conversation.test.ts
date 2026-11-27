import { createHmac } from 'node:crypto'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { ToolDefinition, ToolRunContext } from '@deepseek-ai/dsh-tools'
import { expect, it, vi } from 'vitest'
import { hasNativeConversation, nativeConversation, nativeConversationTool } from '../src/conversation'

function agent(id: string, events: unknown[], origin?: string): Agent {
  return { session: { id, header: { cwd: '/write', createdAt: 0, origin }, ownEvents: () => events } } as unknown as Agent
}
const user = (id: string, text: string) => ({ type: 'user/message', seq: 1, time: 1,
  data: { id, source: { kind: 'user' }, content: [{ type: 'text', text }] } })
const assistant = (text: string) => ({ type: 'assistant/message', seq: 2, time: 2, data: { message:
  { content: [{ type: 'reasoning', text: 'private' }, { type: 'tool-call', name: 'lookup' }, { type: 'text', text }] } } })

it('projects the native root thread, retaining user-visible turns and titles', () => {
  const events = [{ type: 'turn/start', data: {}, time: 0 }, user('one', 'first'),
    ...['agent-instructions', 'runtime-context', 'skill-catalog', 'time-context'].map(kind => ({
      type: 'user/message', data: { source: { kind }, content: [{ type: 'text', text: 'internal context' }] }, time: 1,
    })), assistant('answer 1'), { type: 'turn/start', data: {}, time: 3 },
    { type: 'user/message', data: { source: { kind: 'system' }, content: [{ type: 'text', text: 'internal' }] }, time: 4 },
    assistant('internal response'), user('two', 'second'), assistant('answer 2'), { type: 'session/title', data: { title: 'Shared thread' }, time: 7 }]
  const root = agent('root', events)
  expect(nativeConversation(root.session)).toMatchObject({ thread_id: 'root', display_name: 'Shared thread',
    turns: [{ turn_id: 'one', user: 'first', assistant: 'answer 1' }, { turn_id: 'two', user: 'second', assistant: 'answer 2' }] })
  expect(nativeConversation(agent('worker', events, 'subagent').session)).toBeUndefined()
})

it('signs actual runtime context and replaces injected fields without altering model arguments', async () => {
  const root = agent('native-root-2', [user('native-user', 'hello')])
  const execute = vi.fn(async (_: unknown, _exec: unknown) => ({}))
  const original = { name: 'mcp__lazymind__workflow_list_6fc624ade2b2', execute, output: {} } as unknown as ToolDefinition
  const pairing = { connector_id: 'dsh-test', token: 'fixture-secret' }
  const observed = new WeakSet<Agent>()
  const wrapped = nativeConversationTool(original, pairing, observed)
  const args = Object.freeze({ limit: 1, __lazymind_dsh_source: 'spoof' })
  await wrapped.execute(args, { agent: root } as ToolRunContext)
  const sent = execute.mock.calls[0][0] as { limit: number; __lazymind_dsh_source: { body: string; mac: string } }
  expect(sent.limit).toBe(1)
  expect(JSON.parse(sent.__lazymind_dsh_source.body)).toMatchObject({ thread_id: 'native-root-2', turn_id: 'native-user', tool: original.name })
  expect(sent.__lazymind_dsh_source.mac).toBe(createHmac('sha256', pairing.token).update(sent.__lazymind_dsh_source.body).digest('hex'))
  expect(args.__lazymind_dsh_source).toBe('spoof')
  expect(observed.has(root)).toBe(true)
  const meta = wrapped.output.presentationMeta?.({}, {})
  const cold = agent('native-root-2', [{ type: 'tool/result', data: { meta } }])
  expect(hasNativeConversation(cold, pairing.connector_id)).toBe(true)
  expect(hasNativeConversation(cold, 'different-account-pair')).toBe(false)
})
