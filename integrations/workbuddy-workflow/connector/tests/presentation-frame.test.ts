import { expect, it, vi } from 'vitest'
import { JSDOM } from 'jsdom'
import { startPresentationFrame } from '../src/presentation-frame'

it.each([
  ['http://127.0.0.1:18090', 'http://127.0.0.1:18090/workflow-runs/run-1/embed'],
  ['https://panel.example.test/lazymind/', 'https://panel.example.test/lazymind/workflow-runs/run-1/embed'],
])('uses configured frontend %s and correlates messages with its own page', async (frontend, expectedURL) => {
  const mount = crypto.randomUUID()
  const dom = new JSDOM(`<meta name="lazymind-workflow-frontend" content="${frontend}"><p id="notice"></p><iframe id="workflow"></iframe>`, {
    url: `http://127.0.0.1:19091/v1/workbuddy/frame#run=run-1&mount=${mount}`, runScripts: 'outside-only',
  })
  const window = dom.window
  const parentMessages: any[] = [], childMessages: any[] = []
  const parent = { postMessage: (...args: any[]) => parentMessages.push(args) }
  Object.defineProperty(window, 'parent', { value: parent })
  ;(window as any).fetch = vi.fn().mockResolvedValue({})
  try {
    window.eval(`(${startPresentationFrame.toString()})()`)
    const frame = window.document.querySelector('iframe')!
    await vi.waitFor(() => expect(frame.src).toContain('/workflow-runs/run-1/embed'))
    parentMessages.length = 0
    const url = new URL(frame.src)
    expect(url.origin + url.pathname).toBe(expectedURL)
    expect((window as any).fetch).toHaveBeenCalledWith(url.origin + '/', expect.anything())
    expect(url.searchParams.get('hostOrigin')).toBe('http://127.0.0.1:19091')
    expect(url.searchParams.has('workbuddyFrameClock')).toBe(false)
    frame.contentWindow!.postMessage = (...args: any[]) => childMessages.push(args)
    const message = (data: any, source: any, origin: string) => window.dispatchEvent(new window.MessageEvent('message', { data, source, origin }))
    const command = { type: 'lazymind.workflow.toggle-expand', sessionId: 'run-1' }
    message(command, frame.contentWindow, 'http://evil.test')
    message(command, frame.contentWindow, 'http://localhost:8090')
    message({ ...command, sessionId: 'run-other' }, frame.contentWindow, url.origin)
    expect(parentMessages).toHaveLength(0)
    message(command, frame.contentWindow, url.origin)
    expect(parentMessages).toEqual([[{ ...command, bridgeId: mount }, '*']])
    const reply = { type: 'lazymind.workflow.expansion', expanded: true, sessionId: 'run-1', bridgeId: mount }
    message(reply, window, 'null')
    message({ ...reply, bridgeId: 'old-mount' }, parent, 'null')
    expect(childMessages).toHaveLength(0)
    message(reply, parent, 'null')
    expect(childMessages).toEqual([[reply, url.origin]])
    const requestId = crypto.randomUUID()
    const saved = { type: 'lazymind.workflow.download.result', sessionId: 'run-1', bridgeId: mount, requestId, ok: false }
    message(saved, parent, 'null')
    expect(childMessages).toHaveLength(1)
    message({ type: 'lazymind.workflow.download', bridgeId: mount, requestId, action: 'capabilities' }, frame.contentWindow, url.origin)
    message(saved, parent, 'null')
    expect(childMessages).toHaveLength(2)
    message(saved, parent, 'null')
    expect(childMessages).toHaveLength(2)
  } finally { dom.window.close() }
})

it('shows an unavailable frontend and retries initial loading without adding draft or save behavior', async () => {
  const dom = new JSDOM('<meta name="lazymind-workflow-frontend" content="http://localhost:18091"><p id="notice"></p><button id="retry" hidden>Retry</button><iframe id="workflow"></iframe>', {
    url: `http://127.0.0.1:19091/v1/workbuddy/frame#run=run-1&mount=${crypto.randomUUID()}`, runScripts: 'outside-only',
  })
  const w = dom.window as any
  const messages: any[] = []
  Object.defineProperty(w, 'parent', { value: { postMessage: (data: any) => messages.push(data) } })
  w.fetch = vi.fn().mockRejectedValueOnce(Error('offline')).mockResolvedValue({})
  try {
    w.eval(`(${startPresentationFrame.toString()})()`)
    await vi.waitFor(() => expect(messages.at(-1)?.status).toBe('frontend-unavailable'))
    const frame = w.document.querySelector('iframe')
    expect(frame.getAttribute('src')).toBeNull()
    expect(w.document.querySelector('#retry').hidden).toBe(false)
    w.document.querySelector('#retry').click()
    await vi.waitFor(() => expect(frame.src).toContain('/workflow-runs/run-1/embed'))
    frame.dispatchEvent(new w.Event('load'))
    expect(messages.at(-1)?.status).toBe('ready')
    expect(w.document.querySelector('#notice').hidden).toBe(true)
    expect(w.fetch).toHaveBeenCalledTimes(2)
    expect(w.fetch.mock.calls.every(([url]: string[]) => url === 'http://localhost:18091/')).toBe(true)
    w.dispatchEvent(new w.Event('pagehide'))
    w.document.querySelector('#retry').click()
    expect(w.fetch).toHaveBeenCalledTimes(2)
  } finally { w.close() }
})
