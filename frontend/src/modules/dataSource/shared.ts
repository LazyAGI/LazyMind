import type { TFunction } from "i18next";

export type SourceType = "local" | "s3" | "feishu" | "confluence" | "notion";
export type SourceStatus = "active" | "expired" | "error" | "paused" | "deleted";
export type ConnectionState = "connected" | "expired" | "error" | "pending";
export type SyncMode = "manual" | "scheduled";
export type ConflictPolicy = "overwrite" | "skip" | "versioned";
export type FileSyncMode = "all" | "partial";
export type OAuthState = "pending" | "waiting" | "connected" | "expired" | "error";
export type FileUpdateState = "new" | "changed" | "unchanged" | "deleted";
export type FeishuTargetType = "wiki_space" | "drive_folder";
export type NotionTargetType = "page" | "database";
export type CloudTargetType = FeishuTargetType | NotionTargetType;
export type DetailParseStatus =
  | "parsed"
  | "pending"
  | "downloading"
  | "reindexing"
  | "duplicate"
  | "deleted"
  | "download_failed"
  | "parse_failed"
  | "canceled"
  | "failed";
export type DataSourceKind = "local" | "feishu" | "notion";
export type DataSourceFileType =
  | "pdf"
  | "doc"
  | "docx"
  | "hwp"
  | "ppt"
  | "pptx"
  | "pptm"
  | "jpg"
  | "jpeg"
  | "png"
  | "gif"
  | "bmp"
  | "webp"
  | "tiff"
  | "tif"
  | "ipynb"
  | "epub"
  | "md"
  | "mbox"
  | "csv"
  | "xls"
  | "xlsx"
  | "mp3"
  | "mp4"
  | "txt"
  | "xml"
  | "json"
  | "jsonl"
  | "yaml"
  | "yml"
  | "html"
  | "htm"
  | "py";

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
export const NOTION_APP_SETUP_STORAGE_KEY = "lazymind:datasource:notion:app-setup";
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
export const FEISHU_EXCLUDE_PATTERNS = ["**/~$*"];
export const DATA_SOURCE_FILE_TYPE_OPTIONS: Array<{
  value: DataSourceFileType;
  extensions: string[];
  i18nKey: string;
}> = [
  {
    value: "pdf",
    extensions: ["pdf"],
    i18nKey: "admin.dataSourceFileTypePdf",
  },
  {
    value: "doc",
    extensions: ["doc"],
    i18nKey: "admin.dataSourceFileTypeDoc",
  },
  {
    value: "docx",
    extensions: ["docx"],
    i18nKey: "admin.dataSourceFileTypeDocx",
  },
  {
    value: "hwp",
    extensions: ["hwp"],
    i18nKey: "admin.dataSourceFileTypeHwp",
  },
  {
    value: "ppt",
    extensions: ["ppt"],
    i18nKey: "admin.dataSourceFileTypePpt",
  },
  {
    value: "pptx",
    extensions: ["pptx"],
    i18nKey: "admin.dataSourceFileTypePptx",
  },
  {
    value: "pptm",
    extensions: ["pptm"],
    i18nKey: "admin.dataSourceFileTypePptm",
  },
  {
    value: "jpg",
    extensions: ["jpg"],
    i18nKey: "admin.dataSourceFileTypeJpg",
  },
  {
    value: "jpeg",
    extensions: ["jpeg"],
    i18nKey: "admin.dataSourceFileTypeJpeg",
  },
  {
    value: "png",
    extensions: ["png"],
    i18nKey: "admin.dataSourceFileTypePng",
  },
  {
    value: "gif",
    extensions: ["gif"],
    i18nKey: "admin.dataSourceFileTypeGif",
  },
  {
    value: "bmp",
    extensions: ["bmp"],
    i18nKey: "admin.dataSourceFileTypeBmp",
  },
  {
    value: "webp",
    extensions: ["webp"],
    i18nKey: "admin.dataSourceFileTypeWebp",
  },
  {
    value: "tiff",
    extensions: ["tiff"],
    i18nKey: "admin.dataSourceFileTypeTiff",
  },
  {
    value: "tif",
    extensions: ["tif"],
    i18nKey: "admin.dataSourceFileTypeTif",
  },
  {
    value: "ipynb",
    extensions: ["ipynb"],
    i18nKey: "admin.dataSourceFileTypeIpynb",
  },
  {
    value: "epub",
    extensions: ["epub"],
    i18nKey: "admin.dataSourceFileTypeEpub",
  },
  {
    value: "md",
    extensions: ["md"],
    i18nKey: "admin.dataSourceFileTypeMd",
  },
  {
    value: "mbox",
    extensions: ["mbox"],
    i18nKey: "admin.dataSourceFileTypeMbox",
  },
  {
    value: "csv",
    extensions: ["csv"],
    i18nKey: "admin.dataSourceFileTypeCsv",
  },
  {
    value: "xls",
    extensions: ["xls"],
    i18nKey: "admin.dataSourceFileTypeXls",
  },
  {
    value: "xlsx",
    extensions: ["xlsx"],
    i18nKey: "admin.dataSourceFileTypeXlsx",
  },
  {
    value: "mp3",
    extensions: ["mp3"],
    i18nKey: "admin.dataSourceFileTypeMp3",
  },
  {
    value: "mp4",
    extensions: ["mp4"],
    i18nKey: "admin.dataSourceFileTypeMp4",
  },
  {
    value: "txt",
    extensions: ["txt"],
    i18nKey: "admin.dataSourceFileTypeTxt",
  },
  {
    value: "xml",
    extensions: ["xml"],
    i18nKey: "admin.dataSourceFileTypeXml",
  },
  {
    value: "json",
    extensions: ["json"],
    i18nKey: "admin.dataSourceFileTypeJson",
  },
  {
    value: "jsonl",
    extensions: ["jsonl"],
    i18nKey: "admin.dataSourceFileTypeJsonl",
  },
  {
    value: "yaml",
    extensions: ["yaml"],
    i18nKey: "admin.dataSourceFileTypeYaml",
  },
  {
    value: "yml",
    extensions: ["yml"],
    i18nKey: "admin.dataSourceFileTypeYml",
  },
  {
    value: "html",
    extensions: ["html"],
    i18nKey: "admin.dataSourceFileTypeHtml",
  },
  {
    value: "htm",
    extensions: ["htm"],
    i18nKey: "admin.dataSourceFileTypeHtm",
  },
  {
    value: "py",
    extensions: ["py"],
    i18nKey: "admin.dataSourceFileTypePy",
  },
];
export const DEFAULT_DATA_SOURCE_FILE_TYPES: DataSourceFileType[] = [
  "pdf",
  "doc",
  "docx",
  "xls",
  "xlsx",
  "csv",
];
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
  fileTypes?: DataSourceFileType[];
  fileCandidates: FileCandidate[];
  logs: SyncLogItem[];
  warning?: string;
  oauthConnection?: any | null;
  agentId?: string;
  tenantId?: string;
  scanManaged?: boolean;
  storageUsed?: string;
  parsedDocumentCount?: number;
  detailDocuments?: DetailDocumentItem[];
  rootPath?: string;
  targetRef?: string;
  targetRefs?: string[];
  targetType?: CloudTargetType;
  targetTypes?: Record<string, CloudTargetType>;
  authConnectionId?: string;
  datasetId?: string;
  bindingId?: string;
  bindingIds?: string[];
  bindingTreeKey?: string;
  bindingTreeKeys?: string[];
  configVersion?: number;
}

