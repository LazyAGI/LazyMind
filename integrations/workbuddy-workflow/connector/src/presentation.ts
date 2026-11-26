import { workbuddyVersionIssue } from './compatibility'
import type { LookupBinding, UIAdapter, WatchView } from '../../../workflow-agent-core/src/adapter'
import { runPresentation, type PresentationStatus } from '../../../workflow-agent-core/src/presentation'
import type { Cdp } from './cdp'
import { setTimeout as delay } from 'node:timers/promises'
import { installTailFollow } from './tail-follow'
import { installTailExtent } from './tail-extent'

export type { PresentationStatus } from '../../../workflow-agent-core/src/presentation'
export interface PresentationTarget { session_id: string | null; run_id: string; frame_url: string; mount_id: string; epoch?: string }

// Detect A -> B -> A as well as renderer recreation while a lookup is pending.
// This token guards presentation freshness only; it is never a binding source.
export function readPresentationSession(): { session_id: string | null; epoch: string } | null {
  const host = window as any, sdk = host.wb?.conversations
  const key = '__lazymindWorkflowPresentationSession'
  const usable = sdk && typeof sdk.onCurrentChange === 'function'
  if (host[key] && (!usable || host[key].sdk !== sdk)) {
    host.__lazymindWorkflowTail?.dispose()
    host[key].dispose()
  }
  if (!usable) return null
  if (!host[key]) {
    let session = sdk.currentId || null, epoch = crypto.randomUUID()
    const waiters = new Set<() => void>()
    const changed = () => {
      session = sdk.currentId || null; epoch = crypto.randomUUID()
      for (const resolve of waiters) resolve()
    }
    const unsubscribe = sdk.onCurrentChange(changed)
    const gate = {
      sdk,
      read: () => { if (session !== (sdk.currentId || null)) changed(); return { session_id: session, epoch } },
      wait: (expected: string) => {
        if (gate.read().epoch !== expected) return Promise.resolve()
        return new Promise<void>(resolve => {
          const finish = () => { clearTimeout(timer); waiters.delete(finish); resolve() }
          const timer = setTimeout(finish, 5000)
          waiters.add(finish)
        })
      },
      dispose: () => {
        if (typeof unsubscribe === 'function') unsubscribe()
        for (const resolve of waiters) resolve()
        if (host[key] === gate) delete host[key]
      },
    }
    host[key] = gate
  }
  return host[key].read()
}

export const cleanupPresentation = 'window.__lazymindWorkflowTail?.dispose(); window.__lazymindWorkflowPresentationSession?.dispose()'

