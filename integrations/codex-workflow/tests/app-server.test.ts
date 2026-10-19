import { createServer } from 'node:http'
import { randomUUID } from 'node:crypto'
import { unlink, mkdtemp, symlink, realpath, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { WebSocketServer, type WebSocket } from 'ws'
import { AppServer, RpcError, RpcUnavailable } from '../src/app-server'
import { connectionTarget } from '../desktop-runtime/endpoint.mjs'

const identity = { userAgent: 'codex/0.162.0-alpha.17.2', codexHome: '/profile', platformFamily: 'unix', platformOs: 'macos' }
const cleanups: Array<() => Promise<void>> = []
afterEach(async () => { for (const cleanup of cleanups.splice(0).reverse()) await cleanup() })
async function fixture(handler: (message: any, ws: WebSocket) => void = () => {}, initialize = true) {
  const path = `/tmp/lm-rpc-${randomUUID()}.sock`
  const server = createServer()
  const wss = new WebSocketServer({ server, path: '/rpc' })
  // HTTP listen errors are rejected below; ws also forwards them on its emitter.
  wss.on('error', () => {})
  const messages: any[] = []
  wss.on('connection', ws => ws.on('message', data => {
    const message = JSON.parse(data.toString()); messages.push(message)
    if (message.method === 'initialize' && initialize) ws.send(JSON.stringify({ id: message.id, result: identity }))
    else handler(message, ws)
  }))
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(path, resolve) })
  cleanups.push(async () => {
    for (const ws of wss.clients) ws.terminate()
    await new Promise<void>(resolve => wss.close(() => resolve()))
    await new Promise<void>(resolve => server.close(() => resolve()))
    await unlink(path).catch(() => {})
  })
  const endpoint = `ws+unix://localhost${path}:/rpc`
  const connect = async (options: Parameters<typeof AppServer.connect>[1] = {}) => {
    const connected = await AppServer.connect(endpoint, options)
    cleanups.push(async () => connected.client.close())
    return connected
  }
  return { connect, messages, endpoint }
}