export interface SourceFormValues {
  name?: string;
  knowledgeBase?: string;
  description?: string;
  enabled?: boolean;
  localMode?: "fs" | "mount" | "s3mirror";
  path?: string | string[];
  mountName?: string;
  bucket?: string;
  region?: string;
  prefix?: string;
  target?: string | string[];
  targetType?: CloudTargetType;
  fileTypes?: DataSourceFileType[];
  spaceKey?: string;
  scopes?: string[];
  syncMode?: SyncMode;
  scheduleCycle?: string;
  scheduleWeekdays?: string[];
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
  targetTypes?: Record<string, string>;
  sourceType?: DataSourceKind;
  documentCount: number;
  status: SourceStatus;
  lastSync: string;
  addCount: number;
  deleteCount: number;
  changeCount: number;
  storageUsed?: string;
  parsedDocumentCount?: number;
  documents?: DocumentStatusRow[];
  scanManaged?: boolean;
  tenantId?: string;
  agentId?: string;
  bindingId?: string;
  bindingTreeKey?: string;
  configVersion?: number;
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

export interface DataSourceParseStatusOptions {
  sourceType?: SourceType | DataSourceKind;
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

export function resolveStorageUsed(
  summary?: Record<string, any>,
  fallback?: string,
) {
  const bytes =
    summary?.storage_bytes ??
    summary?.storageBytes ??
    summary?.storage_used_bytes ??
    summary?.storageUsedBytes;

  if (typeof bytes === "number") {
    return formatBytes(bytes);
  }

  const parsedBytes =
    typeof bytes === "string" && bytes.trim() ? Number(bytes) : Number.NaN;
  if (Number.isFinite(parsedBytes)) {
    return formatBytes(parsedBytes);
  }

  return fallback || "0 B";
}

export function resolveParsedDocumentCount(
  summary?: Record<string, any>,
  fallback = 0,
) {
  const value =
    summary?.parsed_document_count ??
    summary?.parsedDocumentCount;
  const parsed =
    typeof value === "number"
      ? value
      : typeof value === "string" && value.trim()
        ? Number(value)
        : Number.NaN;

  if (Number.isFinite(parsed)) {
    return Math.max(0, Math.trunc(parsed));
  }
  return fallback;
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
