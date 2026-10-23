import { afterEach, expect, it, vi } from 'vitest'
import { JSDOM } from 'jsdom'
import { mountPresentation, readPresentationSession, presentationExpression } from '../src/presentation'

let dom: JSDOM
afterEach(() => { (globalThis.window as any)?.__lazymindWorkflowTail?.dispose(); (globalThis.window as any)?.__lazymindWorkflowPresentationSession?.dispose(); dom?.window.close(); vi.unstubAllGlobals() })
function fixture() {
  dom = new JSDOM('<div class="conversation-shell__main"><div class="cr-message-list"><div class="cr-message-list__content"><div class="cr-document" data-root-id="native-1"></div></div></div><textarea></textarea></div>', { url: 'file:///workbuddy/index.html' })
  const window = dom.window as any
  window.HTMLElement.prototype.showPopover = vi.fn()
  window.HTMLElement.prototype.hidePopover = vi.fn()
  window.document.querySelector('.conversation-shell__main').getBoundingClientRect = () => ({ left: 100, top: 60, right: 1000, bottom: 750 })
  const listeners = new Set<() => void>()
  const saveAs = vi.fn(async (_options: { defaultFileName: string, contentBase64: string }) => ({ success: true }))
  window.workbuddyDesktop = { appVersion: '5.8.3' }
  window.wb = { conversations: { currentId: 'native-1', onCurrentChange: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn) } }, fileTransfer: { saveAs } }
  const doc = window.document.querySelector('.cr-document')
  doc.getBoundingClientRect = () => ({ width: 720 })
  for (const key of ['window', 'document', 'Blob', 'FileReader', 'getComputedStyle', 'Element', 'MutationObserver']) vi.stubGlobal(key, window[key])
  const target = { session_id: 'native-1', run_id: 'run-1', frame_url: 'http://127.0.0.1:19091/v1/workbuddy/frame', mount_id: crypto.randomUUID() }
  mountPresentation(target)
  const frame = window.document.querySelector('iframe')
  const reply = vi.spyOn(frame.contentWindow, 'postMessage')
  function message(data: object, source = frame.contentWindow, origin = 'http://127.0.0.1:19091') {
    window.dispatchEvent(new window.MessageEvent('message', { source, origin, data: { sessionId: 'run-1', bridgeId: target.mount_id, ...data } }))
  }
  return { window, frame, reply, message, target, saveAs, change: () => { for (const fn of listeners) fn() } }
}

it('keeps one iframe for the same run, replaces new runs, and removes on native session change', () => {
  const f = fixture()
  mountPresentation({ ...f.target, mount_id: crypto.randomUUID() })
  expect(document.querySelector('iframe')).toBe(f.frame)
  const tail = document.querySelector('[data-lazymind-workflow-tail]')!
  expect(tail.parentElement?.lastElementChild).toBe(tail)
  expect((tail as HTMLElement).style.maxWidth).toBe('720px')
  expect(mountPresentation({ ...f.target, session_id: 'native-other' }).status).toBe('stale')
  expect(document.querySelector('iframe')).toBe(f.frame)
  mountPresentation({ ...f.target, run_id: 'run-new' })
  expect(document.querySelectorAll('iframe')).toHaveLength(1)
  expect(document.querySelector('iframe')).not.toBe(f.frame)
  f.window.wb.conversations.currentId = 'unbound'
  f.change()
  expect(document.querySelector('iframe')).toBeNull()
})

it('requires the current frame, HTTP origin, run and mount before accepting commands', () => {
  const f = fixture()
  const command = { type: 'lazymind.workflow.toggle-expand' }
  f.message(command, f.window)
  f.message(command, f.frame.contentWindow, 'null')
  f.message({ ...command, sessionId: 'wrong' })
  f.message({ ...command, bridgeId: crypto.randomUUID() })
  expect(f.reply).not.toHaveBeenCalled()
  f.message(command)
  expect(f.frame.style.height).toBe('100%')
  f.message({ type: 'lazymind.workflow.toggle-collapse' })
  expect(f.frame.style.height).toBe('64px')
  expect(f.reply).toHaveBeenLastCalledWith(expect.objectContaining({ collapsed: true, bridgeId: f.target.mount_id }), 'http://127.0.0.1:19091')
})

