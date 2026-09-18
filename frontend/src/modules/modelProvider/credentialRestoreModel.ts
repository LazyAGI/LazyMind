export type CredentialRestoreMode = "trusted_device" | "temporary";

export type CredentialRestoreStatus = {
  available: boolean;
  requiresExplicitAction: boolean;
  backupCount: number;
  status: "idle" | "confirming" | "pending" | "running" | "succeeded" | "failed" | "expired" | "canceled" | "conflict";
  completedRecords?: number;
  totalRecords?: number;
  failureCode?: string;
  temporaryExpiresAt?: string;
  operationId?: string;
};

export type CredentialRestoreView = {
  state: "unavailable" | "empty" | "available" | "confirming" | "progress" | "completed" | "failed" | "conflict";
  backupCount: number;
  completedRecords: number;
  totalRecords: number;
  canStart: boolean;
  canRetry: boolean;
  requiresExplicitAction: boolean;
  modeOptions: CredentialRestoreMode[];
};

export function deriveCredentialRestoreView(_status: CredentialRestoreStatus): CredentialRestoreView {
  const status = _status;
  const backupCount = Math.max(0, Number.isFinite(status.backupCount) ? status.backupCount : 0);
  const completedRecords = Math.max(0, Number.isFinite(status.completedRecords) ? status.completedRecords || 0 : 0);
  const totalRecords = Math.max(completedRecords, Number.isFinite(status.totalRecords) ? status.totalRecords || backupCount : backupCount);
  let state: CredentialRestoreView["state"];
  if (!status.available) state = "unavailable";
  else if (backupCount === 0) state = "empty";
  else if (status.status === "confirming") state = "confirming";
  else if (status.status === "pending" || status.status === "running") state = "progress";
  else if (status.status === "succeeded") state = "completed";
  else if (status.status === "conflict") state = "conflict";
  else if (status.status === "failed" || status.status === "expired" || status.status === "canceled") state = "failed";
  else state = "available";

  return {
    state,
    backupCount,
    completedRecords,
    totalRecords,
    canStart: state === "available",
    canRetry: state === "failed",
    requiresExplicitAction: status.requiresExplicitAction,
    modeOptions: ["trusted_device", "temporary"],
  };
}
