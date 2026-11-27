import type { Context } from '@deepseek-ai/cordis'
import type { Agent } from '@deepseek-ai/dsh-agent'
import type { ToolDefinition, ToolRunContext } from '@deepseek-ai/dsh-tools'
import { expect, it, vi } from 'vitest'
import { type ChatEvent, type ChatRun } from '../src/bridge'
import { isManagedThread, managedThreadId, nativeConversationTool } from '../src/conversation'
import { executeChat, pollChat } from '../src/outbound-chat'

const pair = { connector_id: 'local-dsh', token: 'paired-fixture' }
const run: ChatRun = { run_id: 'run-1', action: 'start', provider: 'deepseek-harness',
  host_id: pair.connector_id, prompt: 'First question', lease_token: 'lease-1' }
const textBlock = (text: string) => ({ type: 'text', text })
const event = (type: string, data: unknown) => ({ type, data })
const user = (rpcId: string, text: string) => event('user/message', { id: rpcId, source: { kind: 'user', rpcId }, content: [textBlock(text)] })
const answer = (text: string) => event('assistant/message', { message: { content: [{ type: 'reasoning', text: 'hidden' }, textBlock(text)] } })
const end = () => event('turn/end', { reason: { kind: 'completed' } })

function fixture() {
  const lifetime = new AbortController()
  const events = [event('turn/start', {}), user('ordinary', 'native question'), answer('unrelated native turn'), end()]
  const session = { id: managedThreadId(run.run_id, pair), header: { createdAt: 1 },
    ownEvents: () => events.map((e, i) => ({ ...e, seq: i, time: i })) }
  const agent = { session, inbox: { nextTurn: [], nextStep: [] }, whenIdle: vi.fn(async () => {}) }
  const controller = {
    create: vi.fn(async (req: {sessionId: string}) => req),
    resolveAgent: vi.fn(async () => ({ agent })),
    prompt: vi.fn(async (req: {requestId: string; content: {text: string}[]}) => {
      events.push(event('turn/start', {}), user(req.requestId, req.content[0].text), answer('actual ' + req.content[0].text), end())
    }),
    modelCatalog: vi.fn(async () => ({ default: {provider: 'dsh', model: 'default'}, routableProviders: ['dsh'],
      groups: [{id: 'dsh', models: [{id: 'default'}]}] })),
    cancel: vi.fn(), updateQueue: vi.fn(),
  }
  const ctx = { sessionController: controller } as unknown as Context
  const bridge = {
    claimChat: vi.fn(async (_status: unknown, _signal: AbortSignal) => ({run: null as ChatRun | null})),
    heartbeatChat: vi.fn(async () => ({ stop_requested: false })),
    publishChat: vi.fn(async (_r: ChatRun, _id: string, _e: ChatEvent) => {}),
  }
  return { lifetime, events, session, agent, ctx, controller, bridge }
}

it('binds before prompting, returns only real text, and resumes the same DSH session', async () => {
  const f = fixture()
  await executeChat(f.ctx, f.bridge, pair, run, f.lifetime.signal)
  expect(f.controller.create).toHaveBeenCalledWith({ sessionId: f.session.id })
  expect(f.bridge.publishChat.mock.invocationCallOrder[0]).toBeLessThan(f.controller.prompt.mock.invocationCallOrder[0])
  expect(f.bridge.publishChat.mock.calls.map(call => call[2])).toEqual([
    {type: 'thread_started', provider_thread_id: f.session.id}, {type: 'message', text: 'actual First question'},
    {type: 'turn_completed'}, {type: 'completed'},
  ])
  f.bridge.publishChat.mockClear()
  await executeChat(f.ctx, f.bridge, pair, { ...run, run_id: 'run-2', action: 'resume',
    provider_thread_id: f.session.id, prompt: 'Follow up' }, f.lifetime.signal)
  expect(f.controller.create).toHaveBeenCalledTimes(1)
  expect(f.controller.resolveAgent).toHaveBeenLastCalledWith(f.session.id)
  expect(f.bridge.publishChat.mock.calls[1][2]).toEqual({type: 'message', text: 'actual Follow up'})
})

it('reconciles a completed native turn with the same receipts after losing the bridge response', async () => {
  const f = fixture()
  await executeChat(f.ctx, f.bridge, pair, run, f.lifetime.signal)
  const receipts = f.bridge.publishChat.mock.calls.map(call => [call[1], call[2]])
  f.bridge.publishChat.mockClear()
  await executeChat(f.ctx, f.bridge, pair, { ...run, action: 'recover', provider_thread_id: f.session.id, lease_token: 'new-lease' }, f.lifetime.signal)
  expect(f.controller.prompt).toHaveBeenCalledTimes(1)
  expect(f.bridge.publishChat.mock.calls.map(call => [call[1], call[2]])).toEqual(receipts)
  await executeChat(f.ctx, f.bridge, pair, { ...run, action: 'finalize' }, f.lifetime.signal)
  expect(f.bridge.publishChat.mock.calls.at(-1)?.[2]).toEqual({type: 'completed'})
  expect(f.controller.prompt).toHaveBeenCalledTimes(1)
})

