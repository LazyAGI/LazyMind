import { setTimeout as delay } from 'node:timers/promises'
import { CdpClient } from '../../workflow-agent-core/node/cdp'
import { installTailFollow } from '../../workflow-agent-core/renderer/tail-follow'
import { codexTargets, windowCdp } from './windows'
import { readCodexView, readPresentationSession } from './presentation-view'
import { prepareCodexBrowser, createCodexSurface } from './presentation-surface'

export interface PresentationTarget { session_id: string; run_id: string; frame_url: string; mount_id: string; epoch: string }
export interface PresentationStatus { status: string; page_status?: string; checked_at: number; native_session_id?: string; run_id?: string; target_id?: string; window_count?: number; windows?: PresentationStatus[] }

/** Serialized with its two helpers; mutates presentation only. */
export function mountPresentation(target: PresentationTarget, readView = readCodexView, followTail = installTailFollow, makeSurface = createCodexSurface) {
  const host = window as any, key = '__lazymindCodexWorkflowTail', version = 'codex-tail-v3-ready'
  const gate = host.__lazymindCodexPresentationSession
  if (!gate || gate.read().epoch !== target.epoch || gate.read().session_id !== target.session_id) return { status: 'stale' }
  const previous = host[key]
  if (!target.run_id) { previous?.dispose(); return { status: 'unbound' } }
  const url = new URL(target.frame_url)
  if (url.protocol !== 'http:' || !['localhost', '127.0.0.1'].includes(url.hostname)
    || url.username || url.password || url.pathname !== '/v1/codex/frame' || url.search || url.hash
    || !/^[a-zA-Z0-9_-]{1,256}$/.test(target.run_id) || !/^[a-zA-Z0-9-]{20,80}$/.test(target.mount_id)) throw Error('Invalid presentation target')
  if (previous?.version === version && previous.session === target.session_id && previous.run === target.run_id && previous.origin === url.origin) {
    previous.sync(); return previous.status()
  }
  previous?.dispose()
  const locate = () => { const view = readView(); return view?.session === target.session_id ? view : null }
  const initial = locate()
  if (!initial) return { status: 'layout-unavailable' }
  let scroller = initial.scroller
  const panel = document.createElement('section')
  panel.dataset.lazymindWorkflowTail = target.session_id
  panel.setAttribute('aria-label', 'LazyMind Workflow')
  panel.style.cssText = 'box-sizing:border-box;position:relative;flex:none;margin:16px auto 24px;width:calc(100% - 32px);max-width:var(--thread-body-max-width,var(--thread-content-max-width,760px));overflow:hidden;border:0;border-radius:12px;'
  const surface = makeSurface(target, url, receive, () => sync())
  panel.append(surface.element)
  let disposed = false, expanded = false, collapsed = false, layoutReady = false, pageStatus = 'loading', queued = false
  let follower: ReturnType<typeof installTailFollow> | undefined, followedContent: HTMLElement | undefined
  let observedShell: Element | undefined
  function current() { return gate.read().session_id === target.session_id }
  function size() { surface.element.style.height = collapsed ? '64px' : 'clamp(360px, 60vh, 640px)' }
  function bounds() {
    const view = locate(), shell = view?.scroller.parentElement
    if (!shell) return null
    const r = shell.getBoundingClientRect(), left = Math.max(0, r.left) + 12, top = Math.max(0, r.top) + 12
    const right = Math.min(window.innerWidth, r.right) - 12, bottom = Math.min(window.innerHeight, r.bottom) - 12
    return right > left && bottom > top ? { left, top, width: right - left, height: bottom - top } : null
  }
  function reply(data: object) { if (!disposed && current()) surface.reply(data) }
  function setExpanded(next: boolean) {
    if (expanded === next || next && !bounds()) return
    expanded = next
    if (next) {collapsed=false;reply({type:'lazymind.workflow.collapse',collapsed})}
    surface.setExpanded(next);size();sync()
  }
  function sync() {
    if (disposed) return
    if (!current()) { dispose(); return }
    const view = locate()
    if (!view) {
      layoutReady = false; panel.hidden = true; follower?.dispose(); surface.sync({x:0,y:0,width:0,height:0},{x:0,y:0,width:0,height:0},false)
      if (expanded) { setExpanded(false); reply({ type: 'lazymind.workflow.expansion', expanded }) }
      return
    }
    if (scroller !== view.scroller) {
      scroller.removeEventListener('scroll', sync)
      scroller = view.scroller
      scroller.addEventListener('scroll', sync, { passive: true })
      follower?.dispose()
    }
    layoutReady = true; panel.hidden = false
    const following = follower && followedContent === view.content ? follower.status().following
      : view.scroller.scrollHeight - view.scroller.clientHeight - view.scroller.scrollTop <= 24
    // Keep the panel after every message, ahead of native composer clearance.
    if (panel.parentElement !== view.content || panel.nextElementSibling !== view.spacer) {
      follower?.dispose()
      const move = (view.content as any).moveBefore
      if (panel.isConnected && typeof move === 'function') move.call(view.content, panel, view.spacer)
      else view.content.insertBefore(panel, view.spacer)
    }
    if (!follower || follower.status().disposed || followedContent !== view.content) {
      follower?.dispose(); followedContent = view.content
      follower = followTail(view.scroller, view.content, panel, following, '[data-thread-scroll-footer] button[class~="group/scroll-to-bottom"]')
    }
    const shell = view.scroller.parentElement!
    if (observedShell !== shell) {
      observedShell = shell; resize?.disconnect(); structure.disconnect()
      resize?.observe(shell); resize?.observe(view.transcript); resize?.observe(panel)
      structure.observe(shell, { childList: true, subtree: true })
    }
    const placeholder = surface.element.getBoundingClientRect()
    const viewport = view.scroller.getBoundingClientRect()
    const footer = view.scroller.querySelector<HTMLElement>('[data-thread-scroll-footer]')
    const footerHeight = footer?.getBoundingClientRect().height ?? 0
    const rect = expanded ? bounds()! : {left:placeholder.left,top:placeholder.top,width:placeholder.width,height:placeholder.height}
    const clip = expanded ? {x:rect.left,y:rect.top,width:rect.width,height:rect.height}
      : {x:Math.max(0,viewport.left),y:Math.max(0,viewport.top),width:viewport.width,height:Math.max(0,Math.min(window.innerHeight,viewport.bottom-footerHeight)-Math.max(0,viewport.top))}
    const visible = rect.width>0 && rect.height>0 && rect.top+rect.height>clip.y && rect.top<clip.y+clip.height
    surface.sync({x:rect.left,y:rect.top,width:rect.width,height:rect.height},clip,visible)
  }
  function receive(data: any) {
    if (disposed || !current() || data?.sessionId !== target.run_id || data.bridgeId !== target.mount_id) return
    if (data.type === 'lazymind.workflow.frame-status' && ['loading','ready','frontend-unavailable','load-timeout'].includes(data.status)) {
      pageStatus = data.status
      if (pageStatus === 'ready') { reply({ type: 'lazymind.workflow.expansion', expanded }); reply({ type: 'lazymind.workflow.collapse', collapsed }) }
    } else if (data.type === 'lazymind.workflow.toggle-expand') {
      setExpanded(!expanded); reply({ type: 'lazymind.workflow.expansion', expanded })
    } else if (data.type === 'lazymind.workflow.toggle-collapse') {
      if (expanded) { setExpanded(false); reply({ type: 'lazymind.workflow.expansion', expanded }) }
      collapsed = !collapsed; size(); sync(); reply({ type: 'lazymind.workflow.collapse', collapsed })
    }
  }
  function status() { return { status: !layoutReady ? 'layout-unavailable' : panel.isConnected ? 'mounted' : 'detached', page_status: pageStatus, session_id: target.session_id, run_id: target.run_id, expanded, collapsed } }
  const resize = typeof ResizeObserver === 'function' ? new ResizeObserver(sync) : null
  const structure = new MutationObserver(() => {
    if (queued || disposed) return
    queued = true; queueMicrotask(() => { queued = false; sync() })
  })
  const unsubscribe = gate.subscribe(() => { if (!current()) dispose() })
  function dispose() {
    if (disposed) return
    disposed = true; unsubscribe(); resize?.disconnect(); structure.disconnect(); follower?.dispose()
    window.removeEventListener('resize', sync); scroller.removeEventListener('scroll', sync)
    surface.dispose()
    panel.remove()
    if (host[key]?.dispose === dispose) delete host[key]
  }
  async function probeStatus() {
    if(disposed||!current())return undefined
    const latest=await surface.probeStatus()
    if(disposed||!current())return undefined
    if(latest)receive(latest)
    sync()
    return status()
  }
  host[key] = { version, session: target.session_id, run: target.run_id, origin: url.origin, sync, status, probeStatus, dispose }
  window.addEventListener('resize', sync); scroller.addEventListener('scroll', sync, {passive:true})
  size(); sync()
  return status()
}

