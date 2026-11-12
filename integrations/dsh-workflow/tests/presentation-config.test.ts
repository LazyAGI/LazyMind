import { Context } from '@deepseek-ai/cordis'
import type { IndexInjection, WebRoute } from '@deepseek-ai/dsh-host-webserver'
import { describe, expect, it, vi } from 'vitest'
import { renderIndexInjections } from '@deepseek-ai/dsh-host-webserver'
import { installPresentationConfig, readPresentationConfig, observePresentationConfig } from '../src/presentation-config'
import { eventRun } from '../src/events'

describe('browser presentation configuration', () => {
  it('passes only public config through the DSH page hook and renders URL-free results', async () => {
    const ctx = new Context()
    const config = { serverName: 'lazymind', webUrl: 'https://frontend.example',
      pairingFile: '/private/pairing.json', secret: 'not-for-browser' }
    const plugin = ctx.plugin({ apply: (scope: Context) => installPresentationConfig(scope, config) })
    await plugin.await()
    const rows: IndexInjection[] = []
    ctx.emit('webserver/index-inject', rows)
    expect(rows).toHaveLength(1)
    const row = rows[0]
    if (row.kind !== 'global') throw new Error('Expected public browser config')
    const browser = readPresentationConfig({ [row.name]: row.value })
    expect(browser).toEqual({ serverName: 'lazymind', webUrl: 'https://frontend.example' })
    const html = renderIndexInjections('<html><head></head><body></body></html>', rows)
    expect(html).toContain('https://frontend.example')
    expect(html).not.toContain('pairing')
    expect(html).not.toContain('not-for-browser')
    const control = { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 1,
      continuation: 'awaiting_user', admission: { can_begin: false } }
    expect(eventRun({ type: 'tool/result', data: { content: [{ type: 'text', text: JSON.stringify({ control }) }] } },
      browser.serverName, browser.webUrl)).toEqual({ runId: 'run-1', url: 'https://frontend.example/workflow-runs/run-1' })
    await plugin.dispose()
    const after: IndexInjection[] = []
    ctx.emit('webserver/index-inject', after)
    expect(after).toEqual([])
  })

  it('reports missing configuration instead of trusting a tool-provided URL', () => {
    expect(() => readPresentationConfig({})).toThrow('reload DSH')
  })

  it('serves only the latest public configuration and removes the route on disposal', async () => {
    const ctx = new Context()
    let route!: WebRoute
    const unregister = vi.fn()
    ctx.provide('webServer', { register: (value: WebRoute) => { route = value; return unregister } } as never)
    let config: { serverName: string; webUrl: string; secret?: string } | undefined = {
      serverName: 'lazymind', webUrl: 'http://localhost:8090', secret: 'private-pairing',
    }
    const plugin = ctx.plugin({ apply: (scope: Context) => installPresentationConfig(scope, () => config) })
    try {
      await plugin.await()
      const reply = async () => {
        let body = ''
        const response = { setHeader: vi.fn(), end: (value: string) => { body = value } }
        await route.handler({ method: 'GET' } as never, response as never)
        expect(response.setHeader).toHaveBeenCalledWith('Cache-Control', 'no-store')
        return JSON.parse(body)
      }
      expect(await reply()).toEqual({ config: { serverName: 'lazymind', webUrl: 'http://localhost:8090' } })
      config = undefined
      expect(await reply()).toEqual({ config: null })
    } finally { await plugin.dispose() }
    expect(unregister).toHaveBeenCalledTimes(1)
  })

  it('boots without browser config and adopts the host config when login recovers', async () => {
    vi.stubGlobal('location', { protocol: 'http:' })
    const update = vi.fn()
    const request = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ config: { serverName: 'lazymind', webUrl: 'http://localhost:8090' } }) })
    const stop = observePresentationConfig({}, update, request, 2)
    try {
      expect(update).toHaveBeenCalledWith(undefined)
      await vi.waitFor(() => expect(update).toHaveBeenCalledWith({ serverName: 'lazymind', webUrl: 'http://localhost:8090' }))
    } finally { stop(); vi.unstubAllGlobals() }
    const count = update.mock.calls.length
    await new Promise(resolve => setTimeout(resolve, 10))
    expect(update).toHaveBeenCalledTimes(count)
  })
})
