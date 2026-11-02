import { describe, expect, it, vi } from 'vitest'
import { deliverRecovery, recoveryMessage, type RecoveryIntent } from '../src/recovery-delivery'

const request='a'.repeat(32), thread='11111111-2222-3333-4444-555555555555'
const intent:RecoveryIntent={id:'b'.repeat(32),thread_id:thread,tool:'workflow.start',arguments:{workflow_id:'ppt',request_context:'原任务',input_bindings:{source:{resource_id:'source-1'}},idempotency_key:'stable-start-123456789',driver_session_id:thread}}
function fixture(){
  let stored:any
  const receipts={read:vi.fn(async()=>stored),write:vi.fn(async(value:any)=>{stored={...value}})}
  const native={visible:vi.fn(async()=>true),presented:vi.fn(async()=>true),read:vi.fn(async()=>({id:thread,active:false,ready:true,turns:[] as any[]})),start:vi.fn(async(_thread:string,_client:string,_message:string,_signal:AbortSignal)=>({accepted:true,turnId:'new-turn'}))}
  return {native,receipts,setReceipt:(value:any)=>{stored=value},getReceipt:()=>stored}
}
describe('recovery handoff in the original conversation',()=>{
  it('waits for actual navigation, then sends the original structured request with the same retry key',async()=>{
    const f=fixture(); f.native.visible.mockResolvedValueOnce(false)
    const pause=vi.fn(async()=>{})
    expect(await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000),pause)).toEqual({accepted:true})
    expect(pause).toHaveBeenCalledOnce()
    expect(f.native.start).toHaveBeenCalledOnce()
    expect(f.native.start.mock.calls[0][0]).toBe(thread)
    const message=(f.native.start.mock.calls[0] as any[])[2]
    expect(message).toContain(JSON.stringify({tool:intent.tool,arguments:intent.arguments}))
    expect(message).toContain('not approval of a Workflow review')
    expect(f.getReceipt().status).toBe('accepted')
    expect(f.receipts.write.mock.invocationCallOrder[0]).toBeLessThan(f.native.start.mock.invocationCallOrder[0])
  })
  it('waits for an idle conversation without interrupting the user',async()=>{
    const f=fixture(); f.native.read.mockResolvedValueOnce({id:thread,active:true,ready:true,turns:[]})
    const pause=vi.fn(async()=>{})
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000),pause)
    expect(pause).toHaveBeenCalledOnce(); expect(f.native.start).toHaveBeenCalledOnce()
  })
  it('a repeated recovery click uses the accepted receipt and never sends again',async()=>{
    const f=fixture()
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000))
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000))
    expect(f.native.start).toHaveBeenCalledOnce()
  })
  it('reconciles a send whose native reply was lost',async()=>{
    const f=fixture()
    f.setReceipt({actionId:`recovery:${request}:${intent.id}`,runId:request,threadId:thread,kind:'continue',clientId:'receipt-client',status:'sending'})
    f.native.read.mockResolvedValue({id:thread,active:true,ready:true,turns:[{id:'accepted-turn',status:'inProgress',items:[{type:'userMessage',clientId:'receipt-client'}]}]})
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000))
    expect(f.native.start).not.toHaveBeenCalled(); expect(f.getReceipt()).toMatchObject({status:'accepted',turnId:'accepted-turn'})
  })
  it('does not resend when an uncertain receipt cannot be reconciled',async()=>{
    const f=fixture()
    f.setReceipt({actionId:`recovery:${request}:${intent.id}`,runId:request,threadId:thread,kind:'continue',clientId:'receipt-client',status:'unknown'})
    await expect(deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000))).rejects.toThrow('uncertain')
    expect(f.native.start).not.toHaveBeenCalled()
  })
  it('a revoked account/request cannot admit a message',async()=>{
    const f=fixture()
    await expect(deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000),undefined,async()=>{throw Error('revoked')})).rejects.toThrow('revoked')
    expect(f.native.start).not.toHaveBeenCalled(); expect(f.receipts.write).not.toHaveBeenCalled()
  })
  it('an existing pending control action only restores the chat; its dispatcher remains the sole sender',async()=>{
    const f=fixture()
    await deliverRecovery(request,{thread_id:thread},f.native,f.receipts,AbortSignal.timeout(1000))
    expect(f.native.start).not.toHaveBeenCalled(); expect(f.receipts.write).not.toHaveBeenCalled()
  })
  it('reconciles a broken native reply on retry instead of starting a second turn',async()=>{
    const f=fixture();f.native.start.mockRejectedValueOnce(Error('socket closed'))
    await expect(deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000))).rejects.toThrow('socket closed')
    expect(f.getReceipt().status).toBe('unknown')
    f.native.read.mockResolvedValue({id:thread,active:false,ready:true,turns:[{id:'delivered',status:'completed',items:[{type:'userMessage',clientId:f.getReceipt().clientId}]}]})
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000))
    expect(f.native.start).toHaveBeenCalledOnce();expect(f.getReceipt().turnId).toBe('delivered')
  })
  it('rejects unknown tools and missing idempotency rather than reconstructing a vague Continue',()=>{
    expect(()=>recoveryMessage(request,{...intent,tool:'shell.exec'})).toThrow('Invalid')
    expect(()=>recoveryMessage(request,{...intent,arguments:{...intent.arguments,idempotency_key:''}})).toThrow('stable')
    const message=recoveryMessage(request,{...intent,tool:'workflow.step.begin',arguments:{session_id:'existing-run',step_id:'outline',command_id:'same-command'}})
    expect(message).toContain('Read workflow.state'); expect(message).toContain('SAME command_id')
  })
  it('waits for native resume after the view is visible and idle',async()=>{
    const f=fixture();f.native.read.mockResolvedValueOnce({id:thread,active:false,ready:false,turns:[]})
    const pause=vi.fn(async()=>{expect(f.native.start).not.toHaveBeenCalled()})
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000),pause)
    expect(pause).toHaveBeenCalledOnce();expect(f.native.start).toHaveBeenCalledOnce()
  })
  it('viewing completed results waits for the Panel without reading or sending an execution turn',async()=>{
    const f=fixture();f.native.presented.mockResolvedValueOnce(false)
    f.native.visible.mockResolvedValue(false)
    const pause=vi.fn(async()=>{})
    await deliverRecovery(request,{thread_id:thread},f.native,f.receipts,AbortSignal.timeout(1000),pause)
    expect(pause).toHaveBeenCalledOnce();expect(f.native.read).not.toHaveBeenCalled();expect(f.native.start).not.toHaveBeenCalled()
    expect(f.native.visible).not.toHaveBeenCalled()
  })
  it('automatically waits again when readiness changes before native admission',async()=>{
    const f=fixture();f.native.start.mockResolvedValueOnce({rejected:'still restoring',retryable:true} as any)
    await deliverRecovery(request,intent,f.native,f.receipts,AbortSignal.timeout(1000),async()=>{})
    expect(f.native.start).toHaveBeenCalledTimes(2);expect(f.getReceipt().status).toBe('accepted')
  })
})