export function sessionExpression() { return `(${readPresentationSession.toString()})(${readCodexView.toString()})` }
export function presentationExpression(target: PresentationTarget) {
  return `(async()=>{await (${prepareCodexBrowser.toString()})();return (${mountPresentation.toString()})(${JSON.stringify(target)},${readCodexView.toString()},${installTailFollow.toString()},${createCodexSurface.toString()})})()`
}
export const cleanupPresentation = 'window.__lazymindCodexWorkflowTail?.dispose(); window.__lazymindCodexPresentationSession?.dispose()'

export async function presentationWindowLoop(cdp: Pick<CdpClient,'evaluate'>, request: (session: string) => Promise<{session_id: string; run_id: string}>, bridgeURL: string,
  signal: AbortSignal, report: (status: PresentationStatus) => void) {
  while (!signal.aborted) {
    let native_session_id: string | undefined, run_id: string | undefined, epoch: string | undefined, retryLayout = false
    try {
      const view = await cdp.evaluate<ReturnType<typeof readPresentationSession>>(sessionExpression(), signal)
      epoch = view.epoch; native_session_id = view.session_id ?? undefined
      if (!native_session_id) {
        await cdp.evaluate('window.__lazymindCodexWorkflowTail?.dispose()', signal)
        report({status:'waiting-session',checked_at:Date.now()})
      } else {
        const target = await request(native_session_id)
        if (target.session_id !== native_session_id || typeof target.run_id !== 'string') throw Error('Invalid presentation response')
        run_id = target.run_id || undefined
        const mounted = await cdp.evaluate<{status: string; page_status?: string}>(presentationExpression({...target,epoch:epoch!,frame_url:new URL('/v1/codex/frame',bridgeURL).href,mount_id:crypto.randomUUID()}),signal)
        retryLayout = ['layout-unavailable','stale'].includes(mounted.status)
        report({...mounted,checked_at:Date.now(),native_session_id,run_id})
      }
    } catch (error) {
      const denied = [401,403].includes((error as any)?.status)
      if (denied) await cdp.evaluate('window.__lazymindCodexWorkflowTail?.dispose()',signal).catch(()=>{})
      report({status:denied?'access-denied':'unavailable',checked_at:Date.now(),native_session_id,run_id})
      epoch = undefined
    }
    if (signal.aborted) break
    try {
      if (epoch && !retryLayout) await cdp.evaluate(`window.__lazymindCodexPresentationSession?.wait(${JSON.stringify(epoch)})`,signal)
      else await delay(retryLayout?100:1000,undefined,{signal})
    } catch { if (!signal.aborted) await delay(1000,undefined,{signal}).catch(()=>{}) }
  }
  await cdp.evaluate(cleanupPresentation,AbortSignal.timeout(2000)).catch(()=>{})
  report({status:'disabled',checked_at:Date.now()})
}

