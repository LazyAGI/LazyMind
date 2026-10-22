import { describe, expect, it } from 'vitest'
import { eventRun } from '../src/protocol'
import { runKey, windowStore } from '../src/client/window-store'

describe('standard-event workflow presentation', () => {
  it('builds panel links from configured frontend and URL-free control results', () => {
    const control = { protocol: 'workflow.control.v1', session_id: 'run-1', state_version: 3, continuation: 'awaiting_user', admission: { can_begin: false } }
    for (const result of [{ session_id: 'run-1', control }, { state: { control } }]) {
      const event = { type: 'tool/result', data: { content: [{ type: 'text', text: JSON.stringify(result) }] } }
      expect(eventRun(event, 'lazymind', 'https://configured.example')).toEqual({ runId: 'run-1', url: 'https://configured.example/workflow-runs/run-1' })
      expect(eventRun(event, 'lazymind', '')).toBeNull()
    }
    const event = { type: 'tool/result', data: { meta: { lazymind_workflow: {
      runId: 'run-1', hostSessionId: 'native-1', operation: 'start', url: 'https://untrusted.example/panel',
    } } } }
    expect(eventRun(event, 'lazymind', 'https://configured.example')).toEqual({ runId: 'run-1', url: 'https://configured.example/workflow-runs/run-1', hostSessionId: 'native-1', operation: 'start' })
  })
  it('restores historical URL receipts while new results use host configuration', () => {
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
    expect(eventRun(event, 'lazymind', 'http://localhost:8090')).toEqual({
      runId: 'mcp-0064515ead492c9266272cfeb69fa80f',
      url: 'http://localhost:8090/workflow-runs/mcp-0064515ead492c9266272cfeb69fa80f',
    })
    expect(eventRun(event, 'lazymind', '')).toBeNull()
    expect(eventRun({ type: 'tool/result', data: { message: { content: [{ type: 'text', text: '{"ok":true}' }] } } }, 'lazymind', 'http://localhost:8090')).toBeNull()
    const bulky = { padding: 'x'.repeat(9000), session_id: 'mcp-0064515ead492c9266272cfeb69fa80f',
      interaction_url: 'http://localhost:8090/workflow-runs/mcp-0064515ead492c9266272cfeb69fa80f' }
    expect(eventRun({ type: 'tool/result', data: { message: { content: [{ type: 'text', text: JSON.stringify(bulky) }] } } }, 'lazymind', 'http://localhost:8090')?.runId)
      .toBe('mcp-0064515ead492c9266272cfeb69fa80f')
  })
  it('decodes PTC only from successful LazyMind workflow dispatches', () => {
    const event = { type: 'tool/ptc-dispatch', data: { isError: false, name: 'mcp__lazymind__workflow_start', content: [
      { type: 'text', text: JSON.stringify({ lazymind_workflow: { runId: 'r', hostSessionId: 's' } }) },
    ] } }
    expect(eventRun(event, 'lazymind', 'http://localhost:8090')?.runId).toBe('r')
    expect(eventRun({ ...event, data: { ...event.data, name: 'mcp__other__workflow_start' } }, 'lazymind', 'http://localhost:8090')).toBeNull()
    expect(eventRun({ ...event, data: { ...event.data, isError: true } }, 'lazymind', 'http://localhost:8090')).toBeNull()
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
  it('keeps the active run identity on later observations and reopens it explicitly', () => {
    const store = windowStore()
    const run = { runId: 'r', url: 'http://localhost:8090/workflow-runs/r', hostSessionId: 'a', operation: 'start' }
    store.observe(run, 20)
    const entry = store.snapshot().entries.a
    store.observe({ ...run, operation: 'state' }, 40)
    expect(store.snapshot().entries.a).toBe(entry)
    store.minimize('a')
    store.open(run, 20)
    expect(store.snapshot().entries.a).toMatchObject({ minimized: false, run })
    store.observe({ ...run, runId: 'second', url: 'http://localhost:8090/workflow-runs/second' }, 80)
    expect(store.snapshot().entries.a.run.runId).toBe('second')
  })
})