it('expands across the conversation including its composer, retaining the iframe and inline position on shrink', () => {
  const f = fixture()
  const tail = document.querySelector<HTMLElement>('[data-lazymind-workflow-tail]')!
  const surface = f.frame.parentElement
  const content = tail.parentElement!
  tail.getBoundingClientRect = () => ({ height: 460 } as DOMRect)
  const originalWindow = f.frame.contentWindow
  const originalURL = f.frame.src
  f.message({ type: 'lazymind.workflow.toggle-expand' })
  expect(surface.getAttribute('popover')).toBe('manual')
  expect(surface.style.position).toBe('fixed')
  expect(surface.style.width).toBe('876px')
  expect(surface.style.height).toBe('666px')
  expect(surface.style.left).toBe('112px')
  expect(surface.style.top).toBe('72px')
  expect(tail.style.height).toBe('460px')
  expect(tail.parentElement).toBe(content)
  // A normal connector poll must not reset expanded geometry.
  mountPresentation(f.target)
  expect(surface.style.width).toBe('876px')
  f.window.document.querySelector('.conversation-shell__main').getBoundingClientRect = () => ({ left: 100, top: 60, right: 800, bottom: 650 })
  f.window.dispatchEvent(new f.window.Event('resize'))
  expect(surface.style.width).toBe('676px')
  expect(surface.style.height).toBe('566px')
  f.message({ type: 'lazymind.workflow.toggle-expand' })
  expect(surface.hasAttribute('popover')).toBe(false)
  expect(surface.style.position).toBe('')
  expect(tail.style.height).toBe('')
  expect(f.frame.style.height).toBe('clamp(360px, 60vh, 640px)')
  expect(document.querySelector('iframe')).toBe(f.frame)
  expect(f.frame.contentWindow).toBe(originalWindow)
  expect(f.frame.src).toBe(originalURL)
  expect(content.lastElementChild).toBe(tail)
})

it('fails to inline mode without changing iframe state when the overlay is unavailable', () => {
  const f = fixture()
  f.frame.parentElement.showPopover = () => { throw Error('unavailable') }
  f.message({ type: 'lazymind.workflow.toggle-expand' })
  expect(f.reply).toHaveBeenLastCalledWith(expect.objectContaining({ expanded: false }), expect.anything())
  expect(f.frame.parentElement.hasAttribute('popover')).toBe(false)
  expect(document.querySelector<HTMLElement>('[data-lazymind-workflow-tail]')!.style.height).toBe('')
  expect(f.frame.style.height).toBe('clamp(360px, 60vh, 640px)')
})

it('saves Blob bytes through native saveAs; cancellation, replay and invalid filenames do not succeed', async () => {
  const f = fixture()
  const request = { type: 'lazymind.workflow.download', action: 'save', filename: 'test.txt', blob: new Blob(['hello']), requestId: crypto.randomUUID() }
  f.message(request)
  await vi.waitFor(() => expect(f.reply).toHaveBeenCalledWith(expect.objectContaining({ ok: true }), expect.anything()))
  expect(f.saveAs).toHaveBeenCalledWith({ defaultFileName: 'test.txt', contentBase64: 'aGVsbG8=' })
  f.message(request)
  expect(f.saveAs).toHaveBeenCalledTimes(1)
  f.saveAs.mockResolvedValue({ success: false })
  const cancelled = { ...request, requestId: crypto.randomUUID() }
  f.message(cancelled)
  await vi.waitFor(() => expect(f.reply).toHaveBeenLastCalledWith(expect.objectContaining({ requestId: cancelled.requestId, ok: false }), expect.anything()))
  f.message({ ...request, filename: '../escape.txt', requestId: crypto.randomUUID() })
  expect(f.saveAs).toHaveBeenCalledTimes(2)
  expect(f.reply).toHaveBeenLastCalledWith(expect.objectContaining({ ok: false }), expect.anything())
})

it('saves files larger than 64 MiB through native saveAs', async () => {
  const f = fixture()
  const bytes = new Uint8Array(64 * 1024 * 1024 + 1).fill(97)
  const requestId = crypto.randomUUID()
  f.message({ type: 'lazymind.workflow.download', action: 'save', filename: 'large.bin', blob: new Blob([bytes]), requestId })
  await vi.waitFor(() => expect(f.reply).toHaveBeenCalledWith(expect.objectContaining({ requestId, ok: true }), expect.anything()))
  expect(f.saveAs).toHaveBeenCalledOnce()
  const saved = f.saveAs.mock.calls[0][0]
  expect(saved.defaultFileName).toBe('large.bin')
  expect(Buffer.from(saved.contentBase64, 'base64').equals(Buffer.from(bytes))).toBe(true)
})

it('removes an old panel when trusted lookup reports no binding', () => {
  const f = fixture()
  mountPresentation({ ...f.target, run_id: '' })
  expect(document.querySelector('iframe')).toBeNull()
  expect(f.window.__lazymindWorkflowTail).toBeUndefined()
})

it('rejects a late lookup from the previous conversation without touching the new panel', () => {
  const f = fixture()
  f.window.wb.conversations.currentId = 'native-2'
  f.change()
  const doc = document.querySelector<HTMLElement>('.cr-document')!
  doc.dataset.rootId = 'native-2'
  mountPresentation({ ...f.target, session_id: 'native-2', run_id: 'run-2' })
  const next = document.querySelector('iframe')
  expect(mountPresentation(f.target).status).toBe('stale')
  expect(document.querySelector('iframe')).toBe(next)
  expect(f.window.__lazymindWorkflowTail.status().run_id).toBe('run-2')
})

