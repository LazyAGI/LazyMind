import { describe, it, expect, vi } from 'vitest'
import { mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { CodexAdapter } from '../src/adapter'
import { NativeReceipts } from '../src/receipts'
import { selectCodexTarget } from '../src/native'
import { AdmissionRejected } from '../../workflow-agent-core/src/adapter'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
const thread = '01a12f01-e372-7ef3-aaae-4a0a295d6700'
const claim = (kind = 'continue') => ({ action: { id: 'action-1', session_id: 'run-1', native_session_id: thread,
  kind, status: 'dispatching', created_at: '2026-10-26T00:00:10Z' }, control: { session_id: 'run-1', continuation: kind === 'cancel' ? 'stopped' : 'continue',
  binding: { bound: true, driver_session_id: thread, provider: 'codex' } } }) as any
async function fixture() {
 const native = { read: vi.fn().mockResolvedValue({ id: thread, active: false, turns: [] }),
   start: vi.fn().mockResolvedValue({ accepted: true, turnId: 'turn-new' }), interrupt: vi.fn().mockResolvedValue({ interruptedTurnId: 'turn-owned' }) }
 const receipts = new NativeReceipts(await mkdtemp(join(tmpdir(), 'codex-cdp-receipts-')))
 const bridge = { action: vi.fn() } as any
 return { native, receipts, bridge, adapter: new CodexAdapter(native, receipts, bridge, new AbortController().signal) }
}
describe('CDP Workflow admission and interruption', () => {
 it('sends one notification to the bound chat and saves its actual turn receipt', async () => {
  const {adapter,native,receipts}=await fixture();adapter.host.cacheClaim(claim())
  await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).resolves.toBe(0)
  expect(native.start).toHaveBeenCalledWith(thread,expect.any(String),'[LazyMind action_id=action-1]\ncontinue',expect.any(AbortSignal))
  expect(await receipts.read('action-1')).toMatchObject({status:'accepted',turnId:'turn-new',threadId:thread})
 })
 it('does not send when the target becomes busy', async()=>{
  const {adapter,native}=await fixture();adapter.host.cacheClaim(claim());native.read.mockResolvedValue({id:thread,active:true,turns:[]})
  await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).rejects.toThrow('busy')
  expect(native.start).not.toHaveBeenCalled()
 })
 it('does not resend a write with an uncertain outcome',async()=>{
  const {adapter,native}=await fixture();native.start.mockRejectedValue(new Error('CDP disconnected'))
  adapter.host.cacheClaim(claim());await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).rejects.toThrow()
  adapter.host.cacheClaim(claim());await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).rejects.toThrow('already attempted')
  expect(native.start).toHaveBeenCalledTimes(1)
 })
 it('settles a temporarily unready notification after bounded retries with the same identity',async()=>{
  const {adapter,native,bridge,receipts}=await fixture(), action=claim()
  bridge.claim=vi.fn().mockResolvedValue({...action,dispatch_token:'lease'})
  bridge.action.mockResolvedValue(action);bridge.settle=vi.fn().mockResolvedValue(undefined)
  native.start.mockResolvedValueOnce({rejected:'still restoring',retryable:true})
    .mockResolvedValueOnce({rejected:'still restoring',retryable:true})
  const signal=new AbortController().signal
  await createDispatcher(adapter,adapter.host,bridge,'instance',signal).deliver({...action.action,status:'pending'})
  expect(native.start).toHaveBeenCalledTimes(3)
  expect(native.start.mock.calls[1]).toEqual(native.start.mock.calls[0])
  expect(native.start.mock.calls[2]).toEqual(native.start.mock.calls[0])
  expect(bridge.settle).toHaveBeenCalledWith('action-1','instance','lease','accepted',0,'',signal)
  expect(await receipts.read('action-1')).toMatchObject({status:'accepted',turnId:'turn-new'})
 })
 it.each([true,false])('bounds retries and records definite rejection (retryable=%s)',async(retryable)=>{
  const {adapter,native,bridge,receipts}=await fixture()
  adapter.host.cacheClaim(claim());bridge.action.mockResolvedValue(claim())
  native.start.mockResolvedValue({rejected:'not ready',retryable})
  await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).rejects.toBeInstanceOf(AdmissionRejected)
  expect(native.start).toHaveBeenCalledTimes(retryable?3:1)
  expect(await receipts.read('action-1')).toMatchObject({status:'rejected'})
 })
 it('does not retry an uncertain send after a definite rejection',async()=>{
  const {adapter,native,bridge,receipts}=await fixture()
  adapter.host.cacheClaim(claim());bridge.action.mockResolvedValue(claim())
  native.start.mockResolvedValueOnce({rejected:'still restoring',retryable:true}).mockRejectedValueOnce(Error('CDP disconnected'))
  await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).rejects.toThrow('CDP disconnected')
  expect(native.start).toHaveBeenCalledTimes(2)
  expect(await receipts.read('action-1')).toMatchObject({status:'unknown'})
 })
 it('does not resend when cancellation occurs during the retry wait',async()=>{
  const {adapter,native,bridge}=await fixture(), controller=new AbortController()
  adapter.host.cacheClaim(claim())
  native.start.mockImplementationOnce(async()=>{controller.abort();return {rejected:'still restoring',retryable:true}})
  await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},controller.signal)).rejects.toThrow()
  expect(native.start).toHaveBeenCalledOnce();expect(bridge.action).not.toHaveBeenCalled()
 })
 it('revalidates a consumed action before retrying the native send',async()=>{
  const {adapter,native,bridge}=await fixture(), current=claim()
  adapter.host.cacheClaim(claim());current.action.consumed_at='2026-10-26T00:00:11Z'
  bridge.action.mockResolvedValue(current)
  native.start.mockResolvedValueOnce({rejected:'still restoring',retryable:true})
  await expect(adapter.prompt(thread,{actionId:'action-1',message:'continue'},new AbortController().signal)).rejects.toBeInstanceOf(AdmissionRejected)
  expect(native.start).toHaveBeenCalledOnce()
 })
 it('interrupts the exact Workflow-owned turn',async()=>{
  const {adapter,native,bridge}=await fixture();const action=claim('cancel');bridge.action.mockResolvedValue(action)
  native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'turn-owned',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:00Z'),items:[{
   type:'mcpToolCall',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-1'}}
  }]}]})
  await adapter.host.cancelSession(thread,action)
  expect(native.interrupt).toHaveBeenCalledWith(thread,'turn-owned',JSON.stringify({runId:'run-1',inputs:[]}),expect.any(AbortSignal))
 })
 it('refuses Stop against a new user turn or work started after Stop',async()=>{
  const {adapter,native}=await fixture()
  for(const items of [[{type:'userMessage',content:[{type:'text',text:'another task'}]}],[{type:'mcpToolCall',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-1'}}}]]) {
   native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'new',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:20Z'),items}]})
   await expect(adapter.host.cancelSession(thread,claim('cancel'))).rejects.toThrow('not the target')
  }
  expect(native.interrupt).not.toHaveBeenCalled()
 })
 it.each([
  {type:'userMessage',id:'new-input',content:[{type:'text',text:'work on another task'}]},
  {type:'steeringUserMessage',id:'steer',status:'pending',targetTurnId:'turn-owned'},
  {type:'steeringUserMessage',id:'steer',status:'accepted',targetTurnId:'turn-owned',serverUserMessageId:'new-input'},
  {type:'steered',id:'new-input'},
 ])('invalidates old ownership and notification receipts for $type/$status',async(input)=>{
  for(const withToolReceipt of [false,true]) {
   const {adapter,native,receipts}=await fixture()
   await receipts.write({actionId:'continue-1',runId:'run-1',threadId:thread,kind:'continue',clientId:'client-1',turnId:'turn-owned',status:'accepted'})
   native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'turn-owned',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:00Z'),items:[
    {type:'userMessage',id:'original',clientId:'client-1',content:[{type:'text',text:'[LazyMind action_id=continue-1]\ncontinue'}]},
    ...(withToolReceipt?[{type:'mcpToolCall',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-1'}}}]:[]),
    input,
   ]}]})
   await expect(adapter.host.cancelSession(thread,claim('cancel'))).rejects.toBeInstanceOf(AdmissionRejected)
   expect(native.interrupt).not.toHaveBeenCalled()
  }
 })
 it('can stop an unsteered continuation using its durable notification receipt',async()=>{
  const {adapter,native,bridge,receipts}=await fixture();const action=claim('cancel');bridge.action.mockResolvedValue(action)
  await receipts.write({actionId:'continue-1',runId:'run-1',threadId:thread,kind:'continue',clientId:'client-1',turnId:'turn-owned',status:'accepted'})
  native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'turn-owned',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:00Z'),items:[
   {type:'userMessage',id:'original',clientId:'client-1',content:[{type:'text',text:'[LazyMind action_id=continue-1]\ncontinue'}]},
  ]}]})
  await adapter.host.cancelSession(thread,action)
  expect(native.interrupt).toHaveBeenCalledOnce()
 })
 it.each([
  {tool:'workflow.step.begin',run:'run-1',allowed:true},
  {tool:'workflow.step.begin',run:'run-2',allowed:false},
  {tool:'workflow.state',run:'run-1',allowed:false},
 ])('requires a fresh acquisition for the same run after steering: $tool/$run',async({tool,run,allowed})=>{
  const {adapter,native,bridge}=await fixture();const action=claim('cancel');bridge.action.mockResolvedValue(action)
  native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'turn-owned',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:00Z'),items:[
   {type:'mcpToolCall',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-1'}}},
   {type:'steeringUserMessage',id:'steer',status:'accepted',targetTurnId:'turn-owned',serverUserMessageId:'new-input'},
   {type:'steered',id:'new-input'},
   {type:'mcpToolCall',server:'lazymind',tool,status:'completed',arguments:{session_id:run},result:{structuredContent:{session_id:run,execution:{execution_id:'execution-1'}}}},
   {type:'commandExecution',id:'ordinary-work',status:'inProgress'},
  ]}]})
  if(allowed) {await adapter.host.cancelSession(thread,action);expect(native.interrupt).toHaveBeenCalledOnce()}
  else {await expect(adapter.host.cancelSession(thread,action)).rejects.toBeInstanceOf(AdmissionRejected);expect(native.interrupt).not.toHaveBeenCalled()}
 })
 it('does not regain ownership when an earlier invocation completes after steering',async()=>{
  const {adapter,native}=await fixture()
  const tool:any={type:'mcpToolCall',server:'lazymind',tool:'workflow.step.begin',status:'inProgress',arguments:{session_id:'run-1'}}
  const items=[tool,{type:'steeringUserMessage',id:'steer',status:'accepted',targetTurnId:'turn-owned'}]
  tool.status='completed';tool.result={structuredContent:{session_id:'run-1',execution:{execution_id:'execution-1'}}}
  native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'turn-owned',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:00Z'),items}]})
  await expect(adapter.host.cancelSession(thread,claim('cancel'))).rejects.toBeInstanceOf(AdmissionRejected)
  expect(native.interrupt).not.toHaveBeenCalled()
 })
 it('reports a renderer ownership rejection as definitely not interrupted',async()=>{
  const {adapter,native,bridge}=await fixture();const action=claim('cancel');bridge.action.mockResolvedValue(action)
  native.read.mockResolvedValue({id:thread,active:true,turns:[{id:'turn-owned',status:'inProgress',startedAt:Date.parse('2026-10-26T00:00:00Z'),items:[
   {type:'mcpToolCall',server:'lazymind',tool:'workflow.start',status:'completed',result:{structuredContent:{session_id:'run-1'}}},
  ]}]})
  native.interrupt.mockResolvedValue({rejected:'Codex execution ownership changed'} as any)
  await expect(adapter.host.cancelSession(thread,action)).rejects.toBeInstanceOf(AdmissionRejected)
 })
 it('selects the Codex renderer and rejects ambiguity',()=>{
  expect(selectCodexTarget([{type:'page',url:'app://-/index.html'},{type:'page',url:'https://example.test'}]).url).toBe('app://-/index.html')
  expect(()=>selectCodexTarget([{type:'page',url:'app://-/index.html'},{type:'page',url:'app://-/index.html'}])).toThrow('one Codex')
 })
})