// Serialized into the renderer: keep this function independent of module scope.
export function mountPresentation(target: PresentationTarget, followTail = installTailFollow, fitTail = installTailExtent, versionIssue = workbuddyVersionIssue) {
  const host = window as any
  const version = 'tail-v4'
  const key = '__lazymindWorkflowTail'
  const previous = host[key]
  const wb = host.wb
  const conversations = wb?.conversations
  const gate = host.__lazymindWorkflowPresentationSession
  if (target.epoch && (gate?.sdk !== conversations || gate?.read().epoch !== target.epoch)) return { status: 'stale' }
  if (!conversations) return { status: 'page-not-ready' }
  if ((conversations.currentId || null) !== target.session_id) return { status: 'stale' }
  if (!target.run_id) { previous?.dispose(); return { status: 'unbound' } }
  const issue = versionIssue(host.workbuddyDesktop?.appVersion)
  if (issue) { previous?.dispose(); return { status: issue } }
  if (typeof conversations.onCurrentChange !== 'function') { previous?.dispose(); return { status: 'capability_unavailable' } }
  const url = new URL(target.frame_url)
  if (url.protocol !== 'http:' || !['localhost', '127.0.0.1'].includes(url.hostname)
    || url.username || url.password || url.pathname !== '/v1/workbuddy/frame' || url.search || url.hash
    || !/^[a-zA-Z0-9_-]{1,256}$/.test(target.run_id) || !/^[a-zA-Z0-9-]{20,80}$/.test(target.mount_id)) throw Error('Invalid presentation target')
  if (previous?.version === version && previous.session === target.session_id && previous.run === target.run_id
    && previous.origin === url.origin && previous.epoch === target.epoch) {
    previous.sync()
    const state = previous.status()
    return state.status === 'mounted' ? { ...state, status: 'reused' } : state
  }
  previous?.dispose()
  host.__lazymindTailPanelDemo?.dispose()
  let cachedDoc: HTMLElement | undefined
  const locate = () => {
    if (!current()) return null
    const doc = cachedDoc?.isConnected && cachedDoc.dataset.rootId === target.session_id && cachedDoc.getBoundingClientRect().width > 0
      ? cachedDoc : Array.from(document.querySelectorAll<HTMLElement>('.cr-document[data-root-id]'))
        .find(e => e.dataset.rootId === target.session_id && e.getBoundingClientRect().width > 0)
    const content = doc?.parentElement
    if (!doc || doc.getBoundingClientRect().width <= 0 || !content?.classList.contains('cr-message-list__content')
      || !content.parentElement?.classList.contains('cr-message-list')) return null
    cachedDoc = doc
    return { doc, content }
  }
  const initial = locate()
  if (!initial) return { status: 'layout-unavailable' }
  const panel = document.createElement('section')
  panel.dataset.lazymindWorkflowTail = initial.doc.dataset.rootId!
  panel.setAttribute('aria-label', 'LazyMind Workflow')
  panel.style.cssText = 'box-sizing:border-box;position:relative;margin:16px auto 24px;width:100%;overflow:hidden;border-radius:12px;'
  // Keep the iframe in the same DOM subtree. A manual popover enters the top
  // layer without reparenting/reloading it or inheriting timeline clipping.
  const surface = document.createElement('div')
  surface.dataset.lazymindWorkflowSurface = ''
  const inlineStyle = 'box-sizing:border-box;display:block;width:100%;margin:0;padding:0;border:0;border-radius:12px;overflow:hidden;background:white;'
  surface.style.cssText = inlineStyle
  const frame = document.createElement('iframe')
  frame.title = 'LazyMind Workflow'
  frame.style.cssText = 'display:block;width:100%;border:0;background:transparent;'
  frame.setAttribute('sandbox', 'allow-scripts allow-same-origin allow-forms allow-popups allow-downloads')
  url.hash = new URLSearchParams({ run: target.run_id, mount: target.mount_id }).toString()
  frame.src = url.href
  surface.append(frame)
  panel.append(surface)
  let disposed = false, expanded = false, collapsed = false, saving = false
  let layoutReady = false, pageStatus = 'loading'
  let observedShell: Element | null = null, followedContent: HTMLElement | undefined
  let tailFollow: ReturnType<typeof installTailFollow> | undefined
  let tailExtent: ReturnType<typeof installTailExtent> | undefined, fittedDoc: HTMLElement | undefined
  let syncQueued = false
  const seen = new Set<string>()
  function current() {
    return host.wb?.conversations === conversations && conversations.currentId === target.session_id
      && (!target.epoch || host.__lazymindWorkflowPresentationSession === gate && gate.read().epoch === target.epoch)
  }
  function size() {
    frame.style.height = expanded ? '100%' : collapsed ? '64px' : 'clamp(360px, 60vh, 640px)'
  }
  function expandedBounds() {
    const shell = locate()?.doc.closest('.conversation-shell__main')
    if (!shell) return null
    const rect = shell.getBoundingClientRect()
    const left = Math.max(0, rect.left), top = Math.max(0, rect.top)
    const right = Math.min(window.innerWidth, rect.right), bottom = Math.min(window.innerHeight, rect.bottom)
    if (right <= left || bottom <= top) return null
    const inset = Math.min(12, (right - left) / 4, (bottom - top) / 4)
    return { left: left + inset, top: top + inset, width: right - left - inset * 2, height: bottom - top - inset * 2 }
  }
  function setExpanded(next: boolean) {
    if (next === expanded) return
    if (next) {
      if (!expandedBounds() || typeof surface.showPopover !== 'function') return
      // Reserve the original tail space so expansion does not move history.
      panel.style.height = `${panel.getBoundingClientRect().height}px`
      surface.setAttribute('popover', 'manual')
      try { surface.showPopover() }
      catch { surface.removeAttribute('popover'); panel.style.height = ''; return }
      expanded = true
      collapsed = false
      reply({ type: 'lazymind.workflow.collapse', collapsed })
    } else {
      expanded = false
      surface.hidePopover()
      surface.removeAttribute('popover')
      surface.style.cssText = inlineStyle
      panel.style.height = ''
    }
    size(); sync()
  }
  function sync() {
    if (disposed) return
    if (!current()) { dispose(); return }
    const destination = locate()
    if (!destination) {
      layoutReady = false; panel.hidden = true
      tailFollow?.dispose()
      tailExtent?.dispose(); tailExtent = undefined; fittedDoc = undefined
      // A popover must never remain above a different/unready conversation.
      if (expanded) { setExpanded(false); reply({ type: 'lazymind.workflow.expansion', expanded }) }
      return
    }
    layoutReady = true; panel.hidden = false
    const scroller = destination.content.parentElement!
    const following = tailFollow && followedContent === destination.content
      ? tailFollow.status().following : scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop <= 24
    panel.style.maxWidth = `${Math.round(destination.doc.getBoundingClientRect().width)}px`
    panel.style.colorScheme = getComputedStyle(document.documentElement).colorScheme
    if (panel.parentElement !== destination.content || panel.nextElementSibling) {
      tailFollow?.dispose()
      // Unlike appendChild, moveBefore preserves a connected iframe's document.
      // Once WB has destroyed the old subtree, native page recovery is used;
      // this adapter never invents its own editor/draft restoration protocol.
      const move = (destination.content as any).moveBefore
      if (panel.isConnected && typeof move === 'function') move.call(destination.content, panel, null)
      else destination.content.append(panel)
    }
    if (!tailExtent || fittedDoc !== destination.doc || followedContent !== destination.content) {
      tailExtent?.dispose()
      fittedDoc = destination.doc
      tailExtent = fitTail(destination.doc, scroller, () => tailFollow?.align())
    } else tailExtent.sync()
    if (!tailFollow || tailFollow.status().disposed || followedContent !== destination.content) {
      tailFollow?.dispose()
      followedContent = destination.content
      tailFollow = followTail(scroller, destination.content, panel, following)
      if (host[key]?.sync === sync) host[key].tailFollow = tailFollow
    }
    const shell = destination.doc.closest('.conversation-shell__main')
    if (shell !== observedShell) {
      layoutObserver?.disconnect(); structureObserver.disconnect()
      observedShell = shell
      if (shell) {
        layoutObserver?.observe(shell)
        structureObserver.observe(shell, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-root-id'] })
      }
    }
    if (expanded) {
      const bounds = expandedBounds()
      if (!bounds) { setExpanded(false); reply({ type: 'lazymind.workflow.expansion', expanded }); return }
      Object.assign(surface.style, { position: 'fixed', inset: 'auto', maxWidth: 'none', maxHeight: 'none',
        left: `${bounds.left}px`, top: `${bounds.top}px`, width: `${bounds.width}px`, height: `${bounds.height}px`,
        boxShadow: '0 8px 28px rgba(15,23,42,0.16)' })
    }
  }
  function reply(data: object) {
    if (!disposed && current()) frame.contentWindow?.postMessage({ ...data, sessionId: target.run_id, bridgeId: target.mount_id }, url.origin)
  }
  async function receive(event: MessageEvent) {
    const data = event.data
    if (disposed || !current() || event.source !== frame.contentWindow
      || event.origin !== url.origin || data?.bridgeId !== target.mount_id || data.sessionId !== target.run_id) return
    if (data.type === 'lazymind.workflow.frame-status' && ['loading', 'ready', 'frontend-unavailable', 'load-timeout'].includes(data.status)) {
      pageStatus = data.status
      if (pageStatus === 'ready') {
        reply({ type: 'lazymind.workflow.expansion', expanded })
        reply({ type: 'lazymind.workflow.collapse', collapsed })
      }
      return
    }
    if (data.type === 'lazymind.workflow.toggle-expand') {
      setExpanded(!expanded); reply({ type: 'lazymind.workflow.expansion', expanded }); return
    }
    if (data.type === 'lazymind.workflow.toggle-collapse') {
      if (expanded) { setExpanded(false); reply({ type: 'lazymind.workflow.expansion', expanded }) }
      collapsed = !collapsed; size(); reply({ type: 'lazymind.workflow.collapse', collapsed }); return
    }
    if (data.type !== 'lazymind.workflow.download' || typeof data.requestId !== 'string'
      || !/^[a-zA-Z0-9-]{20,80}$/.test(data.requestId) || seen.has(data.requestId)) return
    const respond = (ok: boolean, error?: string) => reply({ type: 'lazymind.workflow.download.result', requestId: data.requestId, ok, error })
    if (seen.size >= 1000) { respond(false, '请重新打开面板后下载。'); return }
    seen.add(data.requestId)
    let ownsSave = false
    try {
      if (typeof wb.fileTransfer?.saveAs !== 'function') throw Error('WorkBuddy 文件保存不可用。')
      if (data.action === 'capabilities') { respond(true); return }
      if (data.action !== 'save' || !(data.blob instanceof Blob) || typeof data.filename !== 'string'
        || !data.filename.trim() || data.filename.length > 240 || /[\/\\\u0000-\u001f]/.test(data.filename)) throw Error('无效的下载文件。')
      if (saving) throw Error('请先完成当前保存对话框。')
      ownsSave = saving = true
      const base64 = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result).split(',')[1])
        reader.onerror = () => reject(Error('无法读取下载文件。'))
        reader.readAsDataURL(data.blob)
      })
      if (disposed || !current()) return
      const result = await wb.fileTransfer.saveAs({ defaultFileName: data.filename, contentBase64: base64 })
      if (result?.success !== true) throw Error('保存失败或已取消。')
      respond(true)
    } catch (error) { respond(false, error instanceof Error ? error.message : '文件保存失败。') }
    finally { if (ownsSave) saving = false }
  }
  function status() { return { status: !layoutReady ? 'layout-unavailable' : panel.isConnected ? 'mounted' : 'detached', page_status: pageStatus, version, session_id: target.session_id, run_id: target.run_id, expanded, tail_blank_trimmed: tailExtent?.status().trimmed ?? 0 } }
  // Sidebars and split-view previews resize the conversation without a window
  // resize. Observe its full shell, which includes the input composer.
  const layoutObserver = typeof ResizeObserver === 'function' ? new ResizeObserver(sync) : null
  const structureObserver = new MutationObserver(() => {
    // Character streaming inside the current document needs no layout lookup.
    if (disposed || syncQueued || layoutReady && panel.isConnected && cachedDoc?.isConnected
      && cachedDoc.dataset.rootId === target.session_id && cachedDoc.parentElement === panel.parentElement && !panel.nextElementSibling) return
    syncQueued = true
    queueMicrotask(() => { syncQueued = false; if (!disposed) sync() })
  })
  const unsubscribe = conversations.onCurrentChange(() => { if (!current()) dispose() })
  function dispose() {
    if (disposed) return
    disposed = true
    if (typeof unsubscribe === 'function') unsubscribe()
    window.removeEventListener('message', receive)
    window.removeEventListener('resize', sync)
    layoutObserver?.disconnect()
    structureObserver.disconnect()
    tailFollow?.dispose()
    tailExtent?.dispose()
    panel.remove()
    if (host[key]?.dispose === dispose) delete host[key]
  }
  host[key] = { version, session: target.session_id, run: target.run_id, origin: url.origin, epoch: target.epoch, sync, dispose, status, tailFollow }
  window.addEventListener('message', receive)
  window.addEventListener('resize', sync)
  size(); sync()
  return status()
}

