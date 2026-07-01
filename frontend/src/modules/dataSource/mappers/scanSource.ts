import type { DataSourceSummary, DocumentStatusRow } from "../constants/types";
import { formatDateTime, resolveParsedDocumentCount, resolveStorageUsed } from "../utils/format";
import { normalizeDataSourceStatus } from "../utils/status";
import {
  getScanBindingId,
  getScanBindingTarget,
  getScanBindingTreeKey,
  getScanSourceConfigVersion,
  getScanSourceId,
  getScanSourceName,
  getScanSourceUpdatedAt,
  inferSourceKind,
  type ScanV2Binding,
  type ScanV2Source,
  type ScanV2Summary,
} from "../utils/scanAccessors";

export function buildDetailSummaryFromSource(
  source: ScanV2Source,
  summary: ScanV2Summary | undefined,
  documents: DocumentStatusRow[],
  binding?: ScanV2Binding | null,
  lastSyncedAt?: string,
): DataSourceSummary {
  const sourceId = getScanSourceId(source);
  const target = getScanBindingTarget(binding);
  const lastSync =
    formatDateTime(
      lastSyncedAt || binding?.updated_at || getScanSourceUpdatedAt(source),
    ) || "-";
  const isFeishuSource = inferSourceKind(source, binding) === "feishu";
  return {
    id: sourceId,
    name: getScanSourceName(source),
    target: target || "-",
    rootPath: target,
    targetRef: target,
    targetType: binding?.target_type,
    sourceType: isFeishuSource ? "feishu" : "local",
    documentCount: summary?.document_objects || summary?.total_objects || documents.length,
    parsedDocumentCount: resolveParsedDocumentCount(summary),
    status: normalizeDataSourceStatus(
      binding?.status || source.status,
      isFeishuSource ? true : binding?.sync_mode !== "manual",
    ),
    lastSync,
    addCount: summary?.new_count || 0,
    deleteCount: summary?.deleted_count || 0,
    changeCount: summary?.modified_count || 0,
    storageUsed: resolveStorageUsed(summary),
    documents,
    scanManaged: true,
    tenantId: source.tenant_id,
    agentId: binding?.agent_id,
    bindingId: getScanBindingId(binding),
    bindingTreeKey: getScanBindingTreeKey(binding),
    configVersion: getScanSourceConfigVersion(source),
  };
}
