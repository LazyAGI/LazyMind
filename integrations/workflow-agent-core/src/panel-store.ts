import type { RunLink } from './protocol'

export interface WindowEntry { run: RunLink; minimized: boolean; source: 'manual' | 'binding' }
interface Windows { entries: Readonly<Record<string, WindowEntry>>; firstCards: Readonly<Record<string, number>> }

export function runKey(run: RunLink) { return `${run.hostSessionId}\0${new URL(run.url).origin}\0${run.runId}` }

/** Per-plugin presentation state. It never binds, confirms, stops or resumes a workflow. */
export function windowStore() {
  let state: Windows = { entries: {}, firstCards: {} }
  const listeners = new Set<() => void>()
  const publish = (next: Windows) => { state = next; for (const listener of listeners) listener() }
  const update = (id: string, entry: WindowEntry) => publish({ ...state, entries: { ...state.entries, [id]: entry } })
  const entry = (sessionId: string) => Object.hasOwn(state.entries, sessionId) ? state.entries[sessionId] : undefined
  const close = (sessionId: string) => {
    if (!entry(sessionId)) return
    const entries = { ...state.entries }
    delete entries[sessionId]
    publish({ ...state, entries })
  }
  return {
    snapshot: () => state,
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    observe(run: RunLink, anchor: number) {
      if (!run.hostSessionId) return
      const key = runKey(run)
      const first = state.firstCards[key]
      if (first === undefined || anchor < first) publish({
        ...state, firstCards: { ...state.firstCards, [key]: anchor },
      })
    },
    open(run: RunLink) {
      if (run.hostSessionId) update(run.hostSessionId, { run, minimized: false, source: 'manual' })
    },
    binding(sessionId: string, run: RunLink | null) {
      const current = entry(sessionId)
      if (current?.source === 'manual') return
      if (!run) { close(sessionId); return }
      if (current && runKey(current.run) === runKey(run)) return
      update(sessionId, { run, minimized: false, source: 'binding' })
    },
    close,
    clear() { if (Object.keys(state.entries).length > 0) publish({ ...state, entries: {} }) },
    minimize(id: string) { const current = entry(id); if (current) update(id, { ...current, minimized: true }) },
    dispose() { listeners.clear(); state = { entries: {}, firstCards: {} } },
  }
}
