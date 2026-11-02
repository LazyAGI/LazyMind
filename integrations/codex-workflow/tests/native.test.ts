import {afterEach,describe,it,expect,vi} from 'vitest'
import {codexOperation,nativeExpression,selectCodexTarget} from '../src/native'
import {workflowTurnEvidence} from '../src/ownership'
afterEach(()=>{vi.unstubAllGlobals();delete (globalThis as any).__lazymindCodexDiscovery;delete (globalThis as any).__lazymindCodexManager})
describe('Codex renderer native API mapping',()=>{
 const rpcFixture=(rpc:any)=>{
  const state={status:'ready',client:{hostId:'local',rpc}}
  const cell={atom:{init:null},store:{get:()=>state},set:vi.fn(),get:vi.fn(()=>{throw Error('must not evaluate cells')})}
  const client={node:{familyBindings:new Map([[{kind:'signal-family'},new Map([['local',{value:cell}]])]])}}
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:{memoizedState:{memoizedState:{current:client}}}}})
  vi.stubGlobal('document',{querySelectorAll:()=>[]});vi.stubGlobal('Node',class {})
  return {state,cell}
 }
 it('discovers the existing desktop RPC and reads an asynchronous owned conversation',async()=>{
  const disposed=vi.fn(),roleDisposed=vi.fn()
  const rpc=Object.assign(()=>{}, {
   getHostId:vi.fn().mockResolvedValue('local'),
   getConversation:vi.fn().mockImplementation(async()=>({id:'thread',resumeState:'resumed',turnHistory:{kind:'canonical',history:{islands:[{entries:[{value:'t'}]}],entitiesByKey:{t:{turnId:'turn',status:'completed',items:[]}}}},[Symbol.dispose]:disposed})),
   getStreamRole:vi.fn().mockImplementation(async()=>({role:'owner',[Symbol.dispose]:roleDisposed})),
   startTurn:vi.fn(),interruptConversation:vi.fn(),
  })
  const {cell}=rpcFixture(rpc)
  expect(await (0,eval)(nativeExpression({kind:'probe'}))).toEqual({ready:true})
  expect(await (0,eval)(nativeExpression({kind:'read',threadId:'thread'}))).toMatchObject({id:'thread',ready:true,owner:true,active:false,turns:[{id:'turn'}]})
  expect(disposed).toHaveBeenCalledOnce();expect(roleDisposed).toHaveBeenCalledOnce()
  expect(cell.get).not.toHaveBeenCalled();expect(rpc.startTurn).not.toHaveBeenCalled();expect(rpc.interruptConversation).not.toHaveBeenCalled()
 })
 it('does not confuse a config-only RPC with the manager connection or retain a broken RPC',async()=>{
  const rpc=Object.assign(()=>{}, {getHostId:vi.fn().mockResolvedValue('local')})
  const {state,cell}=rpcFixture(rpc)
  cell.store.get=()=>state.client as any
  expect(await codexOperation({kind:'probe'})).toEqual({ready:false})
  expect(rpc.getHostId).not.toHaveBeenCalled()
  delete (globalThis as any).__lazymindCodexDiscovery
  cell.store.get=()=>state
  expect(await codexOperation({kind:'probe'})).toEqual({ready:true})
  rpc.getHostId.mockRejectedValue(Error('RPC closed'))
  expect(await codexOperation({kind:'probe'})).toEqual({ready:false})
  expect((globalThis as any).__lazymindCodexManager).toBeUndefined()
 })
 it('uses the existing RPC admission and waits for native ownership before starting',async()=>{
  let role:any=null
  const disposeResult=vi.fn()
  const rpc=Object.assign(()=>{}, {
   getHostId:vi.fn().mockResolvedValue('local'),
   getConversation:vi.fn().mockResolvedValue({id:'thread',resumeState:'resumed',turns:[]}),
   getStreamRole:vi.fn().mockImplementation(async()=>role),
   startTurn:vi.fn().mockResolvedValue({turnId:'receipt',[Symbol.dispose]:disposeResult}),
   interruptConversation:vi.fn(),
  })
  rpcFixture(rpc)
  const input={kind:'start',threadId:'thread',clientId:'client',message:'continue'}
  expect(await codexOperation(input)).toMatchObject({retryable:true})
  expect(rpc.startTurn).not.toHaveBeenCalled()
  role={role:'owner'}
  expect(await codexOperation(input)).toEqual({accepted:true,turnId:'receipt'})
  expect(rpc.startTurn).toHaveBeenCalledWith('thread',expect.objectContaining({request:expect.objectContaining({clientUserMessageId:'client'}),context:expect.objectContaining({useUserMessageAdmission:false})}))
  expect(disposeResult).toHaveBeenCalledOnce()
 })
 it('rechecks RPC ownership after asynchronous reads before interrupting',async()=>{
  const turn={id:'turn',status:'inProgress',items:[]}
  const rpc=Object.assign(()=>{}, {
   getHostId:vi.fn().mockResolvedValue('local'),
   getConversation:vi.fn().mockResolvedValue({id:'thread',resumeState:'resumed',turns:[turn]}),
   getStreamRole:vi.fn().mockResolvedValueOnce({role:'owner'}).mockResolvedValue({role:'follower'}),
   interruptConversation:vi.fn(),
  })
  rpcFixture(rpc)
  expect(await codexOperation({kind:'interrupt',threadId:'thread',turnId:'turn',expectedOwnership:JSON.stringify({inputs:[]})})).toHaveProperty('rejected')
  expect(rpc.interruptConversation).not.toHaveBeenCalled()
 })
 it('discovers the manager held by a desktop signal client ref',async()=>{
  const manager={getHostId:()=> 'local',getConversation:vi.fn(),startTurn:vi.fn(),interruptConversation:vi.fn()}
  const client={node:{familyBindings:new Map([[{kind:'signal-family'},new Map([['local',{value:{atom:{init:null},store:{get:()=>manager},set:vi.fn()}}]])]])}}
  vi.stubGlobal('__lazymindCodexManager',undefined)
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:{memoizedState:{memoizedState:{current:client}}}}})
  vi.stubGlobal('document',{querySelectorAll:()=>[]})
  vi.stubGlobal('Node',class {})
  expect(await codexOperation({kind:'probe'})).toEqual({ready:true})
  expect(manager.startTurn).not.toHaveBeenCalled()
 })
 it('never evaluates derived cells, including cells that grow the registry when read',async()=>{
  const family=new Map<string,any>(),get=vi.fn(()=>{if(family.size<64)family.set(crypto.randomUUID(),{value:{atom:{},store:{},get}});return {}})
  family.set('local',{value:{atom:{},store:{},get}})
  const accessor=vi.fn(()=>{throw Error('application getter was invoked')})
  const client={node:{familyBindings:new Map([[{kind:'readable-family'},family]])}}
  Object.defineProperty(client,'getHostId',{get:accessor})
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:{memoizedState:{memoizedState:{current:client}}}}})
  vi.stubGlobal('document',{querySelectorAll:()=>[]});vi.stubGlobal('Node',class {})
  expect(await (0,eval)(nativeExpression({kind:'probe'}))).toEqual({ready:false})
  expect(get).not.toHaveBeenCalled();expect(accessor).not.toHaveBeenCalled();expect(family.size).toBe(1)
 })
 it('reads only the primitive local registry slot and never calls cell.get',async()=>{
  const manager={getHostId:()=> 'local',getConversation:vi.fn(),startTurn:vi.fn(),interruptConversation:vi.fn()}
  const forbidden=vi.fn(()=>{throw Error('derived subscription')})
  const cell={atom:{init:null},store:{get:vi.fn(()=>manager)},set:vi.fn(),get:forbidden}
  const client={node:{familyBindings:new Map([
   [{kind:'readable-family'},new Map([['local',{value:{...cell,store:{get:forbidden}}}]])],
   [{kind:'signal-family'},new Map([['remote',{value:{...cell,store:{get:forbidden}}}],['local',{value:cell}]])],
  ])}}
  vi.stubGlobal('__lazymindCodexManager',{oldInterface:true})
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:{memoizedState:{memoizedState:{current:client}}}}})
  vi.stubGlobal('document',{querySelectorAll:()=>[]});vi.stubGlobal('Node',class {})
  expect(await codexOperation({kind:'probe'})).toEqual({ready:true})
  expect(forbidden).not.toHaveBeenCalled();expect(cell.store.get).toHaveBeenCalledOnce();expect(cell.store.get).toHaveBeenCalledWith(cell.atom)
 })
 it('backs off failed discovery and discovers a registry initialized later',async()=>{
  let now=0
  vi.stubGlobal('performance',{now:()=>now})
  const root:any={memoizedState:null},querySelectorAll=vi.fn(()=>[])
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:root}})
  vi.stubGlobal('document',{querySelectorAll});vi.stubGlobal('Node',class {})
  expect(await codexOperation({kind:'probe'})).toEqual({ready:false})
  const manager={getHostId:()=> 'local',getConversation:vi.fn(),startTurn:vi.fn(),interruptConversation:vi.fn()}
  root.memoizedState={memoizedState:{current:{node:{familyBindings:new Map([[{kind:'signal-family'},new Map([['local',{value:{atom:{init:null},store:{get:()=>manager},set:vi.fn()}}]])]])}}}}
  now=4999
  expect(await codexOperation({kind:'probe'})).toEqual({ready:false})
  expect(querySelectorAll).toHaveBeenCalledOnce()
  now=5000
  expect(await codexOperation({kind:'probe'})).toEqual({ready:true})
 })
 it('bounds large React trees even when the clock does not advance',async()=>{
  vi.stubGlobal('performance',{now:()=>0})
  let visited=0
  const fiber=():any=>({memoizedState:null,get child(){visited++;return fiber()}})
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:fiber()}})
  vi.stubGlobal('document',{querySelectorAll:()=>[]});vi.stubGlobal('Node',class {})
  expect(await codexOperation({kind:'probe'})).toEqual({ready:false})
  expect(visited).toBeLessThanOrEqual(8000)
  expect(visited).toBeGreaterThan(0)
 })
 it('selects the main chat when the avatar overlay shares its entry point',()=>{
  const main={type:'page',url:'app://-/index.html',id:'main'}
  const overlay={type:'page',url:'app://-/index.html?initialRoute=%2Favatar-overlay',id:'avatar'}
  expect(selectCodexTarget([overlay,main])).toBe(main)
  expect(()=>selectCodexTarget([overlay])).toThrow()
  expect(()=>selectCodexTarget([main,{...main,id:'another'}])).toThrow()
 })
 it('uses the desktop admission path and accepts its native turnId receipt',async()=>{
  const manager={getHostId:()=> 'local',getStreamRole:()=>({role:'owner'}),isConversationStreaming:()=>true,getConversation:()=>({id:'thread',resumeState:'resumed',turns:[]}),startTurn:vi.fn().mockResolvedValue({turnId:'real-turn'}),interruptConversation:vi.fn()}
  vi.stubGlobal('__lazymindCodexManager',manager)
  expect(await codexOperation({kind:'start',threadId:'thread',clientId:'client',message:'continue'})).toEqual({accepted:true,turnId:'real-turn'})
  expect(manager.startTurn).toHaveBeenCalledWith('thread',expect.objectContaining({request:expect.objectContaining({threadId:'thread',clientUserMessageId:'client'}),context:expect.objectContaining({inheritThreadSettings:true,useUserMessageAdmission:false})}))
 })
 it('does not treat an idle placeholder displayed before native resume as ready',async()=>{
  let resumed=false
  const manager={getHostId:()=> 'local',getStreamRole:()=>resumed?{role:'owner'}:null,isConversationStreaming:()=>resumed,
   getConversation:()=>({id:'thread',resumeState:resumed?'resumed':null,turns:[],threadRuntimeStatus:{type:'idle'}}),startTurn:vi.fn().mockResolvedValue({turnId:'accepted'}),interruptConversation:vi.fn()}
  vi.stubGlobal('__lazymindCodexManager',manager)
  expect(await codexOperation({kind:'read',threadId:'thread'})).toMatchObject({active:false,ready:false})
  expect(await codexOperation({kind:'start',threadId:'thread',clientId:'client',message:'resume'})).toMatchObject({retryable:true})
  expect(manager.startTurn).not.toHaveBeenCalled()
  resumed=true
  expect(await codexOperation({kind:'start',threadId:'thread',clientId:'client',message:'resume'})).toEqual({accepted:true,turnId:'accepted'})
 })
 it('passes the expected turn to the native interrupt method',async()=>{
  const manager={getHostId:()=> 'local',getStreamRole:()=>({role:'owner'}),getConversation:()=>({id:'thread',turns:[{turnId:'target',status:'inProgress',items:[]}]}),startTurn:vi.fn(),interruptConversation:vi.fn().mockResolvedValue('target')}
  vi.stubGlobal('__lazymindCodexManager',manager)
  expect(await codexOperation({kind:'interrupt',threadId:'thread',turnId:'target',expectedOwnership:JSON.stringify({inputs:[]})})).toEqual({interruptedTurnId:'target'})
  expect(manager.interruptConversation).toHaveBeenCalledWith('thread','user-stop','target')
 })
 it.each(['steering', 'other-workflow', 'follower', 'ordinary-tool'])(
  'rechecks ownership in the serialized renderer operation after %s changes',async(change)=>{
   const turn={id:'target',turnId:'target',status:'inProgress',items:[
    {type:'userMessage',id:'original',content:[{type:'text',text:'start workflow'}]},
    {type:'mcpToolCall',id:'start',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-1'}}},
   ] as any[]}
   const input={kind:'interrupt',threadId:'thread',turnId:'target',expectedOwnership:JSON.stringify(workflowTurnEvidence(turn))}
   if(change==='steering')turn.items.push({type:'steeringUserMessage',id:'steer',status:'pending',input:[{type:'text',text:'another task'}]})
   if(change==='other-workflow')turn.items.push({type:'mcpToolCall',id:'start-other',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-2'}}})
   if(change==='ordinary-tool')turn.items.push({type:'commandExecution',id:'shell',status:'inProgress'})
   const manager={getHostId:()=> 'local',getStreamRole:()=>({role:change==='follower'?'follower':'owner'}),
    getConversation:()=>({id:'thread',turnHistory:{kind:'canonical',history:{islands:[{entries:[{value:'target'}]}],entitiesByKey:{target:turn}}}}),
    startTurn:vi.fn(),interruptConversation:vi.fn().mockResolvedValue('target')}
   vi.stubGlobal('__lazymindCodexManager',manager)
   const result=await (0,eval)(nativeExpression(input))
   if(change==='ordinary-tool') {
    expect(result).toEqual({interruptedTurnId:'target'})
    expect(manager.interruptConversation).toHaveBeenCalledOnce()
   } else {
    expect(result.rejected).toContain('ownership changed')
    expect(manager.interruptConversation).not.toHaveBeenCalled()
   }
  })
})
