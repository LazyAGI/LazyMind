import { codexTargets, windowCdp } from './windows'
import { workflowTurnEvidence } from './ownership'
import { sessionExpression } from './presentation'
export { selectCodexTarget } from './windows'
export interface NativeTurn { id: string; status: string; startedAt?: number; items: any[] }
export interface NativeThread { id: string; turns: NativeTurn[]; active: boolean; owner?: boolean; ready?: boolean }

/** Runs inside the existing desktop renderer, never in a new App Server. */
export async function codexOperation(input: any, readEvidence = workflowTurnEvidence): Promise<any> {
  const host = globalThis as any
  const capable = (value: any) => { try { return value && typeof value === 'object'
    && ['getHostId', 'getConversation', 'startTurn', 'interruptConversation'].every(key => typeof value[key] === 'function')
    && value.getHostId() === 'local' } catch { return false } }
  let manager = host.__lazymindCodexManager
  if (!capable(manager)) {
    const roots: any[] = []
    if (host.__codexRoot?._internalRoot?.current) roots.push(host.__codexRoot._internalRoot.current)
    for (const element of Array.from(document.querySelectorAll('#root, #app, body > div'))) {
      for (const key of Object.keys(element)) {
        if (key.startsWith('__reactContainer$') || key.startsWith('__reactFiber$')) roots.push((element as any)[key])
      }
    }
    const fibers = new Set<any>(), values = new Set<any>()
    const find = (value: any, depth: number): any => {
      try {
        if (!value || typeof value !== 'object' || values.has(value) || values.size > 20000) return null
        values.add(value)
        if (capable(value)) return value
        // Current desktop stores managers in existing signal cells, behind the
        // React hook's client ref. WeakMap internals are not enumerable.
        const bindings = value.node?.familyBindings
        if (bindings instanceof Map) {
          for (const family of bindings.values()) {
            if (!(family instanceof Map)) continue
            for (const binding of family.values()) {
              const cell = binding?.value
              if (cell?.atom && cell.store && typeof cell.get === 'function') {
                try { const current = cell.get(); if (capable(current)) return current } catch {}
              }
            }
          }
          return null
        }
        if (!depth || value instanceof Node || value.$$typeof || ('tag' in value && 'memoizedState' in value)) return null
        const entries = value instanceof Map ? Array.from(value.values()) : Object.values(Object.getOwnPropertyDescriptors(value)).flatMap(d => 'value' in d ? [d.value] : [])
        for (const entry of entries) { const found = find(entry, depth - 1); if (found) return found }
      } catch { /* Ignore inaccessible browser objects; inspect other hooks. */ }
      return null
    }
    while (roots.length && fibers.size < 30000 && !manager) {
      const fiber = roots.pop()
      if (!fiber || fibers.has(fiber)) continue
      fibers.add(fiber)
      if (fiber.sibling) roots.push(fiber.sibling)
      if (fiber.child) roots.push(fiber.child)
      let hook = fiber.memoizedState
      for (let i = 0; hook && i < 160 && !manager; i++, hook = hook.next) manager = find(hook.memoizedState, 2)
      if (!manager) manager = find(fiber.memoizedProps?.value, 2)
      let dependency = fiber.dependencies?.firstContext
      for (let i = 0; dependency && i < 30 && !manager; i++, dependency = dependency.next) manager = find(dependency.memoizedValue, 2)
    }
    if (!capable(manager)) throw new Error('Codex native conversation interface is unavailable')
    host.__lazymindCodexManager = manager
  }
  if (input.kind === 'probe') return { ready: true }
  const raw = manager.getConversation(input.threadId)
  if (!raw || raw.id !== input.threadId) return { missing: true }
  const snapshot = () => {
    const state = manager.getConversation(input.threadId)
    const history = state?.turnHistory
    const records = history?.kind === 'canonical'
      ? history.history.islands.flatMap((island: any) => island.entries.map((entry: any) => history.history.entitiesByKey[entry.value])).filter(Boolean)
      : state?.turns ?? []
    const turns = records.map((turn: any) => ({ id: turn.turnId ?? turn.id, status: turn.status,
      startedAt: turn.turnStartedAtMs ?? turn.startedAtMs, items: turn.items ?? [] }))
    const role = manager.getStreamRole?.(input.threadId)?.role
    const ready = state.resumeState === 'resumed' && ['owner','follower'].includes(role) && manager.isConversationStreaming?.(input.threadId) === true
    return { id: state.id, turns, ready, owner: role === 'owner', active: state.threadRuntimeStatus?.type === 'active' || turns.some((turn: any) => turn.status === 'inProgress') }
  }
  if (input.kind === 'read') return snapshot()
  if (input.kind === 'start') {
    if (!snapshot().ready) return { rejected: 'Codex conversation is still restoring; no message was sent', retryable: true }
    if (snapshot().active) return { rejected: 'Codex chat is busy; no message was sent' }
    const result = await manager.startTurn(input.threadId, {
      request: { threadId: input.threadId, clientUserMessageId: input.clientId,
        input: [{ type: 'text', text: input.message, text_elements: [] }] },
      context: { inheritThreadSettings: true, useUserMessageAdmission: false, attachments: [], commentAttachments: [] },
    })
    const turn = result?.turn ?? result?.result?.turn ?? result?.result?.result?.turn
    if (turn?.id || result?.turnId) return { accepted: true, turnId: turn?.id ?? result.turnId }
    const after = snapshot()
    const matches = after.turns.filter((turn: any) => turn.items.some((item: any) => item.type === 'userMessage' && item.clientId === input.clientId))
    if (matches.length !== 1) throw new Error('Native turn receipt is uncertain')
    return { accepted: true, turnId: matches[0].id }
  }
  if (input.kind === 'interrupt') {
    const target = snapshot().turns.find((turn: any) => turn.id === input.turnId)
    if (!target || target.status !== 'inProgress') return { interruptedTurnId: null }
    // Recheck on the owner immediately before admission, after all Bridge/disk/CDP
    // awaits. A follower's cached history cannot fence new input in another window.
    if (manager.getStreamRole?.(input.threadId)?.role !== 'owner'
      || JSON.stringify(readEvidence(target)) !== input.expectedOwnership) {
      return { rejected: 'Codex execution ownership changed; no interruption was sent' }
    }
    const interrupted = await manager.interruptConversation(input.threadId, 'user-stop', input.turnId)
    return { interruptedTurnId: interrupted ?? null }
  }
  throw new Error('Unknown Codex operation')
}

