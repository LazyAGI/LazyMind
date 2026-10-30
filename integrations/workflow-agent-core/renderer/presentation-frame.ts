/** Runs in the shared loopback wrapper; the original run page owns auth/editing. */
export function startPresentationFrame() {
  const params = new URLSearchParams(location.hash.slice(1))
  const run = params.get('run') || '', bridgeId = params.get('mount') || ''
  const native = parent === window && params.get('native') === '1'
  if (!/^[a-zA-Z0-9_-]{1,256}$/.test(run) || !/^[a-zA-Z0-9-]{20,80}$/.test(bridgeId) || parent === window && !native) return
  const events: object[] = []
  let currentStatus = 'loading'
  const emit = (data: object) => {
    if (native) { if (events.length < 100) events.push(data) }
    else parent.postMessage(data, '*')
  }
  const frame = document.querySelector<HTMLIFrameElement>('#workflow')!
  const notice = document.querySelector<HTMLElement>('#notice')!
  const retry = document.querySelector<HTMLButtonElement>('#retry')
  // Assistant Bridge supplies the current configured frontend on every load.
  const base = new URL(document.querySelector<HTMLMetaElement>('meta[name="lazymind-workflow-frontend"]')!.content)
  if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) return
  const url = new URL(`${base.href.replace(/\/$/, '')}/workflow-runs/${run}/embed`)
  url.searchParams.set('hostOrigin', location.origin)
  const hostDownloads = document.querySelector<HTMLMetaElement>('meta[name="lazymind-workflow-downloads"]')?.content !== 'browser'
  if (hostDownloads) {
    url.searchParams.set('downloadHostOrigin', location.origin)
    url.searchParams.set('downloadBridgeId', bridgeId)
  // Generated HTML previews can use detached Blob links; this is still needed
  // without MCP Apps. Leave the old offscreen frame-clock workaround disabled.
    url.searchParams.set('workbuddyBlobDownloads', '1')
  }
  const pending = new Map<string, number>()
  let closed = false, loading = false, navigating = false
  let retryTimer: number | undefined, loadTimer: number | undefined
  let probe: AbortController | undefined
  const snapshot = () => ({ type: 'lazymind.workflow.frame-status', status: currentStatus, sessionId: run, bridgeId })
  const report = (status: string) => { currentStatus = status; emit(snapshot()) }
  function show(status: string, text: string, canRetry: boolean) {
    notice.hidden = false; notice.textContent = text
    if (retry) retry.hidden = !canRetry
    report(status)
  }
  async function load() {
    if (closed || loading) return
    loading = true
    window.clearTimeout(retryTimer); window.clearTimeout(loadTimer)
    show('loading', '正在载入 Workflow…', false)
    probe = new AbortController()
    const timeout = window.setTimeout(() => probe?.abort(), 5000)
    try {
      // Only check that the frontend is reachable. Authentication, run access,
      // editing and saving remain entirely inside the original Workflow page.
      await fetch(url.origin + '/', { mode: 'no-cors', cache: 'no-store', credentials: 'omit', signal: probe.signal })
      if (closed) return
      navigating = true
      frame.src = url.href
      loadTimer = window.setTimeout(() => {
        show('load-timeout', 'Workflow 页面尚未载入，可重试。', true)
      }, 15000)
    } catch {
      if (closed) return
      show('frontend-unavailable', 'LazyMind 页面服务暂时不可用，恢复后将自动载入。', true)
      retryTimer = window.setTimeout(() => { void load() }, 5000)
    } finally { window.clearTimeout(timeout); loading = false }
  }
  const childTypes = new Set(['lazymind.workflow.toggle-expand', 'lazymind.workflow.toggle-collapse'])
  const hostTypes = new Set(['lazymind.workflow.expansion', 'lazymind.workflow.collapse'])
  function receiveHost(data: any) {
    if (!data || data.bridgeId !== bridgeId || data.sessionId !== run) return
    if (hostTypes.has(data.type)) frame.contentWindow?.postMessage(data, url.origin)
    if (data.type === 'lazymind.workflow.download.result' && pending.has(data.requestId)) {
      window.clearTimeout(pending.get(data.requestId)); pending.delete(data.requestId)
      frame.contentWindow?.postMessage(data, url.origin)
    }
  }
  if (native) (window as any).__lazymindWorkflowFrame = {
    drain: () => events.splice(0), receive: receiveHost, status: snapshot,
  }
  window.addEventListener('message', event => {
    const data = event.data
    if (!data || typeof data !== 'object') return
    if (event.source === frame.contentWindow && event.origin === url.origin) {
      if (childTypes.has(data.type) && data.sessionId === run) {
        emit({ type: data.type, sessionId: run, bridgeId })
      } else if (data.type === 'lazymind.workflow.download' && data.bridgeId === bridgeId
        && typeof data.requestId === 'string' && /^[a-zA-Z0-9-]{20,80}$/.test(data.requestId)) {
        if (pending.has(data.requestId)) return
        pending.set(data.requestId, window.setTimeout(() => pending.delete(data.requestId), 310_000))
        emit({ type: data.type, sessionId: run, bridgeId, requestId: data.requestId,
          action: data.action, filename: data.filename, blob: data.blob })
      } else if (data.type === 'lazymind.workflow.compatibility' && data.bridgeId === bridgeId) {
        notice.hidden = data.status !== 'blob-download-error'
        notice.textContent = `保存失败或取消：${String(data.detail || '请重试').slice(0, 500)}`
      }
      return
    }
    // file:// has an opaque origin. Source + mount ID + run are all required.
    if (!native && event.source === parent) receiveHost(data)
  })
  frame.onload = () => {
    if (closed || !navigating) return
    window.clearTimeout(loadTimer)
    notice.hidden = true
    if (retry) retry.hidden = true
    report('ready')
  }
  if (retry) retry.onclick = () => { void load() }
  window.addEventListener('pagehide', () => {
    closed = true; probe?.abort()
    window.clearTimeout(retryTimer); window.clearTimeout(loadTimer)
    for (const timer of pending.values()) window.clearTimeout(timer)
    pending.clear()
  }, { once: true })
  void load()
}
