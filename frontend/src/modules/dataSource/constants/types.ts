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

export interface DataSourceParseStatusOptions {
  sourceType?: SourceType | DataSourceKind;
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

export interface SourceStateMeta {
  color: string;
  text: string;
  tone: "new" | "changed" | "deleted" | "unchanged";
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
