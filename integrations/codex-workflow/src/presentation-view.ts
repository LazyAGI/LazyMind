/** Presentation identity comes from the committed main-chat view, never binds a run. */
export function readCodexView() {
  const scrollers = Array.from(document.querySelectorAll<HTMLElement>('[data-app-action-timeline-scroll]'))
    .filter(element => element.getBoundingClientRect().width > 0)
  if (scrollers.length !== 1) return null
  const scroller = scrollers[0]
  const fiberKey = Object.keys(scroller).find(key => key.startsWith('__reactFiber$'))
  let fiber = fiberKey ? (scroller as any)[fiberKey] : null
  let root = fiber
  while (root?.return) root = root.return
  if (root?.stateNode?.current && root.stateNode.current !== root) fiber = fiber?.alternate
  let session: string | undefined
  for (let depth = 0; fiber && depth < 60; depth++, fiber = fiber.return) {
    const props = fiber.memoizedProps
    if (typeof props?.conversationId === 'string' && props.hostId === 'local') { session = props.conversationId; break }
  }
  if (!session || !/^[a-f0-9-]{36}$/i.test(session)) return null
  const transcript = scroller.querySelector<HTMLElement>('[data-thread-user-message-navigation-content]')
  const content = transcript?.parentElement
  if (!transcript || !content || content.parentElement !== scroller) return null
  // The last native child reserves space for the absolutely-positioned composer.
  // Insert before it, outside the message list and any virtualized turn.
  const spacer = Array.from(content.children).find(element => element !== transcript && element.classList.contains('sticky')) ?? null
  return { session, scroller, content, transcript, spacer }
}

/** Serialized into the renderer together with readCodexView. */
export function readPresentationSession(readView = readCodexView) {
  const host = window as any
  const key = '__lazymindCodexPresentationSession'
  const version = 'codex-view-v1'
  if (host[key]?.version !== version) host[key]?.dispose()
  if (!host[key]) {
    let view = readView(), epoch = crypto.randomUUID(), queued = false
    const listeners = new Set<() => void>(), waiters = new Set<() => void>()
    const check = () => {
      const next = readView()
      if (view?.session === next?.session && view?.scroller === next?.scroller) return
      view = next; epoch = crypto.randomUUID()
      for (const listener of listeners) listener()
      for (const done of [...waiters]) done()
    }
    const observer = new MutationObserver(() => {
      if (queued) return
      queued = true
      queueMicrotask(() => { queued = false; check() })
    })
    observer.observe(document.body, { childList: true, subtree: true })
    const gate = {
      version,
      read: () => { check(); return { session_id: view?.session ?? null, epoch } },
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
  return host[key].read() as { session_id: string | null; epoch: string }
}
