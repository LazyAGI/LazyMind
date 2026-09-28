import { open, readFile, unlink } from 'node:fs/promises'

export class DispatcherBusy extends Error {}

/** One cooperating dispatcher per Codex profile pairing. */
export async function dispatcherLock(pairingFile: string, instanceId: string): Promise<() => Promise<void>> {
  const path = `${pairingFile}.runtime.lock`
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const file = await open(path, 'wx', 0o600)
      try { await file.writeFile(JSON.stringify({ pid: process.pid, instanceId })) } finally { await file.close() }
      return async () => {
        try {
          const current = JSON.parse(await readFile(path, 'utf8'))
          if (current.instanceId === instanceId) await unlink(path)
        } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
      }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
      const previous = JSON.parse(await readFile(path, 'utf8'))
      if (!Number.isSafeInteger(previous.pid) || previous.pid < 1) throw new Error('Invalid Codex dispatcher lock; repair this connection explicitly')
      try { process.kill(previous.pid, 0) }
      catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ESRCH') {
          await unlink(path)
          continue
        }
        throw error
      }
      throw new DispatcherBusy('This Codex profile pairing already has a workflow dispatcher')
    }
  }
  throw new Error('Could not acquire the Codex workflow dispatcher lock')
}
