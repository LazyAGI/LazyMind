import type { TFunction } from "i18next";

export type SourceType = "local" | "s3" | "feishu" | "confluence" | "notion";
export type SourceStatus = "active" | "expired" | "error" | "paused";
export type ConnectionState = "connected" | "expired" | "error" | "pending";
export type SyncMode = "manual" | "scheduled";
export type ConflictPolicy = "overwrite" | "skip" | "versioned";
export type FileSyncMode = "all" | "partial";
export type OAuthState = "pending" | "waiting" | "connected" | "expired" | "error";
export type FileUpdateState = "new" | "changed" | "unchanged" | "deleted";
export type FeishuTargetType = "wiki_space" | "drive_folder";
export type DetailParseStatus = "parsed" | "reindexing" | "duplicate" | "deleted" | "failed";
export type DataSourceKind = "local" | "feishu";

// New source state machine fields exposed by the backend.
export type SourceStateValue = "UNCHANGED" | "NEW" | "MODIFIED" | "DELETED";
export type SyncStateValue =
  | "IDLE"
  | "PENDING"
  | "SCHEDULED"
  | "RUNNING"
  | "FAILED";
export type PendingActionValue = "NONE" | "CREATE" | "UPDATE" | "DELETE";

export const DEFAULT_SCAN_TENANT_ID = "tenant-demo";
export const FEISHU_APP_SETUP_STORAGE_KEY = "lazymind:datasource:feishu:app-setup";
export const FEISHU_DEFAULT_SCOPES = [
  "offline_access",
  "drive:drive",
  "drive:drive:readonly",
  "drive:drive.metadata:readonly",
  "wiki:wiki",
  "wiki:wiki:readonly",
  "wiki:node:retrieve",
  "docx:document",
];
export const FEISHU_INCLUDE_PATTERNS = [
  "**/*.md",
  "**/*.doc",
  "**/*.docx",
  "**/*.pdf",
  "**/*.txt",
];
export const FEISHU_EXCLUDE_PATTERNS = ["**/~$*"];
export const FEISHU_MAX_OBJECT_SIZE_BYTES = 209715200;
export const CLOUD_SYNC_POLL_INTERVAL_MS = 2000;
export const CLOUD_SYNC_TIMEOUT_MS = 120000;

export interface PendingOAuthAttempt {
  timerId: number | null;
  previousState: OAuthState;
  previousVerified: boolean;
  previousConnection: any | null;
  resolved: boolean;
  accountId?: string;
  appId?: string;
}

export interface SyncLogItem {
  id: string;
  time: string;
  result: "success" | "warning" | "failed";
  title: string;
  description: string;
}

export interface FileCandidate {
  id: string;
  name: string;
  path: string;
  size: string;
  type: string;
  updateState: FileUpdateState;
}

export interface DetailDocumentItem {
  id: string;
  name: string;
  path: string;
  size: string;
  tags: string[];
  updateState: FileUpdateState;
  syncDetail: string;
  parseStatus: DetailParseStatus;
  sourceUpdatedAt: string;
  updatedAt: string;
}

export interface DataSourceItem {
  id: string;
  name: string;
  type: SourceType;
  knowledgeBase: string;
  description: string;
  target: string;
  syncMode: SyncMode;
  scheduleLabel: string;
  status: SourceStatus;
  connectionState: ConnectionState;
  lastSync: string;
  nextSync: string;
  documentCount: number;
  addCount: number;
  deleteCount: number;
  changeCount: number;
  permissions: string[];
  conflictPolicy: ConflictPolicy;
  enabled: boolean;
  scopeMode: FileSyncMode;
  selectedFiles: string[];
  fileCandidates: FileCandidate[];
  logs: SyncLogItem[];
  warning?: string;
  oauthConnection?: any | null;
  agentId?: string;
  tenantId?: string;
  scanManaged?: boolean;
  storageUsed?: string;
  detailDocuments?: DetailDocumentItem[];
  rootPath?: string;
  targetRef?: string;
  targetType?: FeishuTargetType;
  authConnectionId?: string;
  datasetId?: string;
}

