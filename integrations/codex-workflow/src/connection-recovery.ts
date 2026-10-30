import { setTimeout as delay } from 'node:timers/promises'
import type { Pairing } from '../../workflow-agent-core/src/transport'

// Only an actual pending control action requests UI. The background heartbeat
// never calls this. Report once per outage, including after the user dismisses.
export function connectionRecovery(base: string, pairing: Pairing, signal: AbortSignal) {
  let reported = false
  return {
    available() { reported = false },
    async unavailable(thread?: string) {
      if (reported || signal.aborted) return
      reported = true
      try {
        await delay(350, undefined, { signal })
        const response = await fetch(new URL('/v1/codex/recovery', base), {
          method: 'POST', redirect: 'error', signal: AbortSignal.any([signal, AbortSignal.timeout(12_000)]),
          headers: { Authorization: `Bearer ${pairing.token}`, 'X-LazyMind-Connector-Id': pairing.connector_id, 'Content-Type': 'application/json' },
          // The native read may fail for a reason unrelated to CDP. LazyMind
          // checks the current process/port/capability before choosing the UI.
          body: JSON.stringify({ code: 'CODEX_CONNECTION_CHECK', thread_id: thread }),
        })
        if (!response.ok) reported = false
      } catch { reported = false }
    },
  }
}
