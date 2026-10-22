/** Serialized with the mount function; no module-scope dependencies. */
export function installTailFollow(scroller: HTMLElement, content: HTMLElement, panel: HTMLElement, initiallyFollowing: boolean) {
  let following = initiallyFollowing, disposed = false, dragging = false, downIntent = false
  let touchY: number | undefined
  const gap = () => Math.max(0, scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop)
  const atBottom = () => gap() <= 2
  function align() {
    if (disposed) return
    if (!panel.isConnected || panel.parentElement !== content || content.parentElement !== scroller) { dispose(); return }
    if (following && !dragging) {
      const end = Math.max(0, scroller.scrollHeight - scroller.clientHeight)
      if (Math.abs(scroller.scrollTop - end) > 0.5) scroller.scrollTop = end
    }
  }
  function intent(delta: number) {
    if (delta < 0) { following = false; downIntent = false }
    if (delta > 0) {
      downIntent = true
      if (!dragging && atBottom()) following = true
    }
  }
  function scroll() {
    // Native virtual-list writes also emit scroll. They are not user intent.
    // Correct in the capture phase, before the host reads this position and
    // before paint; never use smooth scrolling to chase a moving bottom.
    if (!following && downIntent && !dragging && atBottom()) following = true
    align()
  }
  function wheel(event: WheelEvent) {
    if (event.ctrlKey) return
    // An inner code block can consume a wheel without moving the history.
    for (let el = event.target instanceof Element ? event.target : null; el && el !== scroller; el = el.parentElement) {
      if (el.scrollHeight > el.clientHeight && /auto|scroll/.test(getComputedStyle(el).overflowY)
        && (event.deltaY < 0 ? el.scrollTop > 0 : el.scrollTop + el.clientHeight < el.scrollHeight - 1)) return
    }
    intent(event.deltaY)
  }
  function touchStart(event: TouchEvent) { touchY = event.touches[0]?.clientY }
  function touchMove(event: TouchEvent) {
    const y = event.touches[0]?.clientY
    if (y !== undefined && touchY !== undefined) intent(touchY - y)
    touchY = y
  }
  function pointerDown(event: PointerEvent) {
    if (event.button !== 0) return
    const rect = scroller.getBoundingClientRect()
    if (event.clientX >= rect.right - Math.max(14, scroller.offsetWidth - scroller.clientWidth)) {
      dragging = true; following = false; downIntent = false
    }
  }
  function pointerUp() {
    if (!dragging) return
    dragging = false
    following = atBottom()
    align()
  }
  function keydown(event: KeyboardEvent) {
    const target = event.target
    if (!(target instanceof Element) || target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"])')) return
    if (!scroller.contains(target) && target !== document.body && target !== document.documentElement) return
    if (['ArrowUp', 'PageUp', 'Home'].includes(event.key) || event.key === ' ' && event.shiftKey) intent(-1)
    if (['ArrowDown', 'PageDown'].includes(event.key) || event.key === ' ' && !event.shiftKey) intent(1)
    if (event.key === 'End' && !event.shiftKey) { following = true; align() }
  }
  const viewport = scroller.parentElement
  function bottomButton(event: Event) {
    const button = event.target instanceof Element ? event.target.closest('.cr-scroll-to-bottom') : null
    if (!button || !viewport?.contains(button)) return
    following = true; downIntent = false; dragging = false
    align()
  }
  // ResizeObserver delivers after layout and before paint, including virtual
  // spacer growth, image loads and panel resizing. No per-character DOM scan.
  const resize = typeof ResizeObserver === 'function' ? new ResizeObserver(align) : null
  resize?.observe(content); resize?.observe(panel); resize?.observe(scroller)
  // Also owns cleanup for an older mount upgraded in place (iframe preserved).
  const structure = new MutationObserver(align)
  structure.observe(content, { childList: true })
  scroller.addEventListener('scroll', scroll, { capture: true, passive: true })
  scroller.addEventListener('wheel', wheel, { capture: true, passive: true })
  scroller.addEventListener('touchstart', touchStart, { capture: true, passive: true })
  scroller.addEventListener('touchmove', touchMove, { capture: true, passive: true })
  scroller.addEventListener('pointerdown', pointerDown, true)
  window.addEventListener('pointerup', pointerUp, true)
  window.addEventListener('pointercancel', pointerUp, true)
  window.addEventListener('keydown', keydown, true)
  // WB uses mousedown for its loading button and click for the normal button.
  viewport?.addEventListener('mousedown', bottomButton, true)
  viewport?.addEventListener('click', bottomButton, true)
  function dispose() {
    if (disposed) return
    disposed = true; resize?.disconnect(); structure.disconnect()
    scroller.removeEventListener('scroll', scroll, true)
    scroller.removeEventListener('wheel', wheel, true)
    scroller.removeEventListener('touchstart', touchStart, true)
    scroller.removeEventListener('touchmove', touchMove, true)
    scroller.removeEventListener('pointerdown', pointerDown, true)
    window.removeEventListener('pointerup', pointerUp, true)
    window.removeEventListener('pointercancel', pointerUp, true)
    window.removeEventListener('keydown', keydown, true)
    viewport?.removeEventListener('mousedown', bottomButton, true)
    viewport?.removeEventListener('click', bottomButton, true)
  }
  align()
  return { dispose, align, status: () => ({ following, dragging, disposed, distanceFromBottom: gap() }) }
}
