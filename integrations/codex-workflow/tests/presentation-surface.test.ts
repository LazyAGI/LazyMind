import {afterEach,expect,it,vi} from 'vitest'
import {JSDOM} from 'jsdom'
import {createCodexSurface} from '../src/presentation-surface'
let dom:JSDOM, cleanup:(()=>void)|undefined
afterEach(()=>{cleanup?.();dom?.window.close();vi.useRealTimers();vi.unstubAllGlobals()})
it('registers the native guest, forwards only its exact URL/run/mount, and releases it on disposal',async()=>{
 vi.useFakeTimers()
 dom=new JSDOM('<body></body>',{url:'app://-/index.html'})
 vi.stubGlobal('window',dom.window);vi.stubGlobal('document',dom.window.document)
 const events:any[]=[]
 const guest:any=document.createElement('div');let currentURL='about:blank'
 guest.getURL=()=>currentURL
 guest.loadURL=vi.fn(async(url:string)=>{currentURL=url})
 let snapshot:any
 guest.executeJavaScript=vi.fn(async(code:string)=>code.includes('.drain()')?events.splice(0):code.includes('.status()')?snapshot:undefined)
 const unregister=vi.fn(), native={webview:guest,container:document.createElement('div'),isRegistered:true,
  listenForDidRegister:vi.fn(()=>unregister),sync:vi.fn(),browserHostService:{syncView:vi.fn(async()=>true)}}
 const browser={pages:{getWebview:vi.fn(()=>native),disposeHost:vi.fn()}}
 ;(window as any).__lazymindCodexBrowser=browser
 const target={session_id:'thread',run_id:'run',mount_id:crypto.randomUUID()},receive=vi.fn()
 const surface=createCodexSurface(target,new URL('http://127.0.0.1:19091/v1/codex/frame'),receive,vi.fn());cleanup=surface.dispose
 surface.sync({x:1,y:2,width:600,height:400},{x:0,y:0,width:800,height:700},true)
 await Promise.resolve()
 expect(browser.pages.getWebview).toHaveBeenCalledWith('thread',target.mount_id,'',{hostKind:'right-panel'})
 expect(currentURL).toContain('native=1')
 snapshot={type:'lazymind.workflow.frame-status',status:'ready',sessionId:'other',bridgeId:target.mount_id}
 expect(await surface.probeStatus()).toBeUndefined()
 snapshot={...snapshot,sessionId:'run'}
 expect(await surface.probeStatus()).toMatchObject({status:'ready',sessionId:'run'})
 events.push({type:'lazymind.workflow.toggle-collapse',sessionId:'other',bridgeId:target.mount_id},{type:'lazymind.workflow.toggle-collapse',sessionId:'run',bridgeId:target.mount_id})
 await vi.advanceTimersByTimeAsync(150)
 expect(receive).toHaveBeenCalledTimes(1)
 surface.reply({type:'lazymind.workflow.collapse',collapsed:true})
 expect(guest.executeJavaScript).toHaveBeenLastCalledWith(expect.stringContaining('.receive('))
 currentURL='https://other.example/'
 expect(await surface.probeStatus()).toBeUndefined()
 const calls=guest.executeJavaScript.mock.calls.length
 surface.reply({type:'lazymind.workflow.expansion',expanded:true})
 await vi.advanceTimersByTimeAsync(150)
 expect(guest.executeJavaScript).toHaveBeenCalledTimes(calls)
 surface.dispose()
 expect(unregister).toHaveBeenCalledOnce()
 expect(browser.pages.disposeHost).toHaveBeenCalledWith('thread',target.mount_id,'closed')
})

it('observes Windows registration when the listener API is absent and stops observing on disposal',async()=>{
 vi.useFakeTimers()
 dom=new JSDOM('<body></body>',{url:'app://-/index.html'})
 vi.stubGlobal('window',dom.window);vi.stubGlobal('document',dom.window.document)
 const guest:any=document.createElement('div')
 guest.getURL=()=> 'about:blank'
 const native={webview:guest,container:document.createElement('div'),isRegistered:false}
 ;(window as any).__lazymindCodexBrowser={pages:{getWebview:()=>native,disposeHost:vi.fn()}}
 const changed=vi.fn()
 const surface=createCodexSurface({session_id:'thread',run_id:'run',mount_id:crypto.randomUUID()},new URL('http://127.0.0.1:19091/v1/codex/frame'),vi.fn(),changed)
 cleanup=surface.dispose
 await vi.advanceTimersByTimeAsync(100)
 expect(changed).not.toHaveBeenCalled()
 native.isRegistered=true
 await vi.advanceTimersByTimeAsync(100)
 expect(changed).toHaveBeenCalledOnce()
 surface.dispose()
 native.isRegistered=false
 await vi.advanceTimersByTimeAsync(200)
 expect(changed).toHaveBeenCalledOnce()
 expect(vi.getTimerCount()).toBe(0)
})
