import { randomUUID } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'
import { WorkBuddyAdapter } from './adapter'
import { Cdp } from './cdp'

// Explicitly opt-in live tests; never create or choose a conversation.
const sessionId = process.argv[2]
const cancelLoop = process.argv[3] === '--cancel-loop'
if (!sessionId || process.argv.length > 4 || process.argv[3] && !cancelLoop) {
  throw new Error('Usage: node dist/diagnostics/verify.mjs DEDICATED_TEST_SESSION_ID [--cancel-loop]')
}
const cdp = new Cdp(9333)
const adapter = new WorkBuddyAdapter(cdp)
const signal = AbortSignal.timeout(90000)
if (!await adapter.ready(sessionId, signal)) throw new Error('Test session is not idle or CDP is unavailable')
const actionId = 'connector-test-' + randomUUID()
const message = cancelLoop
  ? '进行已获授权的打断测试：最多执行 20 次 shell 工具调用，每次只执行 sleep 1; printf "CDP_LOOP_TICK\\n"。必须每次单独调用工具，不要合并为一个循环；不读写文件、不执行其他命令。外部会在第 3 次左右停止你，收到停止后不要恢复。'
  : '请只回复 WB_CONNECTOR_OK，不要调用任何工具。'
await adapter.prompt(sessionId, { actionId, message }, signal)
let passed = false, cancellationRequested = false
while (!signal.aborted) {
  const result = await cdp.evaluate<any>(`(async () => {
    const c = await wb.conversations.get(${JSON.stringify(sessionId)});
    const page = await c.requests(null);
    const r = page.items.find(r => r.clientRequestId === ${JSON.stringify(actionId)});
    return r ? { requestId: r.id, state: r.state, sessionState: c.info.state,
      tools: (r.assistantMessage?.content ?? []).filter(b => b.type === 'tool').map(b => ({name:b.toolName, status:b.status, loop:JSON.stringify(b.rawInput ?? {}).includes('CDP_LOOP_TICK')})),
      text: (r.assistantMessage?.content ?? []).filter(b => b.type === 'text').map(b => b.text).join('') } : null;
  })()`, signal)
  if (cancelLoop && result && !cancellationRequested && result.tools.filter((tool: any) => tool.loop).length >= 3
    && result.tools.some((tool: any) => tool.loop && tool.status === 'in_progress')) {
    cancellationRequested = true
    await adapter.cancel(sessionId)
  }
  if (result && ['completed', 'cancelled', 'error'].includes(result.state) && result.sessionState === 'idle') {
    if (cancelLoop ? !cancellationRequested || result.state !== 'cancelled'
      : result.state !== 'completed' || result.text.trim() !== 'WB_CONNECTOR_OK') throw new Error('Unexpected native result: ' + JSON.stringify(result))
    console.log(JSON.stringify({ passed: true, sessionId, actionId, ...result, cancellation: adapter.cancellation }, null, 2))
    passed = true; break
  }
  await delay(200, undefined, { signal })
}
if (!passed) throw new Error('Live verification did not complete; no automatic resend')
