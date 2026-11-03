import { mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { createHash, randomUUID } from 'node:crypto'

interface Receipt {
  actionId: string; runId: string; threadId: string; kind: 'continue' | 'cancel'
  clientId?: string; turnId?: string; status: 'sending' | 'accepted' | 'unknown' | 'rejected'
}
const threadPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i

/** Receipts contain identifiers only. A pre-write prevents replay after process death. */
export class NativeReceipts {
  constructor(private directory: string) {}
  private path(id: string) { return join(this.directory, createHash('sha256').update(id).digest('hex') + '.json') }
  async read(id: string): Promise<Receipt | undefined> {
    try {
      const value = JSON.parse(await readFile(this.path(id), 'utf8')) as Receipt
      if (value.actionId !== id || !value.runId || !threadPattern.test(value.threadId)) throw new Error('Invalid native receipt')
      return value
    } catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return undefined; throw error }
  }
  async write(receipt: Receipt) {
    await mkdir(this.directory, { recursive: true, mode: 0o700 })
    const path = this.path(receipt.actionId), temp = `${path}.${randomUUID()}.tmp`
    await writeFile(temp, JSON.stringify(receipt), { mode: 0o600, flag: 'wx' })
    await rename(temp, path)
  }
}
