export type CredentialBackupStatus = {
  enabled: boolean;
  backedUp: number;
  pending: number;
  failed: number;
  lastSucceededAt?: string;
};

export type CredentialBackupView = {
  enabled: boolean;
  summary: "disabled" | "healthy" | "pending" | "failed";
  backedUp: number;
  pending: number;
  failed: number;
  lastSucceededAt?: string;
};

export function deriveCredentialBackupView(_status: CredentialBackupStatus): CredentialBackupView {
  const status = _status;
  const summary: CredentialBackupView["summary"] = !status.enabled
    ? "disabled"
    : status.failed > 0
      ? "failed"
      : status.pending > 0
        ? "pending"
        : "healthy";

  return {
    enabled: status.enabled,
    summary,
    backedUp: Math.max(0, status.backedUp),
    pending: Math.max(0, status.pending),
    failed: Math.max(0, status.failed),
    ...(status.lastSucceededAt ? { lastSucceededAt: status.lastSucceededAt } : {}),
  };
}
