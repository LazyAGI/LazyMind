import { open, readFile, unlink } from 'node:fs/promises'

/** One cooperating dispatcher per pairing/profile, including across DSH processes. */
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
      if (!Number.isSafeInteger(previous.pid) || previous.pid < 1) throw new Error('Invalid workflow dispatcher lock; repair this connection explicitly')
      try { process.kill(previous.pid, 0) }
      catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ESRCH') {
          // Only a definitely exited process can lose the lock without cooperating.
          await unlink(path)
          continue
        }
        throw error
      }
      throw new Error('This DSH profile already has a workflow dispatcher; close its previous DSH process')
    }
  }
  throw new Error('Could not acquire the workflow dispatcher lock')
}
