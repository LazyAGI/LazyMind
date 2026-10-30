import { afterEach, expect, it, vi } from 'vitest'
import { JSDOM } from 'jsdom'
import { readCodexView, readPresentationSession } from '../src/presentation-view'
import { mountPresentation } from '../src/presentation'

let dom: JSDOM
const threadA = '01a1770e-5cca-7100-81da-d9f69e3b02ab', threadB = '01a1770e-5cca-7100-81da-d9f69e3b02ac'
afterEach(() => {
  const host = globalThis.window as any
  host?.__lazymindCodexWorkflowTail?.dispose(); host?.__lazymindCodexPresentationSession?.dispose()
  dom?.window.close(); vi.unstubAllGlobals()
})
function fixture() {
  dom = new JSDOM('<main><div data-app-action-timeline-scroll data-pip-anchor-host="codex-main-thread"><div id="content"><div data-thread-user-message-navigation-content><div id="messages"></div></div><div class="sticky"></div></div><div data-thread-scroll-footer><button class="group/scroll-to-bottom"></button></div></div></main>', {url:'app://-/index.html'})
  const window = dom.window as any
  for (const key of ['window','document','Element','HTMLElement','Node','MutationObserver','getComputedStyle']) vi.stubGlobal(key,window[key])
  const scroller = document.querySelector<HTMLElement>('[data-app-action-timeline-scroll]')!
  scroller.getBoundingClientRect = () => ({width:800} as DOMRect)
  scroller.parentElement!.getBoundingClientRect = () => ({left:0,top:40,right:900,bottom:800} as DOMRect)
  Object.defineProperties(scroller,{scrollHeight:{get:()=>1600},clientHeight:{get:()=>800}})
  scroller.scrollTop = 800
  const props = {conversationId:threadA,hostId:'local'}
  ;(scroller as any).__reactFiber$fixture = {memoizedProps:props}
  window.HTMLElement.prototype.showPopover = vi.fn()
  window.HTMLElement.prototype.hidePopover = vi.fn()
  const current = readPresentationSession()
  const target = {session_id:threadA,run_id:'run-a',epoch:current.epoch,frame_url:'http://127.0.0.1:19091/v1/codex/frame',mount_id:crypto.randomUUID()}
  let receive = (_data:any)=>{}
  const element=document.createElement('div'), nativeFrame=document.createElement('iframe');element.append(nativeFrame)
  const surface={element,reply:vi.fn(),sync:vi.fn(),setExpanded:vi.fn(),probeStatus:vi.fn(async()=>({type:'lazymind.workflow.frame-status',status:'ready',sessionId:'run-a',bridgeId:target.mount_id})),dispose:()=>element.remove()}
  const factory=vi.fn((_target:any,_url:any,handler:any)=>{receive=handler;return surface})
  expect(mountPresentation(target,undefined,undefined,factory).status).toBe('mounted')
  const frame = document.querySelector('iframe')!, post = surface.reply
  const message = (data:object) => receive({sessionId:'run-a',bridgeId:target.mount_id,...data})
  return {target,frame,post,message,props,scroller,window,surface,factory}
}
it('keeps one live iframe after new messages and before composer clearance',async()=>{
  const f=fixture(), panel=document.querySelector('[data-lazymind-workflow-tail]')!
  document.querySelector('#messages')!.append(document.createElement('article'))
  await Promise.resolve()
  mountPresentation({...f.target,mount_id:crypto.randomUUID()})
  expect(document.querySelector('iframe')).toBe(f.frame)
  expect(panel.previousElementSibling).toBe(document.querySelector('[data-thread-user-message-navigation-content]'))
  expect(panel.nextElementSibling?.className).toBe('sticky')
})
it('confirms a loaded page even when its queued ready event has not been forwarded',async()=>{
 const f=fixture(),panel=f.window.__lazymindCodexWorkflowTail
 expect(panel.status().page_status).toBe('loading')
 expect(await panel.probeStatus()).toMatchObject({status:'mounted',page_status:'ready',session_id:threadA,run_id:'run-a'})
 expect(f.surface.probeStatus).toHaveBeenCalledOnce()
})
it('rejects a ready probe resolved after switching conversations',async()=>{
 const f=fixture(),panel=f.window.__lazymindCodexWorkflowTail
 let resolve!:(value:any)=>void
 f.surface.probeStatus.mockImplementationOnce(()=>new Promise(r=>{resolve=r}))
 const pending=panel.probeStatus()
 f.props.conversationId=threadB;readPresentationSession()
 resolve({type:'lazymind.workflow.frame-status',status:'ready',sessionId:'run-a',bridgeId:f.target.mount_id})
 expect(await pending).toBeUndefined()
})
it('keeps iframe and URL through collapse/expand and rejects foreign messages',()=>{
  const f=fixture(), url=f.frame.src
  f.message({type:'lazymind.workflow.toggle-expand',bridgeId:'foreign'})
  expect(f.post).not.toHaveBeenCalled()
  f.message({type:'lazymind.workflow.toggle-expand'})
  expect(f.surface.setExpanded).toHaveBeenLastCalledWith(true)
  f.message({type:'lazymind.workflow.toggle-collapse'})
  expect(f.surface.element.style.height).toBe('64px')
  f.message({type:'lazymind.workflow.toggle-collapse'})
  expect(document.querySelector('iframe')).toBe(f.frame)
  expect(f.frame.src).toBe(url)
})
it('disposes immediately on chat change and rejects an old result after A to B to A',()=>{
  const f=fixture()
  f.props.conversationId=threadB
  readPresentationSession()
  expect(document.querySelector('iframe')).toBeNull()
  f.props.conversationId=threadA
  const current=readPresentationSession()
  expect(current.epoch).not.toBe(f.target.epoch)
  expect(mountPresentation(f.target).status).toBe('stale')
  expect(mountPresentation({...f.target,epoch:current.epoch},undefined,undefined,f.factory).status).toBe('mounted')
})
it('does not drag a reader to the bottom when mounting, collapsing or receiving messages',async()=>{
  const f=fixture()
  f.scroller.dispatchEvent(new dom.window.WheelEvent('wheel',{deltaY:-50}))
  f.scroller.scrollTop=300
  f.message({type:'lazymind.workflow.toggle-collapse'})
  document.querySelector('#messages')!.append(document.createElement('article'))
  await Promise.resolve()
  mountPresentation(f.target)
  expect(f.scroller.scrollTop).toBe(300)
})
it('rejects hidden or ambiguous main-chat layouts',()=>{
  const f=fixture()
  const other=f.scroller.cloneNode(true) as HTMLElement
  other.getBoundingClientRect=()=>({width:500} as DOMRect)
  document.body.append(other)
  expect(readCodexView()).toBeNull()
  other.getBoundingClientRect=()=>({width:0} as DOMRect)
  expect(readCodexView()?.session).toBe(threadA)
})
it('moves scroll listeners with a replacement scroller while preserving the panel',async()=>{
  const f=fixture(), next=f.scroller.cloneNode(true) as HTMLElement
  next.querySelector('[data-lazymind-workflow-tail]')?.remove()
  next.getBoundingClientRect=f.scroller.getBoundingClientRect
  ;(next as any).__reactFiber$fixture={memoizedProps:f.props}
  Object.defineProperties(next,{scrollHeight:{get:()=>1600},clientHeight:{get:()=>800}})
  next.scrollTop=800
  f.scroller.replaceWith(next)
  const current=readPresentationSession()
  mountPresentation({...f.target,epoch:current.epoch},undefined,undefined,f.factory)
  await Promise.resolve();await Promise.resolve()
  expect(document.querySelector('iframe')).toBe(f.frame)
  expect(f.factory).toHaveBeenCalledOnce()
  f.surface.sync.mockClear()
  f.scroller.dispatchEvent(new dom.window.Event('scroll'))
  expect(f.surface.sync).not.toHaveBeenCalled()
  next.dispatchEvent(new dom.window.WheelEvent('wheel',{deltaY:-50}))
  next.scrollTop=300
  next.dispatchEvent(new dom.window.Event('scroll'))
  expect(next.scrollTop).toBe(300)
  expect(f.surface.sync).toHaveBeenCalledOnce()
  f.window.__lazymindCodexWorkflowTail.dispose()
  f.surface.sync.mockClear()
  next.dispatchEvent(new dom.window.Event('scroll'))
  expect(f.surface.sync).not.toHaveBeenCalled()
})
