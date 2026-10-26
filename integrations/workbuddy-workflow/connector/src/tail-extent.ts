/** WB 5.8.3 layout adapter; serialized into its renderer with the mount. */
export function installTailExtent(doc: HTMLElement, scroller: HTMLElement, changed: () => void) {
  // The SDK does not expose reserveTailAnchor. Read the committed renderer's
  // measurements, never mutate React props/hooks or the native virtual spacer.
  function measurement() {
    const fiberKey = Object.keys(doc).find(key => key.startsWith('__reactFiber$'))
    let fiber = fiberKey && (doc as any)[fiberKey]
    if (!fiber) return null
    let root = fiber
    while (root.return) root = root.return
    if (root.stateNode?.current !== root) fiber = fiber.alternate
    for (let depth = 0; fiber && depth < 5; depth++, fiber = fiber.return) {
      const props = fiber.memoizedProps
      if (props?.block?.props?.rootId !== doc.dataset.rootId || !Array.isArray(props.block.children)
        || !props.viewport?.virtualScrollControllerRef) continue
      let hook = fiber.memoizedState
      for (let i = 0; hook && i < 80; i++, hook = hook.next) {
        const list = hook.memoizedState?.current
        if (typeof list?.getTotalSize !== 'function' || typeof list.getVirtualItems !== 'function'
          || list.options?.getScrollElement?.() !== scroller || list.options.count !== props.block.children.length) continue
        const total = list.getTotalSize()
        if (!Number.isFinite(total) || total < 0) return null
        return { total, reserved: props.viewport.reserveTailAnchor === true,
          controller: props.viewport.virtualScrollControllerRef.current }
      }
    }
    return null
  }
  const original = ['height', 'overflow'].map(key => ({ key, value: doc.style.getPropertyValue(key), priority: doc.style.getPropertyPriority(key) }))
  let disposed = false, trimmed = 0, controller: any, unsubscribe: (() => void) | undefined
  let spacer: Element | null = null, flow: Element | null = null
  function restore() {
    if (!trimmed) return
    for (const { key, value, priority } of original) {
      if (value) doc.style.setProperty(key, value, priority)
      else doc.style.removeProperty(key)
    }
    trimmed = 0
  }
  function sync() {
    if (disposed) return
    const nextSpacer = doc.querySelector(':scope > .cr-document__virtual-spacer')
    const nextFlow = nextSpacer?.querySelector(':scope > .cr-document__virtual-flow') ?? null
    if (spacer !== nextSpacer || flow !== nextFlow) {
      resize?.disconnect(); resize?.observe(scroller)
      spacer = nextSpacer; flow = nextFlow
      if (spacer) resize?.observe(spacer)
      if (flow) resize?.observe(flow)
    }
    const measured = measurement()
    if (controller !== measured?.controller) {
      unsubscribe?.(); controller = measured?.controller
      unsubscribe = controller?.subscribeChange?.(sync)
    }
    const reservedHeight = spacer?.getBoundingClientRect().height ?? 0
    const gap = measured?.reserved ? reservedHeight - measured.total : 0
    // The only removable region is the native last-turn reservation, bounded
    // by one viewport. Historical rows (including unmounted ones) stay intact.
    if (measured && spacer && gap > 0.5 && gap <= scroller.clientHeight + 1) {
      doc.style.height = `${measured.total}px`
      doc.style.overflow = 'clip'
      trimmed = gap
    } else restore()
    changed()
  }
  const resize = typeof ResizeObserver === 'function' ? new ResizeObserver(sync) : null
  const structure = new MutationObserver(sync)
  structure.observe(doc, { childList: true, subtree: true })
  sync()
  return {
    sync,
    status: () => ({ trimmed, disposed }),
    dispose: () => {
      if (disposed) return
      disposed = true; resize?.disconnect(); structure.disconnect(); unsubscribe?.(); restore()
    },
  }
}
