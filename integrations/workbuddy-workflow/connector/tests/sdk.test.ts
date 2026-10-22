import { afterEach, describe, expect, it, vi } from 'vitest'
import { sdkOperation } from '../src/sdk'

const A = '8c76b002-6467-41f5-a6aa-f9e4242d8865'
const B = '8c76b002-6467-41f5-a6aa-f9e4242d8866'
const args = { workflow_id: 'wf', idempotency_key: 'unique-start-key-1', input_bindings: { a: 'b' } }
function fixture() {
  const block = { type: 'tool', status: 'in_progress', toolName: 'DeferExecuteTool', toolCallId: 'call-1',
    rawInput: { toolName: 'mcp__lazymind__workflow.start', params: args } }
  const request = { id: 'native-request', state: 'streaming', assistantMessage: { content: [block] } }
  const sessions: any = {
    [A]: { info: { id: A, state: 'working' }, requests: vi.fn(async () => ({ items: [request] })), sendPrompt: vi.fn(async () => undefined), cancel: vi.fn(async () => undefined) },
    [B]: { info: { id: B, state: 'idle' }, requests: vi.fn(async () => ({ items: [] })), sendPrompt: vi.fn(async () => undefined), cancel: vi.fn(async () => undefined) },
  }
  const sdk = { currentId: B, get: vi.fn(async (id: string) => sessions[id]),
    local: { list: vi.fn(async () => ({ items: Object.values(sessions).map((c: any) => c.info), total: 2 })) } }
  vi.stubGlobal('wb', { conversations: sdk }); vi.stubGlobal('workbuddyDesktop', { appVersion: '5.8.3' })
  return { sdk, sessions, block, request }
}
afterEach(() => vi.unstubAllGlobals())
describe('WorkBuddy native SDK contract', () => {
  it.each(['5.8.3', '5.8.4', '5.9.0', '6.0.0', '5.8.4-rc.1'])('supports the checked SDK contract on %s', async version => {
    const f = fixture(); vi.stubGlobal('workbuddyDesktop', { appVersion: version })
    expect(await sdkOperation({ kind: 'probe' })).toEqual({ version })
    expect(await sdkOperation({ kind: 'send', sessionId: B, actionId: 'version-check', message: 'wake' })).toEqual({ accepted: true })
    expect(await sdkOperation({ kind: 'cancel', sessionId: A })).toEqual({ accepted: true })
    expect(f.sessions[B].sendPrompt).toHaveBeenCalledOnce()
    expect(f.sessions[A].cancel).toHaveBeenCalledOnce()
  })
  it('resolves the exact active native start in A while the UI displays B', async () => {
    fixture()
    const result = await sdkOperation({ kind: 'identity', arguments: { ...args, input_bindings: { a: 'b' } } })
    expect(result.matches).toEqual([{ sessionId: A, requestId: 'native-request', toolCallId: 'call-1' }])
  })
  it.each(['completed', 'cancelled', 'error'])('ignores old %s turns', async state => {
    const f = fixture(); f.request.state = state
    expect((await sdkOperation({ kind: 'identity', arguments: args })).matches).toEqual([])
  })
  it('does not accept a different tool, partial arguments, or compacted evidence', async () => {
    const f = fixture()
    f.block.rawInput.toolName = 'mcp__other__workflow.start'
    expect((await sdkOperation({ kind: 'identity', arguments: args })).matches).toEqual([])
    f.block.rawInput.toolName = 'mcp__lazymind__workflow.start'
    expect((await sdkOperation({ kind: 'identity', arguments: { workflow_id: 'wf' } })).matches).toEqual([])
    Object.assign(f.block, { _meta: { 'codebuddy.ai/compactTruncated': true } })
    expect((await sdkOperation({ kind: 'identity', arguments: args })).matches).toEqual([])
  })
  it('fails closed when native pagination repeats before the inventory is complete', async () => {
    const f = fixture(); f.sdk.local.list.mockImplementation(async () => ({ items: [f.sessions[A].info], total: 2 }))
    await expect(sdkOperation({ kind: 'identity', arguments: args })).rejects.toThrow('Incomplete')
  })
  it('sends to the requested idle session and returns admission without waiting for a turn', async () => {
    const f = fixture(); f.sessions[A].info.state = 'idle'
    expect(await sdkOperation({ kind: 'send', sessionId: A, actionId: 'action-1', message: 'wake' })).toEqual({ accepted: true })
    expect(f.sessions[A].sendPrompt).toHaveBeenCalledWith([{ type: 'text', text: 'wake' }], expect.objectContaining({ clientRequestId: 'action-1' }))
    expect(f.sessions[B].sendPrompt).not.toHaveBeenCalled()
  })
  it('rejects a busy race before any input is sent', async () => {
    const f = fixture()
    expect(await sdkOperation({ kind: 'send', sessionId: A })).toHaveProperty('rejected')
    expect(f.sessions[A].sendPrompt).not.toHaveBeenCalled()

  })
  it('cancels the specified background session while another session is visible', async () => {
    const f = fixture()
    expect(await sdkOperation({ kind: 'cancel', sessionId: A })).toEqual({ accepted: true })
    expect(f.sessions[A].cancel).toHaveBeenCalledOnce()
    expect(f.sessions[B].cancel).not.toHaveBeenCalled()
    f.sessions[A].cancel.mockResolvedValue({ errorCode: 'unavailable' })
    expect(await sdkOperation({ kind: 'cancel', sessionId: A })).toEqual({ rejected: 'unavailable' })
  })
  it('rejects versions below the minimum', async () => {
    fixture(); vi.stubGlobal('workbuddyDesktop', { appVersion: '5.8.2' })
    await expect(sdkOperation({ kind: 'status', sessionId: A })).rejects.toThrow('unsupported_version')
  })
  it('reports a deleted bound session without recreating or choosing another', async () => {
    const f = fixture()
    f.sdk.get.mockRejectedValue(new Error('not found'))
    Object.assign(f.sdk, { listByIds: async () => [] })
    expect(await sdkOperation({ kind: 'status', sessionId: A })).toEqual({ missing: true })
    expect(f.sessions[B].sendPrompt).not.toHaveBeenCalled()
  })
})
