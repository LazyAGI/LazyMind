import type { TFunction } from "i18next";
import type { DataSourceSummary, DocumentStatusRow } from "../constants/types";
import { formatBytes, formatDateTime } from "../utils/format";
import {
  normalizeDataSourceFileUpdateState,
  normalizeDataSourceParseStatus,
} from "../utils/status";
import {
  normalizePendingAction,
  resolveSourceState,
  resolveSyncState,
} from "../utils/sourceState";
import {
  getDocumentDisplayName,
  getDocumentLastUpdatedAt,
  getDocumentPath,
  type ScanV2Document,
} from "../utils/scanAccessors";

export function mapScanSyncDetail(
  updateState: DocumentStatusRow["updateState"],
  t: TFunction,
) {
  if (updateState === "new") {
    return t("admin.dataSourceFileUpdateNewDetail");
  }
  if (updateState === "changed") {
    return t("admin.dataSourceFileUpdateChangedDetail");
  }
  if (updateState === "deleted") {
    return t("admin.dataSourceFileUpdateDeletedDetail");
  }
  return t("admin.dataSourceFileUpdateUnchangedDetail");
}

export function stringifyScanError(value: unknown) {
  if (!value) return undefined;
  if (typeof value === "string") return value;
  if (typeof value === "object" && value !== null) {
    const message =
      (value as { message?: string; error?: string }).message ||
      (value as { message?: string; error?: string }).error;
    return message || JSON.stringify(value);
  }
  return `${value}`;
}

export function mapScanDocumentToDetail(
  item: ScanV2Document,
  t: TFunction,
  sourceType?: DataSourceSummary["sourceType"],
): DocumentStatusRow {
  const sourceState = resolveSourceState({
    source_state: item.source_state,
    update_type: item.update_type || item.source_state,
    has_update: item.has_update ?? item.source_state !== "UNCHANGED",
  });
  const syncState = resolveSyncState({
    sync_state: item.sync_state,
  });
  const updateState = normalizeDataSourceFileUpdateState(
    item.update_type || item.source_state,
    item.has_update ?? item.source_state !== "UNCHANGED",
  );
  const effectiveParseState = item.effective_parse_status || item.effectiveParseStatus;
  const fallbackParseState = [
    item.parse_state,
    item.parse_status,
    item.parse_queue_state,
    item.core_task_state,
    item.scan_orchestration_status,
  ]
    .filter(Boolean)
    .join(" ");
  const parseState = `${effectiveParseState || ""}`.trim() || fallbackParseState;
  const lastSyncedAt = formatDateTime(getDocumentLastUpdatedAt(item));
  return {
    id: `${item.document_id}`,
    name: getDocumentDisplayName(item),
    path: getDocumentPath(item),
    size: formatBytes(item.size_bytes),
    tags: item.tags || [],
    updateState,
    syncDetail: item.update_desc || mapScanSyncDetail(updateState, t),
    parseStatus: normalizeDataSourceParseStatus(parseState, item.last_error, {
      sourceType,
    }),
    sourceUpdatedAt: lastSyncedAt || "-",
    updatedAt: lastSyncedAt || "-",
    sourceState,
    syncState,
    pendingAction: normalizePendingAction(item.pending_action),
    nextSyncAt: item.next_sync_at,
    lastError: stringifyScanError(item.last_error),
    knowledgeBasePresent: item.knowledge_base_present,
  };
}
