import { Context } from '@deepseek-ai/cordis'
import type { IndexInjection } from '@deepseek-ai/dsh-host-webserver'
import { describe, expect, it } from 'vitest'
import { renderIndexInjections } from '@deepseek-ai/dsh-host-webserver'
import { installPresentationConfig, readPresentationConfig } from '../src/presentation-config'
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
})
