import {afterEach,expect,it,vi} from 'vitest'
import {JSDOM} from 'jsdom'
import {startPresentationFrame} from '../src/presentation-frame'
let dom:JSDOM
afterEach(()=>{dom?.window.dispatchEvent(new dom.window.Event('pagehide'));dom?.window.close();vi.unstubAllGlobals()})
it('relays native guest controls only from its own frontend and validates host replies',async()=>{
 const mount=crypto.randomUUID()
 dom=new JSDOM('<meta name="lazymind-workflow-frontend" content="http://localhost:8090"><meta name="lazymind-workflow-downloads" content="browser"><p id="notice"></p><button id="retry"></button><iframe id="workflow"></iframe>',{url:`http://127.0.0.1:19091/v1/codex/frame#run=run&mount=${mount}&native=1`})
 const window=dom.window as any
 for(const key of ['window','document','location','parent'])vi.stubGlobal(key,window[key])
 vi.stubGlobal('fetch',vi.fn(async()=>({ok:true})))
 startPresentationFrame();await Promise.resolve();await Promise.resolve()
 const frame=document.querySelector<HTMLIFrameElement>('#workflow')!
 expect(new URL(frame.src).searchParams.get('hostOrigin')).toBe('http://127.0.0.1:19091')
 expect(new URL(frame.src).searchParams.has('downloadHostOrigin')).toBe(false)
 const post=vi.spyOn(frame.contentWindow!,'postMessage'),bridge=window.__lazymindWorkflowFrame
 frame.dispatchEvent(new window.Event('load'))
 expect(bridge.drain().some((event:any)=>event.status==='ready')).toBe(true)
 // Draining the notification must not erase readiness for a late probe.
 expect(bridge.status()).toMatchObject({status:'ready',sessionId:'run',bridgeId:mount})
 const data={type:'lazymind.workflow.toggle-collapse',sessionId:'run'}
 window.dispatchEvent(new window.MessageEvent('message',{source:frame.contentWindow,origin:'https://other.example',data}))
 window.dispatchEvent(new window.MessageEvent('message',{source:frame.contentWindow,origin:'http://localhost:8090',data}))
 const toggles=bridge.drain().filter((x:any)=>x.type==='lazymind.workflow.toggle-collapse')
 expect(toggles).toEqual([{...data,bridgeId:mount}])
 bridge.receive({type:'lazymind.workflow.collapse',sessionId:'other',bridgeId:mount,collapsed:true})
 expect(post).not.toHaveBeenCalled()
 bridge.receive({type:'lazymind.workflow.collapse',sessionId:'run',bridgeId:mount,collapsed:true})
 expect(post).toHaveBeenCalledWith(expect.objectContaining({collapsed:true}),'http://localhost:8090')
})
