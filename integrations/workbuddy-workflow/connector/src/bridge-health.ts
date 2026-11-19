import { BridgeError, isHostAuthorizationError } from '../../../workflow-agent-core/src/transport'

type Source = 'workflow' | 'workbuddy'
const pairingIssues: Record<string, string> = {
  HOST_PAIRING_MISSING: 'pairing_required', HOST_PAIRING_DISABLED: 'pairing_disabled',
  HOST_ACCOUNT_MISMATCH: 'pairing_account_mismatch', HOST_CREDENTIAL_MISMATCH: 'pairing_credential_mismatch',
  HOST_PAIRING_INVALID: 'pairing_invalid', HOST_PAIRING_UNREADABLE: 'pairing_unreadable',
}

// Local identity routes do not contact Core. Keep their evidence separate from
// workflow requests so a local success cannot mask expired Core credentials.
export class BridgeHealth {
  private readonly sources = {
    workflow: { issue: 'lazymind_unavailable', revision: 0, failures: 0, retryAt: 0 },
    workbuddy: { issue: 'lazymind_unavailable', revision: 0, failures: 0, retryAt: 0 },
  }
  constructor(private readonly changed: (issue: string) => void = () => {}) {}

  issue(): string {
    const issues = Object.values(this.sources).map(value => value.issue)
    return issues.find(issue => issue && issue !== 'lazymind_unavailable') || issues.find(Boolean) || ''
  }
  retryAfter(source: Source): number { return Math.max(0, this.sources[source].retryAt - Date.now()) }

  async wait(source: Source, signal: AbortSignal): Promise<void> {
    signal.throwIfAborted()
    while (this.retryAfter(source) > 0) {
      await new Promise<void>((resolve, reject) => {
        const stop = () => { clearTimeout(timer); reject(signal.reason) }
        const timer = setTimeout(() => { signal.removeEventListener('abort', stop); resolve() }, this.retryAfter(source))
        signal.addEventListener('abort', stop, { once: true })
      })
      signal.throwIfAborted()
    }
  }

  async track<T>(source: Source, request: () => Promise<T>): Promise<T> {
    const state = this.sources[source], revision = state.revision
    const update = (issue: string, retry: number) => {
      const before = this.issue()
      state.issue = issue; state.retryAt = retry ? Date.now() + retry : 0
      if (before !== this.issue()) {
        try { this.changed(this.issue()) } catch { /* Health diagnostics cannot affect delivery. */ }
      }
    }
    try {
      const result = await request()
      // An older request finishing late is not evidence of recovery after a failure.
      if (state.revision === revision) { state.failures = 0; update('', 0) }
      return result
    } catch (error) {
      if (isHostAuthorizationError(error)) {
        state.revision++; update(pairingIssues[error.code] || 'lazymind_auth_required', 30_000)
      } else if (!(error instanceof BridgeError) || error.status >= 500) {
        state.revision++; state.failures++
        // A network failure cannot prove that a previous authorization issue is repaired.
        update(state.issue && state.issue !== 'lazymind_unavailable' ? state.issue : 'lazymind_unavailable',
          Math.min(30_000, 1000 * 2 ** Math.min(state.failures - 1, 5)))
      }
      throw error
    }
  }
}
