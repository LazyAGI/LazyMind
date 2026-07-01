import type { TFunction } from "i18next";
import type {
  ConflictPolicy,
  ConnectionState,
  DataSourceKind,
  DataSourceParseStatusOptions,
  DetailParseStatus,
  FileCandidate,
  FileUpdateState,
  OAuthState,
  SourceStatus,
  SourceType,
  SyncMode,
} from "../constants/types";

export function isCloudType(type?: SourceType) {
  return type === "feishu" || type === "notion";
}

function getStatusTokens(value?: string) {
  return `${value || ""}`
    .trim()
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

function hasStatusToken(value: string | undefined, candidates: string[]) {
  const tokens = getStatusTokens(value);
  return candidates.some((candidate) => tokens.includes(candidate));
}

function hasStatusText(value: string | undefined, candidates: string[]) {
  const normalized = `${value || ""}`.trim().toLowerCase();
  return candidates.some((candidate) => normalized.includes(candidate));
}

export function normalizeDataSourceStatus(
  status?: string,
  watchEnabled?: boolean,
): SourceStatus {
  if (hasStatusToken(status, ["delete", "deleted", "deleting", "removed"])) {
    return "deleted";
  }
  if (
    hasStatusToken(status, [
      "error",
      "errored",
      "fail",
      "failed",
      "failure",
      "invalid",
    ])
  ) {
    return "error";
  }
  if (hasStatusToken(status, ["expired", "expire", "token_expired"])) {
    return "expired";
  }
  if (
    hasStatusToken(status, [
      "disabled",
      "disable",
      "paused",
      "pause",
      "stopped",
      "stop",
      "inactive",
    ]) ||
    watchEnabled === false
  ) {
    return "paused";
  }
  return "active";
}

export function normalizeDataSourceConnectionState(status?: string): ConnectionState {
  if (hasStatusToken(status, ["expired", "expire", "token_expired", "inactive"])) {
    return "expired";
  }
  if (
    hasStatusToken(status, [
      "error",
      "errored",
      "fail",
      "failed",
      "failure",
      "invalid",
    ])
  ) {
    return "error";
  }
  if (
    hasStatusToken(status, [
      "pending",
      "waiting",
      "authorizing",
      "queued",
      "processing",
      "syncing",
    ])
  ) {
    return "pending";
  }
  return "connected";
}

export function normalizeDataSourceFileUpdateState(
  updateType?: string,
  hasUpdate?: boolean,
): FileUpdateState {
  if (
    hasStatusText(updateType, [
      "unchanged",
      "no_change",
      "no change",
      "no_update",
      "no update",
      "not_updated",
      "not updated",
      "not_modified",
      "not modified",
      "none",
      "same",
    ])
  ) {
    return "unchanged";
  }
  if (hasStatusToken(updateType, ["delete", "deleted", "remove", "removed"])) {
    return "deleted";
  }
  if (
    hasStatusToken(updateType, [
      "new",
      "add",
      "added",
      "create",
      "created",
      "insert",
      "inserted",
    ])
  ) {
    return "new";
  }
  if (
    hasStatusToken(updateType, [
      "modify",
      "modified",
      "change",
      "changed",
      "update",
      "updated",
    ])
  ) {
    return "changed";
  }
  return hasUpdate ? "changed" : "unchanged";
}

function statusField(value: unknown, key: string) {
  if (typeof value !== "object" || value === null) {
    return "";
  }
  const field = (value as Record<string, unknown>)[key];
  return typeof field === "string" ? field : "";
}

function dataSourceFailureText(parseState?: string, lastError?: unknown) {
  if (!lastError) {
    return parseState || "";
  }
  if (typeof lastError === "string") {
    return [parseState, lastError].filter(Boolean).join(" ");
  }
  if (typeof lastError !== "object") {
    return [parseState, `${lastError}`].filter(Boolean).join(" ");
  }
  return [
    parseState,
    statusField(lastError, "phase"),
    statusField(lastError, "stage"),
    statusField(lastError, "code"),
    statusField(lastError, "reason"),
    statusField(lastError, "message"),
    statusField(lastError, "error"),
  ]
    .filter(Boolean)
    .join(" ");
}

function supportsDownloadParseStatus(sourceType?: SourceType | DataSourceKind) {
  return sourceType !== "local";
}

function normalizeDataSourceFailureStatus(
  parseState?: string,
  lastError?: unknown,
  options?: DataSourceParseStatusOptions,
): DetailParseStatus | undefined {
  const supportsDownloadStatus = supportsDownloadParseStatus(options?.sourceType);
  const phase = statusField(lastError, "phase");
  if (
    supportsDownloadStatus &&
    hasStatusToken(phase, ["download", "export", "fetch", "source"])
  ) {
    return "download_failed";
  }
  if (hasStatusToken(phase, ["parse", "index", "ingest", "core", "knowledge"])) {
    return "parse_failed";
  }

  const code = statusField(lastError, "code") || statusField(lastError, "reason");
  const text = dataSourceFailureText(parseState, lastError);
  if (
    hasStatusText(text, [
      "download_failed",
      "download failed",
      "export_failed",
      "export failed",
      "fetch_failed",
      "fetch failed",
      "transient_source_error",
      "unsupported_export",
      "auth_connection_invalid",
      "permission_denied",
    ]) ||
    hasStatusToken(text, ["download", "export"])
  ) {
    return supportsDownloadStatus ? "download_failed" : undefined;
  }
  if (
    hasStatusText(text, [
      "parse_failed",
      "parse failed",
      "core_task_failed",
      "core_submit_failed",
      "core_task_not_found",
      "index_failed",
      "index failed",
      "ingest_failed",
      "ingest failed",
    ]) ||
    hasStatusToken(code, ["parse", "core", "index", "ingest"])
  ) {
    return "parse_failed";
  }
  return undefined;
}

export function normalizeDataSourceParseStatus(
  parseState?: string,
  lastError?: unknown,
  options?: DataSourceParseStatusOptions,
): DetailParseStatus {
  if (hasStatusToken(parseState, ["cancel", "canceled", "cancelled"])) {
    return "canceled";
  }
  const failureStatus = normalizeDataSourceFailureStatus(
    parseState,
    lastError,
    options,
  );
  if (failureStatus) {
    return failureStatus;
  }
  if (
    hasStatusText(parseState, [
      "not_parsed",
      "not parsed",
      "unparsed",
      "pending_parse",
      "pending parse",
    ])
  ) {
    return "pending";
  }
  if (hasStatusToken(parseState, ["delete", "deleted", "remove", "removed"])) {
    return "deleted";
  }
  if (hasStatusToken(parseState, ["duplicate", "duplicated"])) {
    return "duplicate";
  }
  if (
    hasStatusToken(parseState, [
      "error",
      "errored",
      "fail",
      "failed",
      "failure",
      "invalid",
    ])
  ) {
    return "failed";
  }
  if (
    supportsDownloadParseStatus(options?.sourceType) &&
    (hasStatusToken(parseState, ["download", "downloading", "exporting", "fetching"]) ||
      (hasStatusToken(parseState, [
        "queued",
        "running",
        "pending",
        "waiting",
        "working",
        "processing",
      ]) &&
        !hasStatusToken(parseState, [
          "submitted",
          "parse",
          "parsing",
          "index",
          "indexing",
          "reindex",
          "reindexing",
        ])))
  ) {
    return "downloading";
  }
  if (
    hasStatusToken(parseState, [
      "reindex",
      "reindexing",
      "running",
      "pending",
      "waiting",
      "working",
      "queued",
      "processing",
      "parsing",
      "indexing",
    ])
  ) {
    return "reindexing";
  }
  if (
    hasStatusToken(parseState, [
      "parse",
      "parsed",
      "success",
      "succeeded",
      "complete",
      "completed",
      "done",
      "finished",
    ])
  ) {
    return "parsed";
  }
  return "failed";
}

export function isDataSourceUpdateState(updateType?: string, hasUpdate?: boolean) {
  return normalizeDataSourceFileUpdateState(updateType, hasUpdate) !== "unchanged";
}

export function getSourceTypeTitle(type: SourceType, t: TFunction) {
  if (type === "local") {
    return t("admin.dataSourceTypeLocal");
  }
  if (type === "feishu") {
    return t("admin.dataSourceTypeFeishu");
  }
  if (type === "notion") {
    return t("admin.dataSourceTypeNotion");
  }
  return type;
}

export function getSourceTypeDescription(type: SourceType, t: TFunction) {
  if (type === "local") {
    return t("admin.dataSourceTypeLocalDesc");
  }
  if (type === "feishu") {
    return t("admin.dataSourceTypeFeishuDesc");
  }
  if (type === "notion") {
    return t("admin.dataSourceTypeNotionDesc");
  }
  return "";
}

export function getStatusMeta(status: SourceStatus, t: TFunction) {
  if (status === "deleted") {
    return { color: "default", text: t("common.delete") };
  }
  if (status === "active") {
    return { color: "success", text: t("admin.dataSourceStatusActive") };
  }
  if (status === "expired") {
    return { color: "warning", text: t("admin.dataSourceStatusExpired") };
  }
  if (status === "error") {
    return { color: "error", text: t("admin.dataSourceStatusError") };
  }
  return { color: "default", text: t("admin.dataSourceStatusPaused") };
}

export function getConnectionMeta(state: ConnectionState | OAuthState, t: TFunction) {
  if (state === "connected") {
    return { color: "success", text: t("admin.dataSourceConnectionConnected") };
  }
  if (state === "waiting") {
    return { color: "processing", text: t("admin.dataSourceConnectionWaiting") };
  }
  if (state === "expired") {
    return { color: "warning", text: t("admin.dataSourceConnectionExpired") };
  }
  if (state === "error") {
    return { color: "error", text: t("admin.dataSourceConnectionError") };
  }
  return { color: "default", text: t("admin.dataSourceConnectionPending") };
}

export function getConflictPolicyLabel(policy: ConflictPolicy, t: TFunction) {
  return policy === "overwrite"
    ? t("admin.dataSourceConflictOverwrite")
    : policy === "skip"
      ? t("admin.dataSourceConflictSkip")
      : t("admin.dataSourceConflictVersioned");
}

export function getSyncModeLabel(mode: SyncMode, t: TFunction) {
  return mode === "manual"
    ? t("admin.dataSourceSyncModeManual")
    : t("admin.dataSourceSyncModeScheduled");
}

export function shouldSyncFileCandidate(state: FileUpdateState) {
  return state === "new" || state === "changed" || state === "deleted";
}

export function getFileUpdateMeta(state: FileUpdateState, t: TFunction) {
  if (state === "new") {
    return { color: "success", text: t("admin.dataSourceFileUpdateNew") };
  }
  if (state === "changed") {
    return { color: "processing", text: t("admin.dataSourceFileUpdateChanged") };
  }
  if (state === "deleted") {
    return { color: "error", text: t("admin.dataSourceFileUpdateDeleted") };
  }
  return { color: "default", text: t("admin.dataSourceFileUpdateUnchanged") };
}

export function getPendingUpdateCount(candidates: FileCandidate[]) {
  return candidates.filter((item) => shouldSyncFileCandidate(item.updateState)).length;
}
