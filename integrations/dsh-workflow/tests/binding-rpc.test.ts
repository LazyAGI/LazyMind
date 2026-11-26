import type { Context } from '@deepseek-ai/cordis'
import { expect, it, vi } from 'vitest'
import type { BindingResult } from '../../workflow-agent-core/src/adapter'
import { installBindingRPC } from '../src/binding-rpc'

it('scopes the host-authorized binding RPC to native sessions and the current connection', async () => {
  let handle!: (request: Request) => Promise<Response>
  const register = vi.fn((endpoint: { fetch: typeof handle }) => { handle = endpoint.fetch })
  const scope = {
    get: () => ({ fetch: { register } }),
    sessionController: { list: async () => ({ items: [{ sessionId: 'chat-owned' }] }) },
  }
  const ctx = { inject: (_keys: string[], use: (scope: unknown) => void) => use(scope) } as unknown as Context
  let ready!: (result: BindingResult) => void
  const lookup = vi.fn(async () => new Promise<BindingResult>(resolve => { ready = resolve }))
  const config = { serverName: 'lazymind', webUrl: 'https://lazymind.example', pairingFile: 'private-path', bridgeUrl: 'private-url' }
  let current: { lookup: typeof lookup; config: typeof config } | undefined = { lookup, config }
  const report = vi.fn()
  installBindingRPC(ctx, () => current, report)
  expect(register).toHaveBeenCalledWith(expect.objectContaining({ path: '/api/lazymind/workflow-binding', methods: ['POST'] }))
  const request = (sessionId: string) => handle(new Request('http://localhost/api/lazymind/workflow-binding', {
    method: 'POST', body: JSON.stringify({
      type: 'client-request', method: 'lazymind/workflow-binding', rpcId: 'request-1', payload: { sessionId },
    }),
  }))
  const other = await request('other-profile-chat')
  expect(await other.json()).toMatchObject({
    result: { ok: true, value: { sessionId: 'other-profile-chat', binding: { status: 'unavailable' } } },
  })
  expect(lookup).not.toHaveBeenCalled()

  const reply = request('chat-owned')
  await vi.waitFor(() => expect(lookup).toHaveBeenCalledTimes(1))
  ready({ status: 'bound', runId: 'known-run' })
  const payload = await (await reply).json()
  expect(payload).toMatchObject({ result: { value: { binding: { status: 'bound', runId: 'known-run' } } } })
  expect(JSON.stringify(payload)).not.toContain('private-')
  expect(report.mock.calls).toEqual([['unavailable', undefined], ['bound', undefined]])

  const revoked = request('chat-owned')
  await vi.waitFor(() => expect(lookup).toHaveBeenCalledTimes(2))
  current = undefined
  ready({ status: 'bound', runId: 'stale-run' })
  expect(await (await revoked).json()).toMatchObject({
    result: { value: { binding: { status: 'access-denied' }, config: null } },
  })
})