export function presentationExpression(target: PresentationTarget) {
  return `(${mountPresentation.toString()})(${JSON.stringify(target)}, ${installTailFollow.toString()}, ${installTailExtent.toString()}, ${workbuddyVersionIssue.toString()})`
}

function watchView(cdp: Pick<Cdp, 'evaluate'>, reportIssue: (status: string) => void): WatchView {
  return async (report, signal) => {
    let epoch: string | undefined
    while (!signal.aborted) {
      try {
        if (epoch) await cdp.evaluate(`window.__lazymindWorkflowPresentationSession?.wait(${JSON.stringify(epoch)})`, signal)
        const view = await cdp.evaluate<ReturnType<typeof readPresentationSession>>(`(${readPresentationSession.toString()})()`, signal)
        if (view?.epoch) {
          epoch = view.epoch
          report({ chatId: view.session_id, epoch })
          continue
        }
        if (!signal.aborted) reportIssue('page-not-ready')
      } catch {
        if (!signal.aborted) reportIssue('host-offline')
      }
      epoch = undefined
      try { await delay(1000, undefined, { signal }) } catch { break }
    }
  }
}

export async function presentationLoop(cdp: Pick<Cdp, 'evaluate'>, lookupBinding: LookupBinding, bridgeURL: string, signal: AbortSignal,
  report: (status: PresentationStatus) => void = () => {}) {
  const frameUrl = new URL('/v1/workbuddy/frame', bridgeURL).href
  const ui: UIAdapter = {
    async syncPanel(view, target, active) {
      const result = await cdp.evaluate<{ status: string; page_status?: string }>(presentationExpression({
        session_id: view.chatId, epoch: view.epoch, run_id: target?.runId ?? '', frame_url: target?.frameUrl ?? '', mount_id: crypto.randomUUID(),
      }), active)
      if (result.status === 'unbound') return { status: 'unmounted' }
      if (['unsupported_version', 'version_unreadable', 'capability_unavailable', 'page-not-ready'].includes(result.status)) {
        return { status: 'layout-unavailable', issue: result.status }
      }
      const status = result.status
      return { status: status === 'mounted' || status === 'reused' || status === 'stale' ? status : 'layout-unavailable',
        pageStatus: result.page_status }
    },
  }
  try {
    await runPresentation({ watchView: watchView(cdp, status => report({ status, checked_at: Date.now() })), lookupBinding, ui, frameUrl, signal, report })
  } finally {
    await cdp.evaluate(cleanupPresentation, AbortSignal.timeout(2000)).catch(() => {})
    report({ status: 'disabled', checked_at: Date.now() })
  }
}
