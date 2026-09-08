import { describe, expect, it } from 'vitest'
import { eventRun } from '../src/protocol'
import { runKey, windowStore } from '../src/client/window-store'

describe('standard-event workflow presentation', () => {
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
})