export async function cleanupCodexPresentations(port:number,signal:AbortSignal) {
  const windows=await codexTargets(port,signal)
  await Promise.allSettled(windows.map(window=>windowCdp(port,window.id).evaluate(cleanupPresentation,signal)))
}

/** One delivery worker; a separate presentation loop for every chat window. */
export async function presentationLoop(port:number,request:(session:string)=>Promise<{session_id:string;run_id:string}>,bridgeURL:string,
  signal:AbortSignal,report:(status:PresentationStatus)=>void) {
  const entries=new Map<string,{stop:AbortController;task:Promise<void>;status:PresentationStatus}>()
  function publish() {
    const windows=[...entries.values()].map(entry=>entry.status)
    const mounted=windows.find(window=>window.status==='mounted')
    report({status:mounted?'mounted':windows[0]?.status??'waiting-window',checked_at:Date.now(),window_count:windows.length,windows})
  }
  try {
    while(!signal.aborted) {
      try {
        const targets=await codexTargets(port,signal),ids=new Set(targets.map(target=>target.id))
        for(const [id,entry]of entries)if(!ids.has(id)){entry.stop.abort();entries.delete(id)}
        for(const target of targets) {
          if(entries.has(target.id))continue
          const stop=new AbortController(),entry={stop,task:Promise.resolve(),status:{status:'starting',target_id:target.id,checked_at:Date.now()} as PresentationStatus}
          entries.set(target.id,entry)
          entry.task=presentationWindowLoop(windowCdp(port,target.id),request,bridgeURL,AbortSignal.any([signal,stop.signal]),status=>{
            entry.status={...status,target_id:target.id}
            if(entries.get(target.id)===entry)publish()
          })
        }
        publish()
      }catch{if(!signal.aborted)report({status:'host-offline',checked_at:Date.now(),window_count:entries.size,windows:[...entries.values()].map(entry=>entry.status)})}
      try{await delay(1000,undefined,{signal})}catch{break}
    }
  }finally{
    for(const entry of entries.values())entry.stop.abort()
    await Promise.allSettled([...entries.values()].map(entry=>entry.task))
    report({status:'disabled',checked_at:Date.now(),window_count:0,windows:[]})
  }
}
