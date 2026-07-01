import type { TFunction } from "i18next";
import type {
  DocumentLikeStatus,
  FileUpdateState,
  PendingActionValue,
  SourceStateMeta,
  SourceStateValue,
  SyncStateMeta,
  SyncStateMetaOptions,
  SyncStateValue,
} from "../constants/types";
import { formatDateTime } from "./format";
import { normalizeDataSourceFileUpdateState } from "./status";

const VALID_SOURCE_STATES: ReadonlyArray<SourceStateValue> = [
  "UNCHANGED",
  "NEW",
  "MODIFIED",
  "DELETED",
];

const VALID_SYNC_STATES: ReadonlyArray<SyncStateValue> = [
  "IDLE",
  "PENDING",
  "SCHEDULED",
  "RUNNING",
  "FAILED",
];

const VALID_PENDING_ACTIONS: ReadonlyArray<PendingActionValue> = [
  "NONE",
  "CREATE",
  "UPDATE",
  "DELETE",
];

export function normalizeSourceState(value?: string): SourceStateValue | undefined {
  if (!value) return undefined;
  const upper = `${value}`.trim().toUpperCase() as SourceStateValue;
  return VALID_SOURCE_STATES.includes(upper) ? upper : undefined;
}

export function normalizeSyncState(value?: string): SyncStateValue | undefined {
  if (!value) return undefined;
  const upper = `${value}`.trim().toUpperCase() as SyncStateValue;
  return VALID_SYNC_STATES.includes(upper) ? upper : undefined;
}

export function normalizePendingAction(value?: string): PendingActionValue | undefined {
  if (!value) return undefined;
  const upper = `${value}`.trim().toUpperCase() as PendingActionValue;
  return VALID_PENDING_ACTIONS.includes(upper) ? upper : undefined;
}

// Translate the new SourceState into the legacy FileUpdateState used across
// existing UI helpers, so old fall-back paths keep working.
export function sourceStateToFileUpdate(state?: SourceStateValue): FileUpdateState {
  if (state === "NEW") return "new";
  if (state === "MODIFIED") return "changed";
  if (state === "DELETED") return "deleted";
  return "unchanged";
}

export function resolveSourceState(input: DocumentLikeStatus): SourceStateValue {
  const explicit = normalizeSourceState(input.source_state);
  if (explicit) {
    return explicit;
  }
  // Fallback: derive from legacy fields so older payloads still render.
  const legacy = normalizeDataSourceFileUpdateState(input.update_type, input.has_update);
  if (legacy === "new") return "NEW";
  if (legacy === "changed") return "MODIFIED";
  if (legacy === "deleted") return "DELETED";
  return "UNCHANGED";
}

export function resolveSyncState(input: DocumentLikeStatus): SyncStateValue {
  return normalizeSyncState(input.sync_state) || "IDLE";
}

export function getSourceStateMeta(state: SourceStateValue, t: TFunction): SourceStateMeta {
  if (state === "NEW") {
    return {
      color: "success",
      text: t("admin.dataSourceSourceStateNew"),
      tone: "new",
    };
  }
  if (state === "MODIFIED") {
    return {
      color: "processing",
      text: t("admin.dataSourceSourceStateModified"),
      tone: "changed",
    };
  }
  if (state === "DELETED") {
    return {
      color: "error",
      text: t("admin.dataSourceSourceStateDeleted"),
      tone: "deleted",
    };
  }
  return {
    color: "default",
    text: t("admin.dataSourceSourceStateUnchanged"),
    tone: "unchanged",
  };
}

export function getSyncStateMeta(
  state: SyncStateValue,
  options: SyncStateMetaOptions,
  t: TFunction,
): SyncStateMeta {
  if (state === "RUNNING") {
    return { color: "processing", text: t("admin.dataSourceSyncStateRunning") };
  }
  if (state === "FAILED") {
    const error = `${options.lastError || ""}`.trim();
    return {
      color: "error",
      text: error
        ? t("admin.dataSourceSyncStateFailedWithError", { error })
        : t("admin.dataSourceSyncStateFailed"),
    };
  }
  if (state === "SCHEDULED") {
    const time = formatDateTime(options.nextSyncAt);
    return {
      color: "warning",
      text:
        time && time !== "-"
          ? t("admin.dataSourceSyncStateScheduledAt", { time })
          : t("admin.dataSourceSyncStateScheduled"),
    };
  }
  if (state === "PENDING") {
    return { color: "warning", text: t("admin.dataSourceSyncStatePending") };
  }
  return { color: "default", text: t("admin.dataSourceSyncStateIdle") };
}

// Build a friendly human-readable detail line for a document/tree node, e.g.
// Deleted at source but still retained in the knowledge base until sync removes it.
export function buildDocumentStatusDetail(
  input: DocumentLikeStatus,
  t: TFunction,
): string {
  const sourceState = resolveSourceState(input);
  const syncState = resolveSyncState(input);

  if (sourceState === "DELETED") {
    if (input.knowledge_base_present === false) {
      return t("admin.dataSourceFileUpdateDeletedDoneDetail");
    }
    return t("admin.dataSourceFileUpdateDeletedPendingDetail");
  }
  if (syncState === "FAILED" && input.last_error) {
    return t("admin.dataSourceSyncStateFailedWithError", { error: input.last_error });
  }
  if (syncState === "SCHEDULED" && input.next_sync_at) {
    return t("admin.dataSourceSyncStateScheduledAt", {
      time: formatDateTime(input.next_sync_at),
    });
  }
  if (sourceState === "NEW") return t("admin.dataSourceFileUpdateNewDetail");
  if (sourceState === "MODIFIED") return t("admin.dataSourceFileUpdateChangedDetail");
  return t("admin.dataSourceFileUpdateUnchangedDetail");
}