it('rejects old results even after switching away and back to the same session, or recreating the renderer gate', () => {
  const f = fixture()
  const first = readPresentationSession()!
  expect(readPresentationSession()!.epoch).toBe(first.epoch)
  f.window.wb.conversations.currentId = 'native-2'; f.change()
  f.window.wb.conversations.currentId = 'native-1'; f.change()
  const current = readPresentationSession()!
  expect(current.epoch).not.toBe(first.epoch)
  mountPresentation({ ...f.target, epoch: current.epoch })
  const frame = document.querySelector('iframe')
  expect(mountPresentation({ ...f.target, epoch: first.epoch, run_id: '' }).status).toBe('stale')
  expect(document.querySelector('iframe')).toBe(frame)
  f.window.__lazymindWorkflowPresentationSession.dispose()
  readPresentationSession()
  expect(mountPresentation({ ...f.target, epoch: current.epoch }).status).toBe('stale')
})

it('recovers a replaced timeline, reattaches following and keeps ordinary text updates in place', async () => {
  const f = fixture()
  const oldFollower = f.window.__lazymindWorkflowTail.tailFollow
  const content = document.querySelector<HTMLElement>('.cr-message-list__content')!
  const replacement = content.cloneNode(true) as HTMLElement
  replacement.querySelector('[data-lazymind-workflow-tail]')!.remove()
  replacement.querySelector<HTMLElement>('.cr-document')!.getBoundingClientRect = () => ({ width: 640 } as DOMRect)
  content.replaceWith(replacement)
  await vi.waitFor(() => expect(replacement.querySelector('iframe')).toBe(f.frame))
  expect(oldFollower.status().disposed).toBe(true)
  expect(f.window.__lazymindWorkflowTail.tailFollow.status().disposed).toBe(false)
  const liveFrame = f.frame.contentWindow
  replacement.querySelector('.cr-document')!.textContent = 'New streamed text'
  await Promise.resolve()
  expect(f.frame.contentWindow).toBe(liveFrame)
  expect(document.querySelectorAll('[data-lazymind-workflow-tail]')).toHaveLength(1)
  expect(replacement.lastElementChild?.tagName).toBe('SECTION')
})

it('hides a mismatched layout and restores it without destroying a still connected iframe', async () => {
  const f = fixture()
  const doc = document.querySelector<HTMLElement>('.cr-document')!
  const tail = document.querySelector<HTMLElement>('[data-lazymind-workflow-tail]')!
  const original = f.frame.contentWindow
  doc.dataset.rootId = 'wrong-session'
  await vi.waitFor(() => expect(tail.hidden).toBe(true))
  expect(f.window.__lazymindWorkflowTail.status().status).toBe('layout-unavailable')
  doc.dataset.rootId = 'native-1'
  await vi.waitFor(() => expect(tail.hidden).toBe(false))
  expect(f.frame.contentWindow).toBe(original)
})

it('synchronizes presentation state after the wrapper becomes ready and releases observers on disposal', async () => {
  const f = fixture()
  f.message({ type: 'lazymind.workflow.toggle-expand' })
  f.message({ type: 'lazymind.workflow.frame-status', status: 'ready' })
  expect(f.reply).toHaveBeenCalledWith(expect.objectContaining({ type: 'lazymind.workflow.expansion', expanded: true }), expect.anything())
  const mount = f.window.__lazymindWorkflowTail
  expect(mount.status().page_status).toBe('ready')
  const shell = document.querySelector('.conversation-shell__main')!
  mount.dispose()
  shell.append(document.createElement('div'))
  f.window.dispatchEvent(new f.window.Event('resize'))
  await Promise.resolve()
  expect(document.querySelector('iframe')).toBeNull()
  expect(mount.tailFollow.status().disposed).toBe(true)
})

it('wakes presentation immediately on session change, including a change before subscribing', async () => {
  const f = fixture()
  const first = readPresentationSession()!
  const gate = f.window.__lazymindWorkflowPresentationSession
  const awake = vi.fn()
  const pending = gate.wait(first.epoch).then(awake)
  await Promise.resolve()
  expect(awake).not.toHaveBeenCalled()
  f.window.wb.conversations.currentId = 'native-2'
  f.change()
  await pending
  expect(awake).toHaveBeenCalledOnce()
  await gate.wait(first.epoch) // no five-second delay when lookup raced a switch
  const next = readPresentationSession()!
  const cleanup = gate.wait(next.epoch)
  gate.dispose()
  await cleanup // stop also releases an outstanding CDP evaluation
})

 it.each(['5.8.4', '5.10.0', '6.0.0'])('mounts the existing panel contract on newer %s', version => {
   const f = fixture()
   f.window.workbuddyDesktop.appVersion = version
   f.window.__lazymindWorkflowTail.dispose()
   expect((0, eval)(presentationExpression(f.target)).status).toBe('mounted')
   expect(document.querySelectorAll('iframe')).toHaveLength(1)
 })
 it('distinguishes an old version from missing SDK capabilities', () => {
   const f = fixture()
   f.window.workbuddyDesktop.appVersion = '5.8.2'
   expect(mountPresentation(f.target).status).toBe('unsupported_version')
   expect(document.querySelector('iframe')).toBeNull()
   f.window.workbuddyDesktop.appVersion = '6.0.0'
   delete f.window.wb.conversations.onCurrentChange
   expect(mountPresentation(f.target).status).toBe('capability_unavailable')
   expect(document.querySelector('iframe')).toBeNull()
 })
