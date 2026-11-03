import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {CodexNative} from '../src/native'
import {presentationLoop} from '../src/presentation'
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
   const expected='"run_id":"run-'+target.id+'"'
   if(!expression.includes(expected))throw Error('wrong window/run')
   model.calls.push({target:target.id,kind:'mount'});return{status:'mounted',page_status:'ready'}
  }
  return{session_id:'thread-'+target.id,epoch:'epoch-'+target.id}
 }
}}))
beforeEach(()=>{
 vi.useFakeTimers();model.calls=[];model.aborted=[];model.reads.clear();model.failStart=false
 model.targets=[{id:'a',type:'page',url:'app://-/index.html'},{id:'b',type:'page',url:'app://-/detached-window.html?initialRoute=%2Fdetached-window'},{id:'avatar',type:'page',url:'app://-/index.html?initialRoute=%2Favatar-overlay'}]
 vi.stubGlobal('fetch',vi.fn(async()=>({ok:true,json:async()=>model.targets})))
})
afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals()})
it('recognizes main and detached chat windows while excluding auxiliary windows',()=>{
 expect(model.targets.map(isCodexChatTarget)).toEqual([true,true,false])
 expect(isCodexChatTarget({type:'page',url:'app://-/index.html?initialRoute=%2Flocal%2F01a16196-3096-7dd3-8c20-33cd9250503c'})).toBe(true)
 expect(selectCodexTarget(model.targets,'b').id).toBe('b')
 expect(()=>selectCodexTarget(model.targets)).toThrow()
})
it('mounts every window and discovers new/closed windows without duplicating the shared delivery worker',async()=>{
 const stop=new AbortController(),reports:any[]=[]
 const lookup=vi.fn(async(session:string)=>({session_id:session,run_id:'run-'+session.replace('thread-','')}))
 const running=presentationLoop(9334,lookup,'http://127.0.0.1:19091',stop.signal,status=>reports.push(status))
 try{
  await vi.waitFor(()=>expect(model.calls.filter(x=>x.kind==='mount').map(x=>x.target).sort()).toEqual(['a','b']))
  model.targets=model.targets.filter(x=>x.id!=='a');model.targets.push({id:'c',type:'page',url:'app://-/index.html?initialRoute=%2Flocal%2F01a16196-3096-7dd3-8c20-33cd9250503c'})
  await vi.advanceTimersByTimeAsync(1100)
  expect(model.calls.some(x=>x.kind==='mount'&&x.target==='c')).toBe(true)
  expect(model.aborted).toContain('a')
  expect(lookup.mock.calls.some(([session])=>session==='thread-avatar')).toBe(false)
  expect(reports.some(x=>x.window_count===2&&x.windows?.every((w:any)=>w.target_id!=='a')&&x.windows?.some((w:any)=>w.target_id==='c'))).toBe(true)
 }finally{stop.abort();await running}
 expect(model.calls.filter(x=>x.kind==='cleanup').map(x=>x.target).sort()).toEqual(['b','c'])
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
