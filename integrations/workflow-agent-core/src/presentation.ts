import { setTimeout as delay } from 'node:timers/promises'
import type { BindingResult, LookupBinding, PanelTarget, PanelView, UIAdapter, WatchView } from './adapter'

export interface PresentationStatus {
  status: string
  page_status?: string
  checked_at: number
  native_session_id?: string
  run_id?: string
}

const bindingRefreshMs = 5000
const layoutRetryMs = 100

/** The caller owns the window and its renderer cleanup. */
export async function runPresentation({ watchView, lookupBinding, ui, frameUrl, signal, report }: {
  watchView: WatchView
  lookupBinding: LookupBinding
  ui: UIAdapter
  frameUrl: string
  signal: AbortSignal
  report: (status: PresentationStatus) => void
}): Promise<void> {
  let current: { view: PanelView; stop: AbortController } | undefined

  async function synchronize(view: PanelView, clear: boolean, active: AbortSignal) {
    function publish(status: string, runId?: string, pageStatus?: string) {
      if (!active.aborted) report({ status, checked_at: Date.now(), native_session_id: view.chatId ?? undefined,
        run_id: runId, page_status: pageStatus })
    }

    async function apply(target: PanelTarget | null, statusForUnmount?: string, retryUntil = 0) {
      do {
        if (active.aborted) return
        try {
          const result = await ui.syncPanel(view, target, active)
          if (result.status !== 'stale') {
            const status = result.issue ?? (result.status === 'unmounted' ? statusForUnmount ?? 'unmounted'
              : result.status === 'reused' ? 'mounted' : result.status)
            publish(status, target?.runId, result.pageStatus)
          }
          if (result.status !== 'layout-unavailable' || result.issue) return
        } catch {
          publish('renderer-unavailable', target?.runId)
          return
        }
        if (Date.now() >= retryUntil || active.aborted) return
        await delay(layoutRetryMs, undefined, { signal: active })
      } while (!active.aborted)
    }

    try {
      if (clear || view.chatId === null) await apply(null, view.chatId === null ? 'waiting-session' : undefined)
      if (view.chatId === null) return
      while (!active.aborted) {
        let binding: BindingResult
        try { binding = await lookupBinding(view.chatId, active) }
        catch { binding = { status: 'unavailable' } }
        if (active.aborted) return
        const refreshAt = Date.now() + bindingRefreshMs
        if (binding.status === 'bound') await apply({ runId: binding.runId, frameUrl }, undefined, refreshAt)
        else if (binding.status === 'unavailable') publish('binding-unavailable')
        else await apply(null, binding.status)
        if (!active.aborted) await delay(Math.max(0, refreshAt - Date.now()), undefined, { signal: active })
      }
    } catch (error) {
      if (!active.aborted) throw error
    }
  }

  try {
    await watchView(view => {
      if (signal.aborted || current?.view.epoch === view.epoch && current.view.chatId === view.chatId) return
      const clear = current !== undefined && current.view.chatId !== view.chatId
      current?.stop.abort()
      const stop = new AbortController()
      current = { view, stop }
      const active = AbortSignal.any([signal, stop.signal])
      void synchronize(view, clear, active).catch(() => {
        if (!active.aborted) report({ status: 'unavailable', checked_at: Date.now(), native_session_id: view.chatId ?? undefined })
      })
    }, signal)
  } finally {
    current?.stop.abort()
  }
}