it.each([
  { reviewCall: 'call-1', resultCall: 'call-1', isError: false, completed: true },
  { reviewCall: 'nested-root', resultCall: 'nested-root', isError: true, completed: false },
  { reviewCall: 'other-call', resultCall: 'call-1', isError: false, completed: false },
  { reviewCall: '', resultCall: 'call-1', isError: false, completed: false },
])('settles a textless turn only for its successful review call (%j)', async ({ reviewCall, resultCall, isError, completed }) => {
  const f = fixture()
  f.controller.prompt.mockImplementationOnce(async req => {
    f.events.push(event('turn/start', {}), user(req.requestId, req.content[0].text), event('step/start', {}))
    if (reviewCall) f.events.push(event('lazymind/workflow-awaiting-user', { rootCallId: reviewCall }))
    f.events.push(event('tool/result', { message: { content: [{ type: 'tool-result', toolCallId: resultCall, isError, content: [] }] } }), end())
  })
  await executeChat(f.ctx, f.bridge, pair, run, f.lifetime.signal)
  const receipts = f.bridge.publishChat.mock.calls.map(call => call[2])
  if (!completed) {
    expect(receipts.at(-1)?.type).toBe('failed')
    return
  }
  expect(receipts.slice(1)).toEqual([
    { type: 'message', text: '工作流已暂停，等待你确认后继续。' }, { type: 'turn_completed' }, { type: 'completed' },
  ])
  f.bridge.publishChat.mockClear()
  await executeChat(f.ctx, f.bridge, pair, { ...run, action: 'recover', provider_thread_id: f.session.id }, f.lifetime.signal)
  expect(f.bridge.publishChat.mock.calls.map(call => call[2])).toEqual(receipts)
  expect(f.controller.prompt).toHaveBeenCalledTimes(1)
})

it('does not replay an unverified prompt or resume another connection’s session', async () => {
  const f = fixture()
  for (const thread of [f.session.id, managedThreadId('foreign-run', { ...pair, token: 'other-pairing' })]) {
    await executeChat(f.ctx, f.bridge, pair, { ...run, action: 'recover', provider_thread_id: thread }, f.lifetime.signal)
    expect(f.bridge.publishChat.mock.calls.at(-1)?.[2].type).toBe('failed')
  }
  expect(f.controller.prompt).not.toHaveBeenCalled()
})

it.each([false, true])('cancels only the running owned turn when the connection closes (shared turn: %s)', async shared => {
  const f = fixture()
  f.controller.prompt.mockImplementationOnce(async req => {
    f.events.push(event('turn/start', {}), user(req.requestId, req.content[0].text))
    if (shared) f.events.push(user('other', 'independent user request'))
  })
  f.agent.whenIdle.mockImplementationOnce(() => { f.lifetime.abort(); return Promise.resolve() })
  await executeChat(f.ctx, f.bridge, pair, run, f.lifetime.signal)
  expect(f.controller.cancel).toHaveBeenCalledTimes(shared ? 0 : 1)
  expect(f.bridge.publishChat.mock.calls.map(call => call[2].type)).toEqual(['thread_started'])
})

it('stops an active DSH turn when the next heartbeat reports cancellation', async () => {
  const f = fixture()
  f.bridge.heartbeatChat.mockResolvedValueOnce({stop_requested: false}).mockResolvedValue({stop_requested: true})
  f.controller.prompt.mockImplementationOnce(async req => {
    f.events.push(event('turn/start', {}), user(req.requestId, req.content[0].text))
  })
  f.agent.whenIdle.mockImplementationOnce(() => new Promise<void>(() => {}))
  await executeChat(f.ctx, f.bridge, pair, run, f.lifetime.signal)
  expect(f.controller.cancel).toHaveBeenCalledWith({sessionId: f.session.id})
  expect(f.bridge.publishChat.mock.calls.map(call => call[2].type)).toEqual(['thread_started'])
})

it.each([[], ['offline']])('never dispatches a claim with an inaccessible or missing default model (%s)', async (...routes) => {
  const f = fixture()
  f.controller.modelCatalog.mockResolvedValue({ default: {provider: 'offline', model: 'missing'}, routableProviders: routes,
    groups: [{ id: 'offline', models: [{id: 'other'}] }] })
  f.bridge.claimChat.mockImplementation(async () => { f.lifetime.abort(); return {run} })
  await pollChat(f.ctx, f.bridge, pair, f.lifetime.signal)
  expect(f.bridge.claimChat.mock.calls[0][0]).toEqual({ready: false, unavailable_reason: 'DSH default model unavailable'})
  expect(f.controller.prompt).not.toHaveBeenCalled()
})

it('identifies managed context from the paired native session, including after restart', async () => {
  const f = fixture()
  expect(isManagedThread(f.session.id, pair)).toBe(true)
  expect(isManagedThread(f.session.id, { ...pair, connector_id: 'other' })).toBe(false)
  expect(isManagedThread(f.session.id + '\n', pair)).toBe(false)
  const execute = vi.fn(async (_args: unknown, _exec: unknown) => ({}))
  const original = { name: 'mcp__lazymind__workflow_list', execute, output: {} } as unknown as ToolDefinition
  const wrapped = nativeConversationTool(original, pair, new WeakSet())
  await wrapped.execute({}, { agent: f.agent as unknown as Agent } as ToolRunContext)
  const sent = execute.mock.calls[0][0] as { __lazymind_dsh_source: { body: string } }
  expect(JSON.parse(sent.__lazymind_dsh_source.body)).toMatchObject({ thread_id: f.session.id, managed: true })
})
