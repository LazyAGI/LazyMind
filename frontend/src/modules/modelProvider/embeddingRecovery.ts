type Translate = (key: string, options?: { command: string }) => string;

const offlineCommand = "python -m lazymind.parsing.service.embedding_maintenance --apply --writers-stopped --confirm 'CLEAR TEXT VECTORS'";

export function embeddingOfflineMaintenanceInstructions(t: Translate) {
  return t("modelProvider.embeddingOfflineMaintenance", { command: offlineCommand });
}

const deletionReasonKeys = new Map([
  ["active_index_owner", "modelProvider.embeddingInUse"],
  ["legacy_selected_owner", "modelProvider.embeddingDeletionLegacyOwner"],
  ["index_identity_unverified", "modelProvider.embeddingDeletionUnverified"],
]);

export function getEmbeddingDeletionWarning(error: unknown, t: Translate): { content: string; duration?: number } | undefined {
  const response = (error as { response?: {
    status?: number;
    data?: { code?: number; detail?: unknown; message?: unknown; data?: { reason?: unknown; recovery?: unknown } };
  } })?.response;
  if (response?.status !== 409) return;

  const body = response.data;
  const detail = String(body?.detail || body?.message || "");
  if (![2005039, 2005081, 2005089].includes(body?.code ?? -1) &&
      !detail.includes("requires confirmation to downgrade") && !detail.includes("embedding model is in use")) return;

  const recovery = body?.data?.recovery;
  const reason = body?.data?.reason;
  const reasonKey = typeof reason === "string" ? deletionReasonKeys.get(reason) : undefined;
  if (recovery === "offline_vector_cleanup") {
    return {
      content: `${t(reasonKey || "modelProvider.embeddingDeletionUnverified")} ${embeddingOfflineMaintenanceInstructions(t)}`,
      duration: 12,
    };
  }
  return { content: t(reasonKey || "modelProvider.embeddingInUse") };
}
