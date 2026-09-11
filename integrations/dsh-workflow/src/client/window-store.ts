import type { RunLink } from '../protocol'
import type { WindowRect } from './window-geometry'

export interface WindowEntry { run: RunLink; minimized: boolean; layout?: WindowRect; anchor: number }
interface Windows { entries: Readonly<Record<string, WindowEntry>>; firstCards: Readonly<Record<string, number>> }

export function runKey(run: RunLink) { return `${run.hostSessionId}\0${new URL(run.url).origin}\0${run.runId}` }

/** Per-plugin presentation state. It never binds, confirms, stops or resumes a workflow. */
export function windowStore() {
  let state: Windows = { entries: {}, firstCards: {} }
  const listeners = new Set<() => void>()
  const publish = (next: Windows) => { state = next; for (const listener of listeners) listener() }
  const update = (id: string, entry: WindowEntry) => publish({ ...state, entries: { ...state.entries, [id]: entry } })
  return {
    snapshot: () => state,
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    observe(run: RunLink, anchor: number) {
      if (!run.hostSessionId) return
      const key = runKey(run)
      const first = state.firstCards[key]
      const current = state.entries[run.hostSessionId]
      const shouldOpen = !current || run.operation === 'start' && run.runId !== current.run.runId && anchor > current.anchor
      if (first === undefined || anchor < first || shouldOpen) publish({
        firstCards: { ...state.firstCards, [key]: first === undefined ? anchor : Math.min(first, anchor) },
        entries: shouldOpen ? { ...state.entries, [run.hostSessionId]: { run, minimized: false, anchor, layout: current?.layout } } : state.entries,
      })
    },
    open(run: RunLink, anchor: number) {
      if (!run.hostSessionId) return
      const prev = state.entries[run.hostSessionId]
      update(run.hostSessionId, { run, minimized: false, anchor, layout: prev?.layout })
    },
    minimize(id: string) { const entry = state.entries[id]; if (entry) update(id, { ...entry, minimized: true }) },
    place(id: string, layout: WindowRect) { const entry = state.entries[id]; if (entry) update(id, { ...entry, layout }) },
    dispose() { listeners.clear(); state = { entries: {}, firstCards: {} } },
  }
}
