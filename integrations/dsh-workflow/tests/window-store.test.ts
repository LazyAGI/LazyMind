import { describe, expect, it } from 'vitest'
import { eventRun } from '../src/protocol'
import { runKey, windowStore } from '../src/client/window-store'

describe('standard-event workflow presentation', () => {
  it('reads interaction_url from DSH tool-result message text', () => {
    const event = {
      type: 'tool/result',
      seq: 1510,
      data: {
        turn: 1, step: 4,
        message: {
          source: { kind: 'tool', callId: 'call_1' },
          content: [{
            type: 'tool-result', toolCallId: 'call_1',
            content: [{
              type: 'text',
              text: JSON.stringify({
                session_id: 'mcp-0064515ead492c9266272cfeb69fa80f',
                interaction_url: 'http://localhost:8090/workflow-runs/mcp-0064515ead492c9266272cfeb69fa80f',
              }),
            }],
          }],
        },
      },
    }
    expect(eventRun(event, 'lazymind')).toEqual({
      runId: 'mcp-0064515ead492c9266272cfeb69fa80f',
      url: 'http://localhost:8090/workflow-runs/mcp-0064515ead492c9266272cfeb69fa80f',
    })
    expect(eventRun({ type: 'tool/result', data: { message: { content: [{ type: 'text', text: '{"ok":true}' }] } } }, 'lazymind')).toBeNull()
    const bulky = { padding: 'x'.repeat(9000), session_id: 'mcp-0064515ead492c9266272cfeb69fa80f',
      interaction_url: 'http://localhost:8090/workflow-runs/mcp-0064515ead492c9266272cfeb69fa80f' }
    expect(eventRun({ type: 'tool/result', data: { message: { content: [{ type: 'text', text: JSON.stringify(bulky) }] } } }, 'lazymind')?.runId)
      .toBe('mcp-0064515ead492c9266272cfeb69fa80f')
  })
  it('decodes PTC only from successful LazyMind workflow dispatches', () => {
    const event = { type: 'tool/code-dispatch', data: { isError: false, name: 'mcp__lazymind__workflow_start', content: [
      { type: 'text', text: JSON.stringify({ lazymind_workflow: { runId: 'r', url: 'http://localhost:8090/workflow-runs/r', hostSessionId: 's' } }) },
    ] } }
    expect(eventRun(event, 'lazymind')?.runId).toBe('r')
    expect(eventRun({ ...event, data: { ...event.data, name: 'mcp__other__workflow_start' } }, 'lazymind')).toBeNull()
    expect(eventRun({ ...event, data: { ...event.data, isError: true } }, 'lazymind')).toBeNull()
  })
  it('isolates sessions, deduplicates entry cards and does not reopen a minimized workflow on state reads', () => {
    const store = windowStore()
    const run = { runId: 'r', url: 'http://localhost:8090/workflow-runs/r', hostSessionId: 'a', operation: 'start' }
    store.observe(run, 20)
    store.observe({ ...run, operation: 'state' }, 30)
    expect(store.snapshot().firstCards[runKey(run)]).toBe(20)
    store.minimize('a')
    store.observe({ ...run, operation: 'state' }, 40)
    expect(store.snapshot().entries.a.minimized).toBe(true)
    store.observe({ ...run, runId: 'second', url: 'http://localhost:8090/workflow-runs/second', hostSessionId: 'b' }, 50)
    expect(store.snapshot().entries.a.run.runId).toBe('r')
    expect(store.snapshot().entries.b.run.runId).toBe('second')
    store.dispose()
    expect(store.snapshot().entries).toEqual({})
  })
  it('keeps the panel layout after minimize, reopen and a later start in the same session', () => {
    const store = windowStore()
    const run = { runId: 'r', url: 'http://localhost:8090/workflow-runs/r', hostSessionId: 'a', operation: 'start' }
    const layout = { left: 40, top: 80, width: 900, height: 640 }
    store.observe(run, 20)
    store.place('a', layout)
    store.minimize('a')
    store.open(run, 20)
    expect(store.snapshot().entries.a).toMatchObject({ minimized: false, layout })
    store.observe({ ...run, runId: 'second', url: 'http://localhost:8090/workflow-runs/second', operation: 'start' }, 80)
    expect(store.snapshot().entries.a.layout).toEqual(layout)
    expect(store.snapshot().entries.a.run.runId).toBe('second')
    store.dispose()
  })
})
