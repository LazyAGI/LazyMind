import { afterEach, expect, it, vi } from 'vitest'
import { JSDOM } from 'jsdom'
import { installTailFollow } from '../src/tail-follow'

let dom: JSDOM, stop: (() => void) | undefined
afterEach(() => { stop?.(); dom?.window.close(); vi.unstubAllGlobals() })
function fixture(following = true) {
  dom = new JSDOM('<div id="scroller"><div id="content"><div id="history"></div><section id="panel"></section></div></div>')
  for (const key of ['window', 'document', 'Element', 'MutationObserver', 'getComputedStyle']) vi.stubGlobal(key, (dom.window as any)[key])
  let resized = () => {}
  const disconnect = vi.fn()
  vi.stubGlobal('ResizeObserver', class { constructor(fn: () => void) { resized = fn }; observe() {}; disconnect = disconnect })
  const scroller = document.querySelector<HTMLElement>('#scroller')!, content = document.querySelector<HTMLElement>('#content')!, panel = document.querySelector<HTMLElement>('#panel')!
  const dimensions = { total: 1400, viewport: 600 }
  Object.defineProperties(scroller, { scrollHeight: { get: () => dimensions.total }, clientHeight: { get: () => dimensions.viewport } })
  scroller.scrollTop = following ? 800 : 300
  const follower = installTailFollow(scroller, content, panel, following)
  stop = follower.dispose
  const scroll = () => scroller.dispatchEvent(new dom.window.Event('scroll'))
  const wheel = (deltaY: number) => scroller.dispatchEvent(new dom.window.WheelEvent('wheel', { deltaY }))
  return { scroller, panel, dimensions, follower, resized: () => resized(), disconnect, scroll, wheel }
}

it('keeps the complete history bottom stable during growth and native message-only scroll writes', () => {
  const f = fixture()
  f.dimensions.total += 80; f.resized()
  expect(f.scroller.scrollTop).toBe(880)
  // WorkBuddy scrollToEnd uses the virtual message total, excluding our panel.
  f.scroller.scrollTop = 380; f.scroll()
  expect(f.scroller.scrollTop).toBe(880)
  expect(f.follower.status().following).toBe(true)
  f.dimensions.total -= 200; f.resized()
  expect(f.scroller.scrollTop).toBe(680)
})

it('releases immediately on upward intent and only resumes after the user reaches the bottom', () => {
  const f = fixture()
  f.wheel(-1) // Even a small movement near the bottom must release ownership.
  f.scroller.scrollTop -= 1; f.scroll()
  f.dimensions.total += 100; f.resized()
  expect(f.scroller.scrollTop).toBe(799)
  expect(f.follower.status().following).toBe(false)
  f.wheel(30); f.scroller.scrollTop = 850; f.scroll()
  expect(f.follower.status().following).toBe(false)
  f.scroller.scrollTop = 900; f.scroll()
  expect(f.follower.status().following).toBe(true)
  f.dimensions.total += 50; f.resized()
  expect(f.scroller.scrollTop).toBe(950)
})

it('does not pull a reader to the bottom on mount, viewport resizing or panel changes', () => {
  const f = fixture(false)
  f.dimensions.total += 300; f.dimensions.viewport -= 100; f.resized()
  expect(f.scroller.scrollTop).toBe(300)
  document.body.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'End', bubbles: true }))
  expect(f.scroller.scrollTop).toBe(1200)
  document.body.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'PageUp', bubbles: true }))
  expect(f.follower.status().following).toBe(false)
})

it('disconnects when an old mounted panel is removed and leaves no scroll correction behind', async () => {
  const f = fixture()
  f.panel.remove()
  await Promise.resolve()
  expect(f.disconnect).toHaveBeenCalledOnce()
  expect(f.follower.status().disposed).toBe(true)
  f.scroller.scrollTop = 400; f.scroll()
  expect(f.scroller.scrollTop).toBe(400)
})

it('resumes following the whole history when WB scroll-to-bottom is explicitly clicked', () => {
  const f = fixture(false)
  const button = document.createElement('button')
  button.className = 'cr-scroll-to-bottom'
  f.scroller.parentElement!.append(button)
  button.click()
  expect(f.scroller.scrollTop).toBe(800)
  f.scroller.scrollTop = 420; f.scroll() // WB then writes its message-only end.
  expect(f.scroller.scrollTop).toBe(800)
  f.dimensions.total += 100; f.resized()
  expect(f.scroller.scrollTop).toBe(900)
  f.follower.dispose()
  f.scroller.scrollTop = 200; button.click()
  expect(f.scroller.scrollTop).toBe(200)
})
