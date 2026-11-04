import { codexTargets, windowCdp } from './windows'
import { workflowTurnEvidence } from './ownership'
import { sessionExpression } from './presentation'
export { selectCodexTarget } from './windows'
export interface NativeTurn { id: string; status: string; startedAt?: number; items: any[] }
export interface NativeThread { id: string; turns: NativeTurn[]; active: boolean; owner?: boolean; ready?: boolean; ownerScope?: 'host' | 'window' }

/** Runs inside the existing desktop renderer, never in a new App Server. */
export async function codexOperation(input: any, readEvidence = workflowTurnEvidence): Promise<any> {
  const host = globalThis as any
  // Discovery must not invoke arbitrary accessors or derived signal reads in
  // the application's renderer. Those reads can subscribe, compute and grow
  // the very Maps being traversed.
  const data = (value: any, key: string): any => {
    for (let depth = 0; value && depth < 6; depth++, value = Object.getPrototypeOf(value)) {
      const descriptor = Object.getOwnPropertyDescriptor(value, key)
      if (descriptor) return 'value' in descriptor ? descriptor.value : undefined
    }
  }
  const capable = (value: any) => { try { return value && typeof value === 'object'
    && ['getHostId', 'getConversation', 'startTurn', 'interruptConversation'].every(key => typeof data(value, key) === 'function')
    && data(value, 'getHostId').call(value) === 'local' } catch { return false } }
  // Recent desktops keep the manager outside the renderer. The existing host
  // connection holds its RPC client; the separate config-only RPC is not a
  // conversation manager and must not be admitted by its hostId alone.
  const rpcManager = (state: any) => {
    const client = data(state, 'client'), rpc = data(client, 'rpc')
    if (typeof data(state, 'status') !== 'string' || data(client, 'hostId') !== 'local' || typeof rpc !== 'function') return null
    return {
      __lazymindRpc: rpc,
      getHostId: () => 'local',
      getConversation: (id: string) => rpc.getConversation(id),
      getStreamRole: (id: string) => rpc.getStreamRole(id),
      startTurn: (id: string, request: any) => rpc.startTurn(id, request),
      interruptConversation: (id: string, reason: string, turn: string) => rpc.interruptConversation(id, reason, turn),
    }
  }
  const dispose = (value: any) => { if (typeof value?.[Symbol.dispose] === 'function') value[Symbol.dispose]() }
  let manager = host.__lazymindCodexManager
  if (!capable(manager)) {
    manager = null
    const now = performance.now()
    const previous = host.__lazymindCodexDiscovery
    if (previous?.version === 3 && now < previous.retryAt) {
      if (input.kind === 'probe') return { ready: false }
      throw new Error('Codex native conversation interface is unavailable')
    }
    host.__lazymindCodexDiscovery = { version: 3, retryAt: now + 5000 }
    // A short renderer budget is independent of the CDP/Node timeout: closing
    // a timed-out socket cannot interrupt JavaScript already running here.
    const deadline = now + 12
    let remaining = 4000
    const available = () => remaining-- > 0 && performance.now() < deadline
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
        if (!value || typeof value !== 'object' || values.has(value) || !available()) return null
        values.add(value)
        if (capable(value)) return value
        // Current desktop stores managers in existing signal cells, behind the
        // React hook's client ref. WeakMap internals are not enumerable.
        const bindings = data(data(value, 'node'), 'familyBindings')
        if (bindings instanceof Map) {
          let count = 0
          const size = Math.min(bindings.size, 256)
          for (const [definition, family] of bindings) {
            if (count++ >= size || !available()) break
            if (data(definition, 'kind') !== 'signal-family') continue
            if (!(family instanceof Map)) continue
            // The native manager registry is a primitive signal keyed by host.
            // Read only its existing local slot, never every computed cell.
            const cell = data(family.get('local'), 'value')
            const atom = data(cell, 'atom'), store = data(cell, 'store')
            if (!atom || !Object.hasOwn(atom, 'init') || typeof data(cell, 'set') !== 'function'
              || typeof data(store, 'get') !== 'function') continue
            const current = data(store, 'get').call(store, atom)
            if (capable(current)) return current
            const remote = rpcManager(current)
            if (remote) return remote
          }
          return null
        }
        if (!depth || value instanceof Node || data(value, '$$typeof') || ('tag' in value && 'memoizedState' in value)) return null
        let count = 0
        if (value instanceof Map) {
          const size = Math.min(value.size, 256)
          for (const entry of value.values()) {
            if (count++ >= size || !available()) break
            const found = find(entry, depth - 1); if (found) return found
          }
        } else for (const key in value) {
          if (count++ >= 256 || !available()) break
          const descriptor = Object.getOwnPropertyDescriptor(value, key)
          if (!descriptor || !('value' in descriptor)) continue
          const found = find(descriptor.value, depth - 1); if (found) return found
        }
      } catch { /* Ignore inaccessible browser objects; inspect other hooks. */ }
      return null
    }
    while (roots.length && !manager && available()) {
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
    if (!capable(manager)) {
      if (input.kind === 'probe') return { ready: false }
      throw new Error('Codex native conversation interface is unavailable')
    }
    host.__lazymindCodexManager = manager
  }
  const rpc = data(manager, '__lazymindRpc')
  if (rpc) {
    try {
      if (await rpc.getHostId() !== 'local') throw new Error('Unexpected Codex RPC host')
    } catch {
      delete host.__lazymindCodexManager
      if (input.kind === 'probe') return { ready: false }
      throw new Error('Codex native conversation connection is unavailable')
    }
  }
  if (input.kind === 'probe') return { ready: true }
  // The RPC only fences interruption by turn ID. Steering can change Workflow
  // ownership within that same turn after any asynchronous snapshot. Until the
  // host offers conditional interruption, never send Stop using stale evidence.
  if (rpc && input.kind === 'interrupt') return {
    rejected: 'This Codex version cannot safely interrupt a Workflow turn. Stop the current response in Codex.',
  }
  const capture = (state: any, stream: any, streaming: boolean) => {
    if (!state || state.id !== input.threadId) return { missing: true as const }
    const history = state?.turnHistory
    const records = history?.kind === 'canonical'
      ? history.history.islands.flatMap((island: any) => island.entries.map((entry: any) => history.history.entitiesByKey[entry.value])).filter(Boolean)
      : state?.turns ?? []
    const turns = records.map((turn: any) => ({ id: turn.turnId ?? turn.id, status: turn.status,
      startedAt: turn.turnStartedAtMs ?? turn.startedAtMs, items: turn.items ?? [] }))
    const role = stream?.role
    const ready = state.resumeState === 'resumed' && ['owner','follower'].includes(role) && streaming
    return { id: state.id, turns, ready, owner: role === 'owner', ownerScope: rpc ? 'host' as const : 'window' as const,
      active: state.threadRuntimeStatus?.type === 'active' || turns.some((turn: any) => turn.status === 'inProgress') }
  }
  const localSnapshot = () => capture(manager.getConversation(input.threadId), manager.getStreamRole?.(input.threadId), manager.isConversationStreaming?.(input.threadId) === true)
  const snapshot = async () => {
    if (!rpc) return localSnapshot()
    const state = await manager.getConversation(input.threadId)
    let stream: any
    try {
      stream = await manager.getStreamRole(input.threadId)
      // The RPC exposes getStreamRole, not isConversationStreaming. Both native
      // methods use the same streamRoles map, so a live role proves membership.
      return capture(state, stream, stream != null)
    } finally { dispose(stream); dispose(state) }
  }
  const current = await snapshot()
  if (current.missing) return current
  if (input.kind === 'read') return current
  if (input.kind === 'start') {
    if (!current.ready) return { rejected: 'Codex conversation is still restoring; no message was sent', retryable: true }
    if (current.active) return { rejected: 'Codex chat is busy; no message was sent' }
    const result = await manager.startTurn(input.threadId, {
      request: { threadId: input.threadId, clientUserMessageId: input.clientId,
        input: [{ type: 'text', text: input.message, text_elements: [] }] },
      context: { inheritThreadSettings: true, useUserMessageAdmission: false, attachments: [], commentAttachments: [] },
    })
    try {
      const turn = result?.turn ?? result?.result?.turn ?? result?.result?.result?.turn
      if (turn?.id || result?.turnId) return { accepted: true, turnId: turn?.id ?? result.turnId }
    } finally { if (rpc) dispose(result) }
    const after = await snapshot()
    if (after.missing) throw new Error('Native turn receipt is uncertain')
    const matches = after.turns.filter((turn: any) => turn.items.some((item: any) => item.type === 'userMessage' && item.clientId === input.clientId))
    if (matches.length !== 1) throw new Error('Native turn receipt is uncertain')
    return { accepted: true, turnId: matches[0].id }
  }
  if (input.kind === 'interrupt') {
    const latest = localSnapshot()
    if (latest.missing) return { interruptedTurnId: null }
    const target = latest.turns.find((turn: any) => turn.id === input.turnId)
    if (!target || target.status !== 'inProgress') return { interruptedTurnId: null }
    // Only the direct interface can recheck live ownership and invoke the
    // manager in the same renderer task, with no intervening RPC round trip.
    if (!latest.owner
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
    // RPC clients in main/detached windows observe the same local host manager.
    // Their repeated owner reports are not independent window owners. Preserve
    // the conflict check for legacy managers and mixed interface generations.
    if(owners.length>1&&!owners.every(match=>match.state.ownerScope==='host'))throw Error('Codex reports conflicting owners for this chat')
    const selected=(owners.length?owners:matches).sort((a,b)=>a.id.localeCompare(b.id))[0]
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
