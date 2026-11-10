/** Locate the existing browser service through the desktop's loaded entry modules. */
export async function prepareCodexBrowser(loadModule = (url: string): Promise<Record<string, any>> => import(/* @vite-ignore */ url)) {
  const host = window as any
  if (typeof host.__lazymindCodexBrowser?.pages?.getWebview === 'function') return
  const key = '__lazymindCodexBrowserDiscovery'
  const previous = host[key]
  if (previous?.pending) return previous.pending
  if (previous?.retryAfter > Date.now()) throw Error('Codex browser discovery is cooling down')
  const state = {pending: undefined as Promise<void> | undefined, retryAfter: 0}
  host[key] = state
  state.pending = (async () => {
    const asset = /^app:\/\/-\/assets\/(?:(?:index|app-main|app-initial)-[a-zA-Z0-9_-]+|[0-9]+\.[a-zA-Z0-9_-]+)\.js$/
    const urls = Array.from(document.querySelectorAll<HTMLLinkElement | HTMLScriptElement>('link[href],script[type="module"][src]'))
      .map(element => 'href' in element ? element.href : element.src).filter(url => asset.test(url))
    const queue = [...new Set(urls)].sort((a, b) => Number(!a.includes('/app-initial-')) - Number(!b.includes('/app-initial-')))
    const seen = new Set<string>()
    // Only inspect entry wrappers, never recursively scan the full module graph.
    for (let count = 0; queue.length && count < 4; count++) {
      const url = queue.shift()!
      if (seen.has(url)) continue
      seen.add(url)
      const exports = await loadModule(url)
      // Rspack exposes the running application's module cache through its entry.
      // Inspect loaded exports only, without executing uninitialized factories.
      const modules = [exports, ...Object.values(exports.__webpack_require__?.c ?? {})
        .map((module: any) => module?.exports)]
      for (const module of modules) {
        let candidates: any[]
        try { candidates = [module, ...Object.values(module ?? {})] } catch { continue }
        for (const value of candidates) {
          try {
            if (value && typeof value === 'object' && typeof value.pages?.getWebview === 'function'
              && typeof value.pages?.disposeHost === 'function' && typeof value.getSnapshot === 'function') {
              host.__lazymindCodexBrowser = value
              return
            }
          } catch {
            // Cached exports can include cross-origin windows or revoked proxies.
          }
        }
      }
      if (!/\/(?:index|app-main)-/.test(url)) continue
      const response = await fetch(url, {signal: AbortSignal.timeout(2000), redirect: 'error'})
      if (!response.ok) throw Error('Codex browser entry module is unavailable')
      const reader = response.body?.getReader()
      if (!reader) throw Error('Codex browser entry module is unreadable')
      let source = '', size = 0
      const decoder = new TextDecoder()
      try {
        while (true) {
          const {done, value} = await reader.read()
          if (done) break
          size += value.byteLength
          if (size > 64 * 1024) throw Error('Codex browser entry module exceeds discovery limit')
          source += decoder.decode(value, {stream: true})
        }
      } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
      // Resolve only quoted, same-directory desktop entry dependencies.
      for (const match of source.matchAll(/["'`](\.\/app-(?:main|initial)-[a-zA-Z0-9_-]+\.js)["'`]/g)) {
        const next = new URL(match[1], url).href
        if (asset.test(next) && !seen.has(next) && !queue.includes(next)) queue.push(next)
      }
    }
    throw Error('Codex browser module is unavailable')
  })()
  try { await state.pending } catch (error) { state.retryAfter = Date.now() + 5000; throw error }
  finally { state.pending = undefined }
}

/** Native browser guest: its own process and CSP, using the desktop's registration. */
export function createCodexSurface(target: {session_id:string;run_id:string;mount_id:string}, url: URL,
  receive: (data: any) => void, changed: () => void) {
  const browser = (window as any).__lazymindCodexBrowser
  if (!browser?.pages?.getWebview) throw Error('Codex native browser interface is unavailable')
  const tab = target.mount_id
  const native = browser.pages.getWebview(target.session_id, tab, '', {hostKind:'right-panel'})
  const guest = native.webview
  const element = document.createElement('div')
  element.dataset.lazymindWorkflowSurface = ''
  element.style.cssText = 'width:100%;height:clamp(360px,60vh,640px)'
  const ref = {current:null}
  let disposed = false, loading = false, loaded = false, draining = false, expanded = false, lastGeometry = ''
  url.hash = new URLSearchParams({run:target.run_id,mount:target.mount_id,native:'1'}).toString()
  function valid() {
    if (disposed) return false
    try { return guest.getURL() === url.href } catch { return false }
  }
  function reply(data: object) {
    if (!valid()) return
    void guest.executeJavaScript(`window.__lazymindWorkflowFrame?.receive(${JSON.stringify({...data,sessionId:target.run_id,bridgeId:target.mount_id})})`).catch(()=>{})
  }
  async function navigate() {
    if (disposed || loaded || loading) return
    loading = true
    try { await guest.loadURL(url.href); loaded = true } catch {} finally { loading = false }
  }
  function ready() { if (!valid()) void navigate() }
  guest.addEventListener('dom-ready',ready)
  // Newer Windows desktops expose registration state without a listener API.
  // Watch that transition so an initially hidden guest can finish mounting.
  let registered = native.isRegistered
  const registrationTimer = typeof native.listenForDidRegister === 'function' ? undefined : setInterval(() => {
    if (disposed || registered === native.isRegistered) return
    registered = native.isRegistered
    changed()
  }, 100)
  const unregister = typeof native.listenForDidRegister === 'function'
    ? native.listenForDidRegister(changed) : () => clearInterval(registrationTimer)
  function sync(bounds: {x:number;y:number;width:number;height:number}, clip: {x:number;y:number;width:number;height:number}, visible: boolean) {
    if (disposed) return
    const state = {bounds,isVisible:visible,clipBounds:clip,scale:1,windowZoom:1,shouldPaint:true,mountGeneration:1}
    native.sync(state,ref)
    // The native guest is painted outside our rounded DOM placeholder. Clip
    // its actual container to the shared WorkflowPanel's 10px corner radius.
    native.container.style.borderRadius='10px'
    native.container.style.backgroundColor='transparent'
    guest.style.borderRadius='inherit'
    guest.style.clipPath='inset(0 round 10px)'
    guest.style.backgroundColor='transparent'
    if (native.isRegistered) {
      const geometry = JSON.stringify({bounds,visible})
      if (lastGeometry !== geometry) {
        lastGeometry = geometry
        void native.browserHostService.syncView({browserTabId:tab,conversationId:target.session_id,hostKind:'right-panel',mountGeneration:1,
          bounds:visible?bounds:null,visible,presented:visible,viewportScale:1}).catch(()=>{})
      }
      if (!loaded) void navigate()
    }
    if (expanded && !native.container.matches(':popover-open')) {
      native.container.setAttribute('popover','manual')
      native.container.style.margin='0';native.container.style.padding='0';native.container.style.border='0'
      try {native.container.showPopover()} catch {}
    }
  }
  function setExpanded(next: boolean) {
    expanded = next
    if (!next && native.container.hasAttribute('popover')) {
      try {native.container.hidePopover()} catch {}
      native.container.removeAttribute('popover')
    }
  }
  const timer = setInterval(async()=>{
    if (disposed || draining || !valid()) return
    draining = true
    try {
      const events = await guest.executeJavaScript('window.__lazymindWorkflowFrame?.drain() ?? []')
      if (disposed) return
      for (const data of events) if (data?.sessionId===target.run_id&&data.bridgeId===target.mount_id) receive(data)
    } catch {} finally {draining=false}
  },150)
  async function probeStatus() {
    if (!valid()) return undefined
    const status=await guest.executeJavaScript('window.__lazymindWorkflowFrame?.status()')
    if (!valid() || status?.type!=='lazymind.workflow.frame-status' || status.sessionId!==target.run_id || status.bridgeId!==target.mount_id) return undefined
    return status
  }
  return {element,reply,sync,setExpanded,probeStatus,
    dispose:()=>{
      if(disposed)return;disposed=true;clearInterval(timer);unregister();guest.removeEventListener('dom-ready',ready)
      setExpanded(false);browser.pages.disposeHost(target.session_id,tab,'closed');element.remove()
    },
  }
}
