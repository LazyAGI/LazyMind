import { readFile, stat, writeFile, rename, unlink } from 'node:fs/promises'
import { parseArgs } from 'node:util'
import { fileURLToPath } from 'node:url'
import { randomUUID } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { createDispatcher } from '../../../workflow-agent-core/src/dispatcher'
import type { Pairing } from '../../../workflow-agent-core/src/transport'
import { dispatcherLock, DispatcherBusy } from '../../../workflow-agent-core/node/runtime-lock'
import { WorkBuddyAdapter } from './adapter'
import { Cdp } from './cdp'
import { cleanupPresentation, presentationLoop, type PresentationStatus } from './presentation'
import { createDiagnostics, diagnosedTransport } from './diagnostics'
import { workBuddyConnection } from './connection'

async function main() {
  const { values } = parseArgs({ options: {
    'pairing-file': { type: 'string' }, 'bridge-url': { type: 'string', default: 'http://127.0.0.1:19091' },
    'cdp-port': { type: 'string', default: '9333' }, 'parent-pid': { type: 'string' }, 'stop-file': { type: 'string' },
    'cleanup-only': { type: 'boolean', default: false }, 'reload-cleanup': { type: 'boolean', default: false },
  } })
  const path = values['pairing-file']
  if (!path) throw new Error('pairing-file required')
  const info = await stat(path)
  if (!info.isFile() || process.platform !== 'win32' && (info.mode & 0o077) !== 0) throw new Error('Private pairing file required')
  const pairing = JSON.parse(await readFile(path, 'utf8')) as Pairing & { provider: string; enabled: boolean }
  if (pairing.provider !== 'workbuddy' || (!pairing.enabled && !values['cleanup-only']) || !/^host-[a-f0-9]{32}$/.test(pairing.connector_id)
    || !/^[a-f0-9]{64}$/.test(pairing.token)) throw new Error('Invalid WorkBuddy pairing')
  if (values['cleanup-only'] || values['reload-cleanup']) {
    if (values['cleanup-only'] && !values['reload-cleanup'] && pairing.enabled) throw new Error('Disable the pairing before cleaning presentation')
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
      await new Cdp(Number(values['cdp-port'])).evaluate(cleanupPresentation, AbortSignal.timeout(2000))
    } finally { await release() }
    return
  }
  const lifetime = new AbortController()
  const signal = lifetime.signal
  const stop = () => lifetime.abort()
  process.once('SIGINT', stop); process.once('SIGTERM', stop)
  const parent = values['parent-pid'] ? Number(values['parent-pid']) : undefined
  if (parent !== undefined && (!Number.isSafeInteger(parent) || parent < 1)) throw new Error('Invalid parent PID')
  const cdp = new Cdp(Number(values['cdp-port']))
  const log = createDiagnostics(pairing.connector_id)
  const adapter = new WorkBuddyAdapter(cdp, log)
  const { bridge, identities, lookupBinding } = workBuddyConnection(values['bridge-url']!, pairing, adapter, log, signal)
  const transport = diagnosedTransport(bridge, log)
  const instance = randomUUID()
  const unlock = await dispatcherLock(path, instance)
  const dispatcher = createDispatcher(adapter, undefined, transport, instance, signal)
  // Parent is Assistant Bridge, never an MCP connection or iframe.
  const stopFile = values['stop-file']
  const timer = setInterval(async () => {
    try {
      if (stopFile && await stat(stopFile).then(info => info.isFile(), () => false)) return stop()
      if (parent) process.kill(parent, 0)
      const current = JSON.parse(await readFile(path, 'utf8'))
      if (!current.enabled || current.token !== pairing.token || current.runtime_revision !== pairing.runtime_revision) stop()
    } catch { stop() }
  }, 1000)
  const statusPath = path + '.workbuddy-status.json'
  const temporaryStatus = statusPath + '.' + instance
  const connector = {
    connector_entry: fileURLToPath(import.meta.url),
    connector_version: process.env.LAZYMIND_WORKFLOW_CONNECTOR_VERSION,
    connector_revision: process.env.LAZYMIND_WORKFLOW_CONNECTOR_REVISION,
    connector_reload: process.env.LAZYMIND_WORKFLOW_CONNECTOR_RELOAD,
  }
  let presentation: PresentationStatus = { status: 'starting', checked_at: Date.now() }
  async function heartbeat() {
    while (!signal.aborted) {
      let version = '', ready = false, issue = 'host_unavailable'
      try {
        const probe = await adapter.probe(signal)
        version = probe.version; issue = probe.issue; ready = !issue
      } catch { /* A transport failure is not evidence of an unsupported version. */ }
      try {
        await writeFile(temporaryStatus, JSON.stringify({ ...connector, connector_id: pairing.connector_id, checked_at: Date.now(),
          ready, version, issue, connection: dispatcher.connection, pairing_revision: pairing.runtime_revision, cancellation: adapter.cancellation, bridge_url: values['bridge-url'], presentation }), { mode: 0o600 })
        await rename(temporaryStatus, statusPath)
      } catch { /* Status failure never changes delivery receipts. */ }
      try { await delay(2000, undefined, { signal }) } catch { break }
    }
  }
  try {
    console.log('WorkBuddy connector started; session wake enabled')
    let lastPresentation = ''
    await Promise.all([dispatcher.poll(), identities(), heartbeat(), presentationLoop(cdp, lookupBinding, values['bridge-url']!, signal, value => {
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
