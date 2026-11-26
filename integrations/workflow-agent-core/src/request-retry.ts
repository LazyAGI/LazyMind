import { BridgeError, isHostAuthorizationError } from './transport-errors'

const pairingIssues: Record<string, string> = {
  HOST_PAIRING_MISSING: 'pairing_required', HOST_PAIRING_DISABLED: 'pairing_disabled',
  HOST_ACCOUNT_MISMATCH: 'pairing_account_mismatch', HOST_CREDENTIAL_MISMATCH: 'pairing_credential_mismatch',
  HOST_PAIRING_INVALID: 'pairing_invalid', HOST_PAIRING_UNREADABLE: 'pairing_unreadable',
}

export interface RequestObservation {
  checked_at: number
  issue: string
}

export class RequestRetry {
  private failures = 0
  retryAt = 0
  observation?: RequestObservation

  resetBackoff() {
    this.failures = 0
    this.retryAt = 0
  }

  succeeded() {
    this.resetBackoff()
    this.observeSuccess()
  }

  observeSuccess() {
    this.observation = { checked_at: Date.now(), issue: '' }
  }

  failed(error: unknown) {
    const now = Date.now()
    const authorization = isHostAuthorizationError(error)
    const previous = this.observation?.issue
    const issue = authorization
      ? Object.hasOwn(pairingIssues, error.code) ? pairingIssues[error.code] : 'lazymind_auth_required'
      : previous || 'lazymind_unavailable'
    const delay = authorization ? 30_000 : Math.min(30_000, 1000 * 2 ** Math.min(this.failures, 5))
    this.failures++
    this.retryAt = now + delay
    this.observation = { checked_at: now, issue }
    return { authorization, issue }
  }
}

export function requestErrorCode(error: unknown): string {
  return error instanceof BridgeError && /^[A-Z][A-Z0-9_]{0,63}$/.test(error.code) ? error.code : 'BRIDGE_UNAVAILABLE'
}

export function waitUntil(at: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return }
    const abort = () => { clearTimeout(timer); reject(signal.reason) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, Math.max(0, at - Date.now()))
    signal.addEventListener('abort', abort, { once: true })
  })
}
