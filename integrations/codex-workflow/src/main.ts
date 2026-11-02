import { readFile, writeFile, rename } from 'node:fs/promises'
import { parseArgs } from 'node:util'
import { randomUUID } from 'node:crypto'
import { join, dirname } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import { HostBridge } from '../../workflow-agent-core/src/transport'
import { dispatcherLock, DispatcherBusy } from '../../workflow-agent-core/node/runtime-lock'
import { CodexAdapter } from './adapter'
import { CodexNative } from './native'
import { NativeReceipts } from './receipts'
import { presentationLoop, type PresentationStatus } from './presentation'
import { cleanupCodexPresentations } from './presentation'
import { connectionRecovery } from './connection-recovery'
import { deliverRecovery, type RecoveryIntent } from './recovery-delivery'
import { finishRecovery } from './recovery-result'

async function main() {
  const { values } = parseArgs({ options: {
    'pairing-file': { type: 'string' }, 'bridge-url': { type: 'string', default: 'http://127.0.0.1:19091' },
    'cdp-port': { type: 'string' }, 'parent-pid': { type: 'string' },
    'cleanup-only': { type: 'boolean', default: false },
    'probe-only': { type: 'boolean', default: false },
    'presentation-probe-only': { type: 'boolean', default: false }, 'thread-id': {type:'string'},
    'recover-intent': { type: 'boolean', default: false },
  } })
  if (values['presentation-probe-only']) {
    const thread=values['thread-id']??''
    if(!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(thread))throw Error('Invalid presentation target')
    return finishRecovery({ready:await new CodexNative(Number(values['cdp-port'])).presented(thread,AbortSignal.timeout(5000))})
  }
  if (values['probe-only']) {
    // A one-shot read-only capability probe. It acquires no dispatcher lock,
    // posts no actions, mounts no panel, and writes no readiness cache.
    console.log(JSON.stringify(await new CodexNative(Number(values['cdp-port'])).probe(AbortSignal.timeout(4000))))
    return
  }
  const path = values['pairing-file']
  if (!path) throw new Error('pairing-file is required')
  const pairing = JSON.parse(await readFile(path, 'utf8'))
  if (pairing.provider !== 'codex' || !pairing.enabled && !values['cleanup-only']) throw new Error('Codex pairing is disabled')
  if (values['recover-intent']) {
    const chunks: Buffer[] = []; let size=0
    for await (const chunk of process.stdin) { size+=chunk.length; if(size>256*1024)throw Error('Recovery operation is too large'); chunks.push(Buffer.from(chunk)) }
    const input=JSON.parse(Buffer.concat(chunks).toString('utf8')) as {request_id:string;intent:RecoveryIntent}
    const signal=AbortSignal.timeout(60_000)
    const authorize=async()=>{
      const response=await fetch(new URL('/v1/codex/recovery?wait=0',values['bridge-url']!),{
        signal:AbortSignal.any([signal,AbortSignal.timeout(5000)]),redirect:'error',
        headers:{Authorization:`Bearer ${pairing.token}`,'X-LazyMind-Connector-Id':pairing.connector_id},
      })
      if(!response.ok)throw Error('Codex recovery authorization is no longer valid')
      const latest=await response.json() as {request?:{id:string;intents?:RecoveryIntent[]}}
      if(latest.request?.id!==input.request_id || !latest.request.intents?.some(intent=>intent.id===input.intent.id&&intent.thread_id===input.intent.thread_id
        && intent.tool===input.intent.tool&&JSON.stringify(intent.arguments)===JSON.stringify(input.intent.arguments)))throw Error('Codex recovery request expired')
    }
    const result=await deliverRecovery(input.request_id,input.intent,new CodexNative(Number(values['cdp-port'])),
      new NativeReceipts(join(dirname(path),pairing.connector_id+'.recovery-receipts')),signal,undefined,authorize,stage=>console.log(JSON.stringify({stage})))
    return finishRecovery(result)
  }
  if (values['cleanup-only']) {
    const deadline = Date.now()+3000
    let release: (() => Promise<void>) | undefined
    while (!release) {
      try {release=await dispatcherLock(path,randomUUID())}
      catch(error) {if(!(error instanceof DispatcherBusy)||Date.now()>=deadline)throw error;await delay(100)}
    }
    try { await cleanupCodexPresentations(Number(values['cdp-port']),AbortSignal.timeout(2000)) }
    finally { await release() }
    return
  }
  const native = new CodexNative(Number(values['cdp-port']))
  const lifetime = new AbortController(), signal = lifetime.signal
  const stop = () => lifetime.abort()
  process.once('SIGINT', stop); process.once('SIGTERM', stop)
  const instance = randomUUID(), unlock = await dispatcherLock(path, instance)
  const bridge = new HostBridge(values['bridge-url']!, pairing)
  const adapter = new CodexAdapter(native, new NativeReceipts(join(dirname(path), pairing.connector_id + '.receipts')), bridge, signal,
    connectionRecovery(values['bridge-url']!, pairing, signal))
  let presentation: PresentationStatus = { status: 'starting', checked_at: Date.now() }
  const lookup = async (session: string) => {
    const response = await fetch(new URL('/v1/codex/presentation?session_id=' + encodeURIComponent(session), values['bridge-url']!), {
      signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]), redirect: 'error',
      headers: { Authorization: `Bearer ${pairing.token}`, 'X-LazyMind-Connector-Id': pairing.connector_id },
    })
    if (!response.ok) throw Object.assign(new Error('Codex presentation lookup unavailable'), {status:response.status})
    return response.json() as Promise<{session_id:string;run_id:string}>
  }
  const parent = Number(values['parent-pid'])
  if (!Number.isSafeInteger(parent) || parent < 1) throw new Error('parent-pid is required')
  const timer = setInterval(async () => {
    try {
      process.kill(parent, 0)
      const latest = JSON.parse(await readFile(path, 'utf8'))
      if (!latest.enabled || latest.token !== pairing.token) stop()
    } catch { stop() }
  }, 1000)
  async function heartbeat() {
    const file = path + '.codex-status.json', temp = file + '.' + instance
    while (!signal.aborted) {
      let ready = false, issue = 'host_unavailable'
      try { ready = (await native.probe(signal)).ready === true; issue = ready ? '' : 'capability_unavailable' } catch { issue = 'cdp_unavailable' }
      try {
        await writeFile(temp, JSON.stringify({ connector_id: pairing.connector_id, checked_at: Date.now(), ready, issue, presentation }), { mode: 0o600 })
        await rename(temp, file)
      } catch {}
      try { await delay(2000, undefined, { signal }) } catch { break }
    }
  }
  try { await Promise.all([heartbeat(), createDispatcher(adapter, adapter.host, bridge, instance, signal).poll(),
    presentationLoop(Number(values['cdp-port']), lookup, values['bridge-url']!, signal, value => { presentation = value })]) }
  finally { stop(); clearInterval(timer); await unlock(); process.off('SIGINT', stop); process.off('SIGTERM', stop) }
}
main().catch(error => { console.error(String(error)); process.exitCode = 1 })