export interface SourceFormValues {
  name?: string;
  knowledgeBase?: string;
  description?: string;
  enabled?: boolean;
  localMode?: "fs" | "mount" | "s3mirror";
  path?: string;
  mountName?: string;
  bucket?: string;
  region?: string;
  prefix?: string;
  target?: string;
  targetType?: FeishuTargetType;
  spaceKey?: string;
  scopes?: string[];
  syncMode?: SyncMode;
  scheduleCycle?: string;
  scheduleTime?: string;
  fileSyncMode?: FileSyncMode;
  selectedFiles?: string[];
  conflictPolicy?: ConflictPolicy;
  autoScan?: boolean;
  skipInternalAssets?: boolean;
}

export interface FeishuAppSetup {
  appId: string;
  appSecret: string;
}

export interface DataSourceSummary {
  id: string;
  name: string;
  target: string;
  rootPath?: string;
  targetRef?: string;
  targetType?: string;
  sourceType?: DataSourceKind;
  documentCount: number;
  status: SourceStatus;
  lastSync: string;
  addCount: number;
  deleteCount: number;
  changeCount: number;
  storageUsed?: string;
  documents?: DocumentStatusRow[];
  scanManaged?: boolean;
  tenantId?: string;
  agentId?: string;
}

export interface DataSourceDetailState {
  source?: DataSourceSummary;
}

export interface DocumentStatusRow {
  id: string;
  name: string;
  path: string;
  size: string;
  tags: string[];
  updateState: FileUpdateState;
  syncDetail: string;
  parseStatus: DetailParseStatus;
  sourceUpdatedAt: string;
  updatedAt: string;
  // New state machine fields. Optional so older backends keep working.
  sourceState?: SourceStateValue;
  syncState?: SyncStateValue;
  pendingAction?: PendingActionValue;
  nextSyncAt?: string;
  lastError?: string;
  knowledgeBasePresent?: boolean;
}

export function isCloudType(type?: SourceType) {
  return type === "feishu";
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

export function normalizeDataSourceParseStatus(parseState?: string): DetailParseStatus {
  if (
    hasStatusText(parseState, [
      "not_parsed",
      "not parsed",
      "unparsed",
      "pending_parse",
      "pending parse",
    ])
  ) {
    return "reindexing";
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
    hasStatusToken(parseState, [
      "reindex",
      "reindexing",
      "running",
      "pending",
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
  return type;
}

export function getSourceTypeDescription(type: SourceType, t: TFunction) {
  if (type === "local") {
    return t("admin.dataSourceTypeLocalDesc");
  }
  if (type === "feishu") {
    return t("admin.dataSourceTypeFeishuDesc");
  }
  return "";
}

export function getStatusMeta(status: SourceStatus, t: TFunction) {
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

export function formatDateTime(value?: string) {
  if (!value) {
    return "-";
  }

  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  const year = parsed.getFullYear();
  const month = `${parsed.getMonth() + 1}`.padStart(2, "0");
  const day = `${parsed.getDate()}`.padStart(2, "0");
  const hour = `${parsed.getHours()}`.padStart(2, "0");
  const minute = `${parsed.getMinutes()}`.padStart(2, "0");
  return `${year}-${month}-${day} ${hour}:${minute}`;
}

export function formatBytes(bytes?: number) {
  if (!bytes || bytes < 0) {
    return "0 B";
  }

  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }

  return `${value.toFixed(value >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

// Source/sync state helpers below.

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

export interface DocumentLikeStatus {
  source_state?: string;
  sync_state?: string;
  pending_action?: string;
  next_sync_at?: string;
  last_error?: string;
  knowledge_base_present?: boolean;
  update_type?: string;
  has_update?: boolean;
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

export interface SourceStateMeta {
  color: string;
  text: string;
  tone: "new" | "changed" | "deleted" | "unchanged";
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

export interface SyncStateMeta {
  color: string;
  text: string;
}

export interface SyncStateMetaOptions {
  nextSyncAt?: string;
  lastError?: string;
  knowledgeBasePresent?: boolean;
  sourceState?: SourceStateValue;
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
// "源端删除，知识库中仍保留，等待同步删除". Used in tooltips/secondary text.
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
