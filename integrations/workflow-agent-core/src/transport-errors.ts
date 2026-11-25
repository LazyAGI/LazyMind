export class BridgeError extends Error {
  constructor(readonly code: string, message: string, readonly status: number) { super(message) }
}

export function isHostAuthorizationError(error: unknown): error is BridgeError {
  // Core can reject an individual action or receipt with 403 (including HOST_AUTH_REQUIRED).
  // The Bridge reports a missing LazyMind session with 401 and pairing failures with explicit codes.
  return error instanceof BridgeError && (error.status === 401
    || ['HOST_PAIRING_MISSING', 'HOST_PAIRING_DISABLED', 'HOST_ACCOUNT_MISMATCH',
      'HOST_CREDENTIAL_MISMATCH', 'HOST_PAIRING_INVALID', 'HOST_PAIRING_UNREADABLE'].includes(error.code))
}