describe('App Server RPC over a real Unix Socket', () => {
  it('accepts the same native profile through a symlink during initialization', async () => {
    const home = await mkdtemp(join(tmpdir(), 'lm-rpc-profile-'))
    cleanups.push(() => rm(home, { recursive: true, force: true }))
    const alias = join(home, 'alias')
    await symlink(await realpath(home), alias, 'junction')
    const f = await fixture((message, ws) => {
      if (message.method === 'initialize') ws.send(JSON.stringify({ id: message.id, result: { ...identity, codexHome: home } }))
    }, false)
    await f.connect({ expectedHome: alias })
  })
  it('initializes once and dispatches concurrent responses by request ID', async () => {
    const requests: Array<{ message: any; ws: WebSocket }> = []
    const f = await fixture((message, ws) => {
      if (!message.id) return
      requests.push({ message, ws })
      if (requests.length === 2) for (const { message: m, ws: w } of requests.reverse()) w.send(JSON.stringify({ id: m.id, result: m.params }))
    })
    const { client, identity: actual } = await f.connect({ expectedHome: '/profile' })
    expect(actual).toEqual(identity)
    expect(await Promise.all([client.request('one', { n: 1 }), client.request('two', { n: 2 })])).toEqual([{ n: 1 }, { n: 2 }])
    expect(f.messages.map(m => m.method)).toEqual(['initialize', 'initialized', 'one', 'two'])
    expect(f.messages[0].params.capabilities.experimentalApi).toBe(false)
  })
  it('routes native notifications and approval requests separately without answering them', async () => {
    const f = await fixture((message, ws) => {
      if (message.method !== 'watch') return
      ws.send(JSON.stringify({ method: 'turn/completed', params: { threadId: 'thread', turn: { id: 'turn', status: 'interrupted' } } }))
      ws.send(JSON.stringify({ id: 99, method: 'item/tool/requestUserInput', params: { threadId: 'thread' } }))
      ws.send(JSON.stringify({ id: message.id, result: {} }))
    })
    const { client } = await f.connect()
    const notifications: unknown[] = [], requests: unknown[] = []
    client.on('notification', value => notifications.push(value)); client.on('serverRequest', value => requests.push(value))
    await client.request('watch', {})
    expect(notifications).toHaveLength(1); expect(requests).toHaveLength(1)
    expect(f.messages.some(m => m.id === 99)).toBe(false)
    client.respond(99, { answers: {} })
  })
  it('preserves an explicit RPC rejection rather than interpreting it as acceptance', async () => {
    const f = await fixture((message, ws) => { if (message.id) ws.send(JSON.stringify({ id: message.id, error: { code: -32602, message: 'stale turn' } })) })
    const { client } = await f.connect()
    await expect(client.request('turn/interrupt', {})).rejects.toMatchObject({ code: -32602, message: 'stale turn' })
    expect(RpcError.prototype).toBeInstanceOf(Error)
  })
  it('marks a timed-out write uncertain and never resends it', async () => {
    const f = await fixture()
    const { client } = await f.connect({ timeoutMs: 40 })
    await expect(client.request('turn/start', { threadId: 'thread' })).rejects.toMatchObject({ sent: true })
    expect(f.messages.filter(m => m.method === 'turn/start')).toHaveLength(1)
  })
  it('rejects disconnected pending requests and does not reconnect automatically', async () => {
    const f = await fixture((message, ws) => { if (message.method === 'turn/start') ws.terminate() })
    const { client } = await f.connect()
    await expect(client.request('turn/start', {})).rejects.toMatchObject({ sent: true })
    await expect(client.request('turn/start', {})).rejects.toMatchObject({ sent: false })
    expect(f.messages.filter(m => m.method === 'turn/start')).toHaveLength(1)
  })
  it('distinguishes an already-aborted call from an abort after writing', async () => {
    const f = await fixture()
    const { client } = await f.connect()
    await expect(client.request('before', {}, AbortSignal.abort())).rejects.toMatchObject({ sent: false })
    const abort = new AbortController()
    const request = client.request('after', {}, abort.signal)
    abort.abort()
    await expect(request).rejects.toMatchObject({ sent: true })
    expect(f.messages.some(m => m.method === 'before')).toBe(false)
  })
  it('closes on malformed messages and releases pending requests', async () => {
    const f = await fixture((message, ws) => { if (message.method === 'bad') ws.send('not JSON') })
    const { client } = await f.connect()
    await expect(client.request('bad', {})).rejects.toMatchObject({ sent: true })
  })
  it('bounds initialization and rejects another profile', async () => {
    const silent = await fixture(() => {}, false)
    await expect(silent.connect({ timeoutMs: 40 })).rejects.toMatchObject({ sent: true })
    const f = await fixture()
    await expect(f.connect({ expectedHome: '/different-profile' })).rejects.toThrow('another Codex profile')
  })
  it('reads without resuming and refuses a mismatched native thread', async () => {
    const f = await fixture((message, ws) => { if (message.id) ws.send(JSON.stringify({ id: message.id, result: { thread: { id: 'other', status: { type: 'idle' } } } })) })
    const { client } = await f.connect()
    await expect(client.readThread('thread')).rejects.toThrow('another or invalid thread')
    expect(f.messages.some(m => m.method === 'thread/resume')).toBe(false)
  })
  it('uses exact turn targets and pinned input fields', async () => {
    const f = await fixture((message, ws) => { if (message.id) ws.send(JSON.stringify({ id: message.id, result: {} })) })
    const { client } = await f.connect()
    await client.startTurn('thread', 'read Core', 'action')
    await client.steerTurn('thread', 'turn-1', 'read Core', 'action-2')
    await client.interruptTurn('thread', 'turn-1')
    expect(f.messages.find(m => m.method === 'turn/start').params).toEqual({ threadId: 'thread', input: [{ type: 'text', text: 'read Core' }], clientUserMessageId: 'action' })
    expect(f.messages.find(m => m.method === 'turn/steer').params.expectedTurnId).toBe('turn-1')
    expect(f.messages.find(m => m.method === 'turn/interrupt').params).toEqual({ threadId: 'thread', turnId: 'turn-1' })
  })
  it('rejects non-local endpoints before attempting a connection', () => {
    for (const value of ['ws://localhost:4500', 'ws+unix://remote/tmp/server.sock:/rpc', 'ws+unix://localhost/tmp/a.sock:/rpc?token=secret', 'ws+unix://localhost/tmp/%00.sock:/rpc']) {
      expect(() => connectionTarget(value)).toThrow()
    }
    expect(connectionTarget('ws+unix://localhost/tmp/server.sock:/rpc').socketPath).toBe('/tmp/server.sock')
    expect(RpcUnavailable.prototype).toBeInstanceOf(Error)
  })
})
