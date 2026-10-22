import { afterEach, expect, it, vi } from 'vitest'
import { JSDOM } from 'jsdom'
import { installTailExtent } from '../src/tail-extent'

let dom: JSDOM
let adapter: ReturnType<typeof installTailExtent> | undefined
afterEach(() => { adapter?.dispose(); adapter = undefined; dom?.window.close(); vi.unstubAllGlobals() })
function fixture() {
  dom = new JSDOM('<div id="scroll"><div class="cr-document" data-root-id="session"><div class="cr-document__virtual-spacer"><div class="cr-document__virtual-flow"></div></div></div></div>')
  vi.stubGlobal('MutationObserver', dom.window.MutationObserver)
  const doc = dom.window.document.querySelector<HTMLElement>('.cr-document')!
  const scroller = doc.parentElement!
  Object.defineProperty(scroller, 'clientHeight', { value: 700 })
  let total = 4200, extent = 4600, notify = () => {}
  const spacer = doc.firstElementChild as HTMLElement
  spacer.getBoundingClientRect = () => ({ height: extent } as DOMRect)
  const root: any = { stateNode: {} }; root.stateNode.current = root
  const controller = { subscribeChange: (fn: () => void) => { notify = fn; return () => { notify = () => {} } } }
  const list = { getTotalSize: () => total, getVirtualItems: () => [], options: { count: 14, getScrollElement: () => scroller } }
  const owner: any = { return: root, memoizedProps: { block: { props: { rootId: 'session' }, children: Array(14) }, viewport: { reserveTailAnchor: true, virtualScrollControllerRef: { current: controller } } }, memoizedState: { memoizedState: { current: list } } }
  const fiber: any = { return: owner }; (doc as any).__reactFiber$test = fiber
  const changed = vi.fn()
  adapter = installTailExtent(doc, scroller, changed)
  return { doc, spacer, owner, fiber, root, changed, grow: (real: number, reserved: number) => { total = real; extent = reserved; notify() } }
}

it('removes only the reserved tail as replies grow, retaining all historical height and native spacer', () => {
  const f = fixture()
  expect(f.doc.style.height).toBe('4200px')
  expect(adapter!.status().trimmed).toBe(400)
  expect(f.spacer.style.height).toBe('')
  f.grow(4400, 4600)
  expect(f.doc.style.height).toBe('4400px')
  expect(adapter!.status().trimmed).toBe(200)
  f.grow(4800, 4800)
  expect(f.doc.style.height).toBe('')
  expect(f.doc.style.overflow).toBe('')
  f.grow(4900, 5300) // a new turn reserves space again
  expect(f.doc.style.height).toBe('4900px')
  adapter!.dispose()
  expect(f.doc.style.height).toBe('')
  f.grow(5000, 5300)
  expect(f.doc.style.height).toBe('')
})

it('does not crop unrendered history when the tail is not in the DOM', () => {
  const f = fixture()
  expect(f.doc.querySelectorAll('.cr-document__virtual-item')).toHaveLength(0)
  expect(f.doc.style.height).toBe('4200px') // total native history, not the mounted range
  f.owner.memoizedProps.viewport.reserveTailAnchor = false
  adapter!.sync()
  expect(f.doc.style.height).toBe('')
  f.owner.memoizedProps.viewport.reserveTailAnchor = true
  f.grow(2000, 4600) // not a one-viewport reservation: do not trim
  expect(f.doc.style.height).toBe('')
})

it('reads the committed tree and restores native styles when the layout cannot be verified', () => {
  const f = fixture()
  const oldRoot = { stateNode: { current: f.root } }
  const old = { return: oldRoot, memoizedProps: {}, memoizedState: null }
  ;(f.doc as any).__reactFiber$test = { return: old, alternate: f.fiber }
  adapter!.sync()
  expect(f.doc.style.height).toBe('4200px')
  f.owner.memoizedProps.block.props.rootId = 'different'
  adapter!.sync()
  expect(f.doc.style.height).toBe('')
  expect(f.doc.style.overflow).toBe('')
})
