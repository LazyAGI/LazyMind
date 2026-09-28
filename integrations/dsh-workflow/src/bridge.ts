import { readFile, stat } from 'node:fs/promises'
import { object } from '../../workflow-agent-core/src/protocol'
import type { Pairing } from '../../workflow-agent-core/src/transport'
export * from '../../workflow-agent-core/src/transport'

/** The only credential loaded is LazyMind's scoped local pairing, never DSH's signing key. */
export async function loadPairing(path: string): Promise<Pairing> {
  if (!path) throw new Error('Reconnect DeepSeek Harness from LazyMind to configure the workflow bundle')
  const info = await stat(path)
  if (!info.isFile() || (process.platform !== 'win32' && (info.mode & 0o077) !== 0)) throw new Error('Workflow pairing must be a private file')
  const value = object(JSON.parse(await readFile(path, 'utf8')))
  if (!value || typeof value.connector_id !== 'string' || typeof value.token !== 'string' || value.token.length !== 64 || value.enabled !== true) {
    throw new Error('Workflow pairing is unavailable; reconnect DeepSeek Harness from LazyMind')
  }
  return { connector_id: value.connector_id, token: value.token }
}
