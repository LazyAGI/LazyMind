import { readFile, stat, writeFile, rename, unlink } from 'node:fs/promises'
import { parseArgs } from 'node:util'
import { randomUUID } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { createDispatcher } from '../../../workflow-agent-core/src/dispatcher'
import { HostBridge, type Pairing } from '../../../workflow-agent-core/src/transport'
import { dispatcherLock, DispatcherBusy } from '../../../workflow-agent-core/node/runtime-lock'
import { WorkBuddyAdapter } from './adapter'
import { Cdp } from './cdp'
import { presentationLoop, type PresentationStatus } from './presentation'
import { createDiagnostics, diagnosedTransport } from './diagnostics'

async function main() {
  const { values } = parseArgs({ options: {
    'pairing-file': { type: 'string' }, 'bridge-url': { type: 'string', default: 'http://127.0.0.1:19091' },
    'cdp-port': { type: 'string', default: '9333' }, 'parent-pid': { type: 'string' },
    'cleanup-only': { type: 'boolean', default: false },
  } })
  const path = values['pairing-file']
  if (!path) throw new Error('pairing-file required')
  const info = await stat(path)
  if (!info.isFile() || process.platform !== 'win32' && (info.mode & 0o077) !== 0) throw new Error('Private pairing file required')
  const pairing = JSON.parse(await readFile(path, 'utf8')) as Pairing & { provider: string; enabled: boolean }
  if (pairing.provider !== 'workbuddy' || (!pairing.enabled && !values['cleanup-only']) || !/^host-[a-f0-9]{32}$/.test(pairing.connector_id)
    || !/^[a-f0-9]{64}$/.test(pairing.token)) throw new Error('Invalid WorkBuddy pairing')
  if (values['cleanup-only']) {
    if (pairing.enabled) throw new Error('Disable the pairing before cleaning presentation')
    const deadline = Date.now() + 5000
    let release: (() => Promise<void>) | undefined
    while (!release) {
      try { release = await dispatcherLock(path, randomUUID()) }
      catch (error) {
        if (!(error instanceof DispatcherBusy) || Date.now() >= deadline) throw error
        await delay(100)
      }
    }
    try {
      await new Cdp(Number(values['cdp-port'])).evaluate(
        'window.__lazymindWorkflowTail?.dispose(); window.__lazymindWorkflowPresentationSession?.dispose()', AbortSignal.timeout(2000))
    } finally { await release() }
    return
  }
  const bridge = new HostBridge(values['bridge-url']!, pairing) // Validates loopback URL before credential use.
  const lifetime = new AbortController()
  const signal = lifetime.signal
  const stop = () => lifetime.abort()
  process.once('SIGINT', stop); process.once('SIGTERM', stop)
  const parent = values['parent-pid'] ? Number(values['parent-pid']) : undefined
  if (parent !== undefined && (!Number.isSafeInteger(parent) || parent < 1)) throw new Error('Invalid parent PID')
  const cdp = new Cdp(Number(values['cdp-port']))
  const log = createDiagnostics(pairing.connector_id)
  const adapter = new WorkBuddyAdapter(cdp, log)
  const transport = diagnosedTransport(bridge, log)
  const instance = randomUUID()
  const unlock = await dispatcherLock(path, instance)
  // Parent is Assistant Bridge, never an MCP connection or iframe.
  const timer = setInterval(async () => {
    try {
      if (parent) process.kill(parent, 0)
      const current = JSON.parse(await readFile(path, 'utf8'))
      if (!current.enabled || current.token !== pairing.token) stop()
    } catch { stop() }
  }, 1000)
  const statusPath = path + '.workbuddy-status.json'
  const temporaryStatus = statusPath + '.' + instance
  let presentation: PresentationStatus = { status: 'starting', checked_at: Date.now() }
  async function heartbeat() {
    while (!signal.aborted) {
      let version = '', ready = false, issue = 'host_unavailable'
      try {
        const probe = await adapter.probe(signal)
        version = probe.version; issue = probe.issue || ''; ready = !issue
      } catch { /* A transport failure is not evidence of an unsupported version. */ }
      try {
        await writeFile(temporaryStatus, JSON.stringify({ connector_id: pairing.connector_id, checked_at: Date.now(),
          ready, version, issue, cancellation: adapter.cancellation, bridge_url: values['bridge-url'], presentation }), { mode: 0o600 })
        await rename(temporaryStatus, statusPath)
      } catch { /* Status failure never changes delivery receipts. */ }
      try { await delay(2000, undefined, { signal }) } catch { break }
    }
  }
  const request = async (route: string, body?: unknown) => {
    const response = await fetch(values['bridge-url']! + '/v1/workbuddy/' + route, {
      method: body === undefined ? 'GET' : 'POST', redirect: 'error',
      signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
      headers: { Authorization: `Bearer ${pairing.token}`, 'X-LazyMind-Connector-Id': pairing.connector_id,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
    if (!response.ok) throw Object.assign(new Error('WorkBuddy bridge unavailable'), { status: response.status })
    return response.json() as Promise<any>
  }
  async function identities() {
    while (!signal.aborted) {
      try {
        const page = await request('identity-requests')
        for (const item of page.requests) {
          try {
            const started = performance.now()
            const identity = await adapter.identify(item.arguments, signal)
            await request('identity-receipts', { id: item.id, ...identity })
            log('identity.resolved', { identity_request_id: item.id, native_session_id: identity.sessionId,
              native_request_id: identity.requestId, tool_call_id: identity.toolCallId, status: 'resolved', duration_ms: performance.now() - started })
          } catch {
            // Native tool visibility may lag dispatch. Retry discovery only,
            // never send a message or infer identity from the active UI tab.
          }
        }
      } catch { /* Temporary bridge/CDP outage; callers have bounded waits. */ }
      try { await delay(250, undefined, { signal }) } catch { break }
    }
  }
  try {
    console.log('WorkBuddy connector started; session wake and interruption enabled')
    let lastPresentation = ''
    await Promise.all([createDispatcher(adapter, adapter.control(signal), transport, instance, signal).poll(), identities(), heartbeat(), presentationLoop(cdp, request, values['bridge-url']!, signal, value => {
      presentation = value
      const fields = { status: value.status, page_status: value.page_status, run_id: value.run_id, native_session_id: value.native_session_id }
      const key = JSON.stringify(fields)
      if (key !== lastPresentation) { lastPresentation = key; log('presentation', fields) }
    })])
  } finally {
    stop(); clearInterval(timer); await unlock()
    await unlink(temporaryStatus).catch(() => {})
    process.removeListener('SIGINT', stop); process.removeListener('SIGTERM', stop)
  }
}
main().catch(error => { console.error(String(error)); process.exitCode = 1 })
