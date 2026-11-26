import { workbuddyVersionIssue } from './compatibility'

// Serialized with its version check supplied explicitly.
// Native SDK records are the only identity source; currentId is never consulted.
export async function sdkOperation(input: any, versionIssue = workbuddyVersionIssue): Promise<any> {
  const host = globalThis as any
  const version = host.workbuddyDesktop?.appVersion
  const sdk = host.wb?.conversations
  const issue = versionIssue(version) || (typeof sdk?.get !== 'function' || typeof sdk?.local?.list !== 'function' ? 'capability_unavailable' : '')
  if (input.kind === 'probe') return { version: typeof version === 'string' ? version : '', ...(issue ? { issue } : {}) }
  if (issue) throw new Error(issue)
  if (input.kind === 'identity') {
    const canonical = (v: any): string => {
      if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']'
      if (v && typeof v === 'object') return '{' + Object.keys(v).sort().map(k => JSON.stringify(k) + ':' + canonical(v[k])).join(',') + '}'
      return JSON.stringify(v)
    }
    const matches: any[] = []
    const seen = new Set<string>()
    let complete = false
    // Exhaust the native list, including inactive UI tabs. Incomplete discovery fails closed.
    for (let page = 1; page <= 100; page++) {
      const result = await sdk.local.list({ page, size: 100 })
      if (!Array.isArray(result.items) || !Number.isFinite(result.total)) throw new Error('Unknown list format')
      let added = 0
      for (const info of result.items) {
        if (seen.has(info.id)) continue
        seen.add(info.id); added++
        if (!['working', 'pending', 'planning'].includes(info.state)) continue
        const conversation = await sdk.get(info.id)
        if (typeof conversation?.requests !== 'function') throw new Error('capability_unavailable')
        const requests = await conversation.requests(null)
        for (const request of requests.items) {
          if (['completed', 'cancelled', 'error'].includes(request.state)) continue
          for (const block of request.assistantMessage?.content ?? []) {
            if (block.type !== 'tool' || block.status !== 'in_progress' || block._meta?.['codebuddy.ai/compactTruncated']) continue
            let name = block.toolName ?? block._meta?.['codebuddy.ai/toolName']
            let args = block.rawInput
            if (name === 'DeferExecuteTool') { name = args?.toolName; args = args?.params }
            if (name !== 'mcp__lazymind__workflow.start' || canonical(args) !== canonical(input.arguments)) continue
            matches.push({ sessionId: info.id, requestId: request.id, toolCallId: block.toolCallId })
          }
        }
      }
      if (seen.size >= result.total) { complete = true; break }
      if (!added) break
    }
    if (!complete) throw new Error('Incomplete native session inventory')
    return { matches }
  }
  let conversation
  try { conversation = await sdk.get(input.sessionId) }
  catch (error) {
    // Some SDK versions throw for a deleted session. Distinguish that from an
    // outage using an exact-ID lookup; never create a replacement conversation.
    if (typeof sdk.listByIds === 'function') {
      const known = await sdk.listByIds([input.sessionId])
      if (Array.isArray(known) && known.length === 0) return { missing: true }
    }
    throw error
  }
  if (!conversation || conversation.info?.id !== input.sessionId) return { missing: true }
  if (input.kind === 'status') return { state: conversation.info.state, version: host.workbuddyDesktop.appVersion }
  if (input.kind === 'send') {
    if (conversation.info.state !== 'idle') return { rejected: 'Conversation became busy before admission' }
    if (typeof conversation.sendPrompt !== 'function') return { rejected: 'sendPrompt unavailable' }
    const result = await conversation.sendPrompt([{ type: 'text', text: input.message }], {
      clientRequestId: input.actionId, clientTimestamp: Date.now(), emitUserMessage: true,
    })
    if (result?.errorCode) return { rejected: String(result.errorCode) }
    return { accepted: true }
  }
  if (input.kind === 'cancel') {
    if (typeof conversation.cancel !== 'function') return { rejected: 'cancel unavailable' }
    // Deliberately session-scoped: Stop interrupts the currently running turn.
    const result = await conversation.cancel()
    if (result?.errorCode) return { rejected: String(result.errorCode) }
    return { accepted: true }
  }
  throw new Error('Unsupported SDK operation')
}