export function nativeExpression(input: object) {
  return `(input => (${codexOperation.toString()})(input, ${workflowTurnEvidence.toString()}))(${JSON.stringify(input)})`
}

export class CodexNative {
  constructor(private port: number) {}
  private evaluate(id:string,input:object,signal:AbortSignal):Promise<any> {
    return windowCdp(this.port,id).evaluate(nativeExpression(input),signal,(input as any).kind==='start'?30000:8000)
  }
  async probe(signal:AbortSignal) {
    const windows=await codexTargets(this.port,signal)
    const results=await Promise.allSettled(windows.map(window=>this.evaluate(window.id,{kind:'probe'},signal)))
    return {ready:results.some(result=>result.status==='fulfilled'&&result.value?.ready===true),window_count:windows.length}
  }
  private async locate(threadId:string,signal:AbortSignal) {
    const windows=await codexTargets(this.port,signal)
    const results=await Promise.allSettled(windows.map(async window=>({id:window.id,state:await this.evaluate(window.id,{kind:'read',threadId},signal) as NativeThread&{missing?:boolean}})))
    const matches=results.flatMap(result=>result.status==='fulfilled'&&!result.value.state?.missing&&result.value.state?.id===threadId?[result.value]:[])
    const owners=matches.filter(match=>match.state.owner)
    if(owners.length>1)throw Error('Codex reports conflicting owners for this chat')
    const selected=owners[0]??matches.sort((a,b)=>a.id.localeCompare(b.id))[0]
    if(!selected)throw Error('Open the original Codex chat before continuing')
    return selected
  }
  async read(threadId:string,signal:AbortSignal):Promise<NativeThread> {return (await this.locate(threadId,signal)).state}
  async visible(threadId:string,signal:AbortSignal):Promise<boolean> {
    const windows=await codexTargets(this.port,signal)
    const results=await Promise.allSettled(windows.map(window=>windowCdp(this.port,window.id).evaluate<{session_id?:string}>(sessionExpression(),signal)))
    return results.some(result=>result.status==='fulfilled'&&result.value?.session_id===threadId)
  }
  async presented(threadId:string,signal:AbortSignal):Promise<boolean> {
    const windows=await codexTargets(this.port,signal)
    const expression=`(async()=>{const panel=window.__lazymindCodexWorkflowTail;if(panel?.session!==${JSON.stringify(threadId)})return false;const state=await panel.probeStatus?.();return window.__lazymindCodexWorkflowTail===panel&&panel.session===${JSON.stringify(threadId)}&&state?.session_id===${JSON.stringify(threadId)}&&state?.status==='mounted'&&state?.page_status==='ready'})()`
    const results=await Promise.allSettled(windows.map(window=>windowCdp(this.port,window.id).evaluate<boolean>(expression,signal)))
    return results.some(result=>result.status==='fulfilled'&&result.value===true)
  }
  private async mutate(input:{kind:string;threadId:string;clientId?:string;message?:string;turnId?:string;expectedOwnership?:string},signal:AbortSignal) {
    const target=await this.locate(input.threadId,signal)
    // Never retry a mutation in another window after an uncertain response.
    return this.evaluate(target.id,input,signal)
  }
  start(threadId:string,clientId:string,message:string,signal:AbortSignal) {return this.mutate({kind:'start',threadId,clientId,message},signal)}
  interrupt(threadId:string,turnId:string,expectedOwnership:string,signal:AbortSignal) {return this.mutate({kind:'interrupt',threadId,turnId,expectedOwnership},signal)}
}
