import type { NativeTurn } from './native'
/** Serialized into the renderer as well: keep all runtime helpers inside this function. */
export function workflowTurnEvidence(turn: NativeTurn) {
  const object = (value: any): Record<string, any> | undefined =>
    value && typeof value === 'object' && !Array.isArray(value) ? value : undefined
  const inputs: Record<string, any>[] = []
  let run: string | undefined
  // Native items retain invocation order when a pending tool receives its result.
  // A late completion before the latest input must not restore old ownership.
  for (const value of turn.items ?? []) {
    const item = object(value)
    if (!item) continue
    // Accepted steering can replace the server userMessage with a steered marker.
    // Pending steering also invalidates ownership until fresh Workflow evidence.
    if (['userMessage', 'steeringUserMessage', 'steered'].includes(item.type)) {
      inputs.push(item); run = undefined; continue
    }
    if (item.type !== 'mcpToolCall' || !['lazymind', 'lazymind-workflow'].includes(String(item.server))
      || !['workflow.start', 'workflow.step.begin', 'workflow.step.claim', 'workflow.step.resume', 'workflow.step.complete'].includes(String(item.tool))
      || item.status !== 'completed' || item.error) continue
    const result = object(item.result), fields = object(result?.structuredContent)
    if (!fields || result?.isError === true) continue
    const state = object(fields.state), execution = object(fields.execution)
    const id = fields.session_id ?? state?.session_id
    if (typeof id !== 'string' || !id) continue
    // An error or a read of another run never claims a turn. Acquisitions must
    // have a real execution, and their argument/result session must agree.
    if (item.tool !== 'workflow.start' && object(item.arguments)?.session_id !== id) continue
    if (['workflow.step.begin', 'workflow.step.claim', 'workflow.step.resume'].includes(String(item.tool)) && typeof execution?.execution_id !== 'string') continue
    run = id
  }
  return { runId: run, inputs }
}
