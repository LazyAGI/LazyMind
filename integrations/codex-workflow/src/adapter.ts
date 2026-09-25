import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { AdmissionRejected, type RuntimeAdapter } from '../../workflow-agent-core/src/adapter'

const runFile = promisify(execFile)
const threadPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i
export type QueueRunner = (binary: string, args: string[], signal: AbortSignal, codexHome: string) => Promise<void>
const queue: QueueRunner = async (binary, args, signal, codexHome) => {
  await runFile(binary, args, { signal, timeout: 15_000, maxBuffer: 1024 * 1024,
    env: { ...process.env, CODEX_HOME: codexHome } })
}

/** Admission only: no native turn inspection, cancellation, or uncertain retries. */
export class CodexAdapter implements RuntimeAdapter<string> {
  readonly continuationMode = 'queue' as const
  readonly supportsCancel = false as const
  constructor(private binary: string, private codexHome: string, private run: QueueRunner = queue) {}
  id(threadId: string) { return threadId }
  warn(message: string) { console.warn(message) }
  async resolve(threadId: string): Promise<{ agent: string } | { error: string }> {
    // Never use names: queue accepts them, but names need not identify one task.
    return threadPattern.test(threadId) ? { agent: threadId } : { error: 'A Codex thread UUID is required' }
  }
  async prompt(threadId: string, input: { actionId: string; message: string }, signal: AbortSignal) {
    if ('error' in await this.resolve(threadId)) throw new Error('A Codex thread UUID is required')
    if (signal.aborted) throw new AdmissionRejected('Queue delivery was aborted before launch')
    const message = `[LazyMind action_id=${input.actionId}]\n${input.message}\n` +
      'This queued notification may be delayed. Read workflow.state before taking any action; current Core state and authorization override this notification. ' +
      'When control.continuation is awaiting_user, report that review is needed and END this turn. Do not approve the review yourself, begin another step, or poll while waiting. ' +
      'Also end this turn for awaiting_executor, stopped, completed, or binding_required. Wait for a new notification or explicit user input.'
    try {
      await this.run(this.binary, ['queue', '--thread', threadId, '--message', message], signal, this.codexHome)
    } catch (error) {
      if (['ENOENT', 'EACCES', 'ENOEXEC'].includes((error as NodeJS.ErrnoException).code ?? '')) {
        throw new AdmissionRejected('Codex queue executable could not be launched')
      }
      // A nonzero exit, timeout, or abort after launch may follow successful enqueue.
      // Do not include execFile's full command (which contains the prompt) in receipts.
      throw new Error('Codex queue receipt is uncertain; do not automatically resend')
    }
    // CLI success means accepted input, not execution. No invented native event sequence.
    return 0
  }
  cancel(): never { throw new Error('Codex queue does not support turn interruption') }
}
