/** Presentation identity comes from the committed main-chat view, never binds a run. */
export function readCodexChat() {
  const visibleMainScrollers = (selector: string) =>
    Array.from(document.querySelectorAll<HTMLElement>(selector))
      .filter(element => element.getBoundingClientRect().width > 0
        && !element.closest('[data-app-shell-tab-panel-controller], [inert], [aria-hidden="true"]'))
  const timelineScrollers = visibleMainScrollers('[data-app-action-timeline-scroll]')
  // Compact Codex chats omit the timeline action marker. Other Work Mode
  // surfaces host MCP apps, so require the native Codex transcript class.
  const scrollers = timelineScrollers.length > 0 ? timelineScrollers
    : visibleMainScrollers('.thread-scroll-container[data-mcp-app-scroll-host="true"]')
  if (scrollers.length === 0) return null
  if (scrollers.length > 1) return undefined
  const scroller = scrollers[0]
  const fiberKey = Object.keys(scroller).find(key => key.startsWith('__reactFiber$'))
  let fiber = fiberKey ? (scroller as any)[fiberKey] : null
  let root = fiber
  while (root?.return) root = root.return
  if (root?.stateNode?.current && root.stateNode.current !== root) fiber = fiber?.alternate
  let session: string | undefined, remote = false
  for (let depth = 0; fiber && depth < 60; depth++, fiber = fiber.return) {
    const props = fiber.memoizedProps
    if (typeof props?.conversationId === 'string' && props.hostId === 'local') { session = props.conversationId; break }
    if (typeof props?.conversationId === 'string' && typeof props.hostId === 'string') remote = true
  }
  if (!session || !/^[a-f0-9-]{36}$/i.test(session)) return remote ? null : undefined
  return { session, scroller }
}

export function readCodexView(readChat = readCodexChat) {
  const chat = readChat()
  if (!chat) return null
  const { session, scroller } = chat
  const transcript = scroller.querySelector<HTMLElement>('[data-thread-user-message-navigation-content]')
  const content = transcript?.parentElement
  if (!transcript || !content || content.parentElement !== scroller) return null
  // Codex sizes its composer spacer with this variable in both chat layouts.
  // Older versions also mark it as sticky.
  const spacer = content.querySelector(':scope > [aria-hidden="true"][class*="--thread-scroll-padding-bottom"]')
    ?? Array.from(content.children).find(element => element !== transcript && element.classList.contains('sticky')) ?? null
  return { session, scroller, content, transcript, spacer }
}

/** Serialized into the renderer together with readCodexChat. */
export function readPresentationSession(readChat = readCodexChat) {
  const host = window as any
  const key = '__lazymindCodexPresentationSession'
  const version = 'codex-view-v4'
  if (host[key]?.version !== version) host[key]?.dispose()
  if (!host[key]) {
    let view = readChat() ?? null, epoch = crypto.randomUUID(), queued = false, available = true
    const listeners = new Set<() => void>(), waiters = new Set<() => void>()
    const check = () => {
      const next = readChat()
      if (available === (next !== undefined) && (next === undefined
        || view?.session === next?.session && view?.scroller === next?.scroller)) return
      available = next !== undefined
      if (next !== undefined && (view?.session !== next?.session || view?.scroller !== next?.scroller)) {
        view = next
        epoch = crypto.randomUUID()
      }
      for (const listener of listeners) listener()
      for (const done of [...waiters]) done()
    }
    const observer = new MutationObserver(() => {
      if (queued) return
      queued = true
      queueMicrotask(() => { queued = false; check() })
    })
    observer.observe(document, { childList: true, subtree: true, attributes: true, attributeFilter: ['aria-hidden', 'inert'] })
    const gate = {
      version,
      read: () => { check(); return { session_id: view?.session ?? null, epoch, available } },
      subscribe: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener) },
      wait: (expected: string) => {
        if (gate.read().epoch !== expected) return Promise.resolve()
        return new Promise<void>(resolve => {
          const done = () => { clearTimeout(timer); waiters.delete(done); resolve() }
          const timer = setTimeout(done, 5000)
          waiters.add(done)
        })
      },
      dispose: () => {
        observer.disconnect(); listeners.clear()
        for (const done of [...waiters]) done()
        if (host[key] === gate) delete host[key]
      },
    }
    host[key] = gate
  }
  return host[key].read() as { session_id: string | null; epoch: string; available: boolean }
}
