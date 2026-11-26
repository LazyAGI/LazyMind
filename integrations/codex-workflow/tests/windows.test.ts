import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {CodexNative} from '../src/native'
import type {LookupBinding} from '../../workflow-agent-core/src/adapter'
import {presentationLoop,type PresentationStatus,type PresentationTarget} from '../src/presentation'
import {isCodexChatTarget,selectCodexTarget} from '../src/windows'
const model=vi.hoisted(()=>({targets:[] as any[],calls:[] as any[],aborted:[] as string[],reads:new Map<string,any>(),failStart:false}))
vi.mock('node:timers/promises',()=>({setTimeout:(ms:number,_value:any,options?:{signal?:AbortSignal})=>new Promise((resolve,reject)=>{
 const signal=options?.signal
 if(signal?.aborted)return reject(signal.reason)
 const timer=setTimeout(()=>{signal?.removeEventListener('abort',abort);resolve(undefined)},ms)
 function abort(){clearTimeout(timer);reject(signal?.reason)}
 signal?.addEventListener('abort',abort,{once:true})
})}))
vi.mock('../../workflow-agent-core/node/cdp',()=>({CdpClient:class {
 constructor(_port:number,private select:(targets:any[])=>any){}
 async evaluate(expression:string,signal:AbortSignal){
  const target=this.select(model.targets)
  if(expression.includes('?.wait('))return new Promise((resolve,reject)=>{
   const timer=setTimeout(resolve,5000)
   signal.addEventListener('abort',()=>{model.aborted.push(target.id);clearTimeout(timer);reject(signal.reason)},{once:true})
  })
  if(expression.startsWith('window.__lazymindCodexWorkflowTail?.dispose()')){model.calls.push({target:target.id,kind:'cleanup'});return}
  if(expression.includes('function codexOperation(')){
   const input=JSON.parse(expression.slice(expression.lastIndexOf(')(')+2,-1));model.calls.push({target:target.id,...input})
   if(input.kind==='probe')return{ready:true}
   if(input.kind==='read')return model.reads.get(target.id)??{missing:true}
   if(input.kind==='start'&&model.failStart)throw Error('disconnected after admission')
   return {accepted:true,turnId:'new-turn'}
  }
  if(expression.includes('mountPresentation')){
   const payloadStart=expression.indexOf('({"session_id":')+1
   const payloadEnd=expression.indexOf(',()=>',payloadStart)
   const {session_id,run_id}:PresentationTarget=JSON.parse(expression.slice(payloadStart,payloadEnd))
   model.calls.push({target:target.id,kind:run_id?'mount':'unmount',session_id,run_id})
   return run_id?{status:'mounted',page_status:target.id==='b'?'frontend-unavailable':'ready'}:{status:'unbound'}
  }
  return{session_id:target.sessionId,epoch:'epoch-'+target.id}
 }
}}))
beforeEach(()=>{
 vi.useFakeTimers();model.calls=[];model.aborted=[];model.reads.clear();model.failStart=false
 model.targets=[{id:'a',sessionId:'thread-design',type:'page',url:'app://-/index.html'},{id:'b',sessionId:'thread-review',type:'page',url:'app://-/detached-window.html?initialRoute=%2Fdetached-window'},{id:'avatar',sessionId:'thread-overlay',type:'page',url:'app://-/index.html?initialRoute=%2Favatar-overlay'}]
 vi.stubGlobal('fetch',vi.fn(async()=>({ok:true,json:async()=>model.targets})))
})
afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals()})
it('recognizes main and detached chat windows while excluding auxiliary windows',()=>{
 expect(model.targets.map(isCodexChatTarget)).toEqual([true,true,false])
 expect(isCodexChatTarget({type:'page',url:'app://-/index.html?initialRoute=%2Flocal%2F01a16196-3096-7dd3-8c20-33cd9250503c'})).toBe(true)
 expect(selectCodexTarget(model.targets,'b').id).toBe('b')
 expect(()=>selectCodexTarget(model.targets)).toThrow()
})
it('mounts workflows by chat session across reopened windows and keeps an unbound window unmounted',async()=>{
 const stop=new AbortController(),reports:PresentationStatus[]=[]
 const bindings=new Map([['thread-design','run-spec'],['thread-review','run-approval']])
 const lookup=vi.fn<LookupBinding>(async session=>{
  const runId=bindings.get(session)
  return runId?{status:'bound',runId}:{status:'unbound'}
 })
 const mounts=()=>model.calls.filter(call=>call.kind==='mount').sort((first,second)=>first.target.localeCompare(second.target))
 const initialMounts=[
  {target:'a',kind:'mount',session_id:'thread-design',run_id:'run-spec'},
  {target:'b',kind:'mount',session_id:'thread-review',run_id:'run-approval'},
 ]
 const running=presentationLoop(9334,lookup,'http://127.0.0.1:19091',stop.signal,status=>reports.push(status))
 try{
  await vi.waitFor(()=>expect(mounts()).toEqual(initialMounts))
  expect(reports.at(-1)).toMatchObject({status:'mounted',page_status:'frontend-unavailable',window_count:2})
  model.targets=model.targets.filter(target=>target.id!=='a')
  model.targets.push(
   {id:'c',sessionId:'thread-design',type:'page',url:'app://-/index.html?initialRoute=%2Flocal%2F01a16196-3096-7dd3-8c20-33cd9250503c'},
   {id:'d',sessionId:'thread-draft',type:'page',url:'app://-/detached-window.html?initialRoute=%2Fdetached-window'},
  )
  await vi.advanceTimersByTimeAsync(1100)
  expect(mounts()).toEqual([...initialMounts,{target:'c',kind:'mount',session_id:'thread-design',run_id:'run-spec'}])
  expect(model.calls.filter(call=>call.target==='d')).toEqual([
   {target:'d',kind:'unmount',session_id:'thread-draft',run_id:''},
  ])
  expect(model.aborted).toContain('a')
  expect(lookup.mock.calls.map(([session])=>session).sort()).toEqual(['thread-design','thread-design','thread-draft','thread-review'])
  expect(reports.at(-1)).toMatchObject({window_count:3,windows:[
   {target_id:'b',status:'mounted'},
   {target_id:'c',status:'mounted',native_session_id:'thread-design',run_id:'run-spec'},
   {target_id:'d',status:'unbound',native_session_id:'thread-draft'},
  ]})
 }finally{stop.abort();await running}
 expect(model.calls.filter(x=>x.kind==='cleanup').map(x=>x.target).sort()).toEqual(['b','c','d'])
})
it('routes native control by the bound thread owner and never retries a failed mutation elsewhere',async()=>{
 const thread='01a1770e-5cca-7100-81da-d9f69e3b02ab',signal=new AbortController().signal
 model.reads.set('a',{id:thread,active:false,turns:[],owner:false});model.reads.set('b',{id:thread,active:false,turns:[],owner:true})
 const native=new CodexNative(9334)
 expect((await native.probe(signal)).ready).toBe(true)
 await native.start(thread,'client','continue',signal)
 expect(model.calls.filter(x=>x.kind==='start').map(x=>x.target)).toEqual(['b'])
 model.calls=[];model.failStart=true
 await expect(native.start(thread,'client2','continue',signal)).rejects.toThrow('disconnected')
 expect(model.calls.filter(x=>x.kind==='start').map(x=>x.target)).toEqual(['b'])
})

it.each([['window','window'],['host','window'],[undefined,undefined]])('still rejects independent or mixed owner scopes %s/%s',async(a,b)=>{
 const thread='01a1770e-5cca-7100-81da-d9f69e3b02ab',signal=new AbortController().signal
 model.reads.set('a',{id:thread,active:false,turns:[],owner:true,ownerScope:a})
 model.reads.set('b',{id:thread,active:false,turns:[],owner:true,ownerScope:b})
 await expect(new CodexNative(9334).start(thread,'client','continue',signal)).rejects.toThrow('conflicting owners')
 expect(model.calls.some(x=>x.kind==='start')).toBe(false)
})

it('chooses one shared host client and does not retry an uncertain mutation in another window',async()=>{
 const thread='01a1770e-5cca-7100-81da-d9f69e3b02ab',signal=new AbortController().signal
 for(const id of ['a','b'])model.reads.set(id,{id:thread,active:false,turns:[],owner:true,ownerScope:'host'})
 model.targets.reverse();model.failStart=true
 await expect(new CodexNative(9334).start(thread,'client','continue',signal)).rejects.toThrow('disconnected')
 expect(model.calls.filter(x=>x.kind==='start').map(x=>x.target)).toEqual(['a'])
})
