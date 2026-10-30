import {afterEach,describe,it,expect,vi} from 'vitest'
import {codexOperation,nativeExpression,selectCodexTarget} from '../src/native'
import {workflowTurnEvidence} from '../src/ownership'
afterEach(()=>vi.unstubAllGlobals())
describe('Codex renderer native API mapping',()=>{
 it('discovers the manager held by a desktop signal client ref',async()=>{
  const manager={getHostId:()=> 'local',getConversation:vi.fn(),startTurn:vi.fn(),interruptConversation:vi.fn()}
  const client={node:{familyBindings:new Map([['managers',new Map([['local',{value:{atom:{},store:{},get:()=>manager}}]])]])}}
  vi.stubGlobal('__lazymindCodexManager',undefined)
  vi.stubGlobal('__codexRoot',{_internalRoot:{current:{memoizedState:{memoizedState:{current:client}}}}})
  vi.stubGlobal('document',{querySelectorAll:()=>[]})
  vi.stubGlobal('Node',class {})
  expect(await codexOperation({kind:'probe'})).toEqual({ready:true})
  expect(manager.startTurn).not.toHaveBeenCalled()
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
