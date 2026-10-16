import { readFile, stat } from 'node:fs/promises'
import { homedir } from 'node:os'
import { isAbsolute, join, resolve } from 'node:path'
import { parseArgs, promisify } from 'node:util'
import { randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { createDispatcher } from '../../workflow-agent-core/src/dispatcher'
import { HostBridge } from '../../workflow-agent-core/src/transport'
import { setTimeout as delay } from 'node:timers/promises'
import { dispatcherLock, DispatcherBusy } from './runtime-lock'
import { openRuntime, sharedEndpoint } from './runtime'
import { sameProfile } from './profile'

const runFile = promisify(execFile)
async function main() {
  const { values } = parseArgs({ options: {
    'managed': { type: 'boolean', default: false }, 'parent-pid': { type: 'string' },
    'codex-bin': { type: 'string' }, 'codex-home': { type: 'string' },
    'pairing-file': { type: 'string' }, 'lazymind-cli': { type: 'string', default: 'lazymind' },
    'bridge-url': { type: 'string', default: 'http://127.0.0.1:19091' },
    'app-server-url': { type: 'string' },
  } })
  const binary = values['codex-bin']
  if (!binary || !isAbsolute(binary)) throw new Error('--codex-bin must be the absolute path to the desktop bundled Codex executable')
  const profile = resolve(values['codex-home'] ?? process.env.CODEX_HOME ?? join(homedir(), '.codex'))
  let path = values['pairing-file']
  if (!path) {
    const { stdout } = await runFile(values['lazymind-cli']!, ['internal', 'codex-workflow-pair', '--codex-home', profile])
    path = JSON.parse(stdout).pairing_file
  }
  if (!path) throw new Error('LazyMind did not return a pairing file')
  const info = await stat(path)
  // Windows uses profile ACLs, not the synthetic POSIX mode returned by stat.
  if (!info.isFile() || process.platform !== 'win32' && (info.mode & 0o077) !== 0) throw new Error('Pairing must be a private file (0600)')
  const pairing = JSON.parse(await readFile(path, 'utf8'))
  if (pairing.provider !== 'codex' || pairing.enabled !== true || !await sameProfile(pairing.profile, profile)
    || !/^host-[a-f0-9]{32}$/.test(pairing.connector_id) || !/^[a-f0-9]{64}$/.test(pairing.token)) {
    throw new Error('Pairing does not match this Codex profile')
  }
  const lifetime = new AbortController()
  const stop = () => lifetime.abort()
  process.once('SIGINT', stop)
  process.once('SIGTERM', stop)
  const parentPID = values['parent-pid'] ? Number(values['parent-pid']) : undefined
  if (parentPID !== undefined && (!Number.isSafeInteger(parentPID) || parentPID < 1)) throw new Error('Invalid parent PID')
  // A killed MCP process cannot run cleanup. Its worker must not outlive it.
  const parentWatch = parentPID === undefined ? undefined : setInterval(() => {
    try { process.kill(parentPID, 0) }
    catch (error) { if ((error as NodeJS.ErrnoException).code === 'ESRCH') lifetime.abort() }
  }, 1000)
  parentWatch?.unref()
  const instanceId = randomUUID()
  let unlock: (() => Promise<void>) | undefined
  let closeRuntime: (() => void) | undefined
  let modeWatch: ReturnType<typeof setInterval> | undefined
  try {
    while (!lifetime.signal.aborted) {
      try { unlock = await dispatcherLock(path, instanceId); break }
      catch (error) {
        if (!values.managed || !(error instanceof DispatcherBusy)) throw error
        await delay(1000, undefined, { signal: lifetime.signal })
      }
    }
    lifetime.signal.throwIfAborted()
    const bridge = new HostBridge(values['bridge-url']!, pairing)
    const selectedEndpoint = values['app-server-url'] ?? await sharedEndpoint(profile)
    const runtime = await openRuntime({ binary, profile, pairingFile: path, connectorId: pairing.connector_id,
      bridge, lifetime, endpoint: selectedEndpoint ?? null })
    closeRuntime = runtime.close
    // Reconfiguration takes effect at a connection boundary, never mid-delivery
    // by switching transports and resending the same action.
    if (!values['app-server-url']) {
      let checking = false
      modeWatch = setInterval(async () => {
        if (checking) return
        checking = true
        try { if (await sharedEndpoint(profile) !== selectedEndpoint) lifetime.abort(new Error('Shared server configuration changed; restart dispatcher')) }
        catch { lifetime.abort(new Error('Shared server configuration became invalid')) }
        finally { checking = false }
      }, 1000)
      modeWatch.unref()
    }
    console.log(`Codex Workflow ${runtime.mode} dispatcher ready. Cancellation: ${runtime.adapter.cancellation}.`)
    await createDispatcher(runtime.adapter, runtime.host, bridge, instanceId, lifetime.signal).poll()
  } finally {
    lifetime.abort()
    if (modeWatch) clearInterval(modeWatch)
    closeRuntime?.()
    if (parentWatch) clearInterval(parentWatch)
    if (unlock) await unlock()
    process.removeListener('SIGINT', stop)
    process.removeListener('SIGTERM', stop)
  }
}
main().catch(error => { console.error(String(error)); process.exitCode = 1 })
