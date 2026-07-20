import {
  Configuration as CoreConfiguration,
  DefaultApiFactory,
  SkillDiffApiFactory,
  SkillDraftsApiFactory,
  SkillFsApiFactory,
  SkillMarketApiFactory,
  SkillRevisionsApiFactory,
  SkillSharesApiFactory,
  SkillsApiFactory,
  type DiffEntryLineOpenAPIResponse,
  type DiffFileOpenAPIResponse,
  type DiffTreeOpenAPIResponse,
  type MarketItemOpenAPIResponse,
  type MarketListOpenAPIResponse,
  type SkillCreateManagedOpenAPIRequest,
  type SkillDetailOpenAPIResponse,
  type SkillDraftStatusOpenAPIResponse,
  type SkillFileOpenAPIResponse,
  type SkillListItemOpenAPIResponse,
  type SkillOrganizeOpenAPIResponse,
  type SkillRevisionOpenAPIResponse,
  type SkillShareDetailOpenAPIResponse,
  type SkillShareListItemOpenAPIResponse,
  type SkillShareTargetItemOpenAPIResponse,
  type SkillTreeNodeOpenAPIResponse,
  type SkillUpdateManagedOpenAPIRequest,
} from "@/api/generated/core-client";
import {
  axiosInstance,
  BASE_URL,
  localizeErrorCode,
} from "@/components/request";
import { mapDiffEntryLines } from "./components/skillPackage/skillDiffUtils";
import type { DiffLine } from "./shared";

const coreConfig = new CoreConfiguration({ basePath: BASE_URL });
const skillsApi = SkillsApiFactory(coreConfig, BASE_URL, axiosInstance);
const defaultCoreApi = DefaultApiFactory(coreConfig, BASE_URL, axiosInstance);
const skillDraftsApi = SkillDraftsApiFactory(coreConfig, BASE_URL, axiosInstance);
const skillFsApi = SkillFsApiFactory(coreConfig, BASE_URL, axiosInstance);
const skillRevisionsApi = SkillRevisionsApiFactory(coreConfig, BASE_URL, axiosInstance);
const skillSharesApi = SkillSharesApiFactory(coreConfig, BASE_URL, axiosInstance);
const skillMarketApi = SkillMarketApiFactory(coreConfig, BASE_URL, axiosInstance);
const skillDiffApi = SkillDiffApiFactory(coreConfig, BASE_URL, axiosInstance);

const coreBasePath = `${BASE_URL}/api/core`;

const SKILL_MD_PATH = "SKILL.md";

const createSkillOrganizeRequestId = () => {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `org_${crypto.randomUUID()}`;
  }
  return `org_${Date.now()}_${Math.random().toString(36).slice(2)}`;
};

interface ApiEnvelope<T> {
  code?: number;
  message?: string;
  data?: T;
}

type WrappedPayload<T> = T | ApiEnvelope<T>;

const unwrapEnvelope = <T>(payload: WrappedPayload<T>): T => {
  if (payload && typeof payload === "object" && "data" in payload) {
    const envelope = payload as ApiEnvelope<T>;
    if (envelope.data !== undefined) {
      return envelope.data;
    }
  }
  return payload as T;
};

const toStringArray = (value: unknown): string[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => (typeof item === "string" ? item.trim() : ""))
    .filter(Boolean);
};

export interface SkillDraftSummary {
  hasUncommittedDraft: boolean;
  taskId: string;
  version: number;
}

export interface SkillAssetRecord {
  id: string;
  skillId: string;
  name: string;
  skillName: string;
  description: string;
  category: string;
  tags: string[];
  content: string;
  headRevisionId: string;
  draft: SkillDraftSummary;
  autoEvo: boolean;
  isEnabled: boolean;
  deletedAt?: string;
  deletedBy?: string;
}

export interface SkillDraftGeneratePayload {
  userInstruct: string;
  suggestionIds?: string[];
}

export interface SkillDraftPreviewRecord {
  currentContent: string;
  diff: string;
  draftContent: string;
  draftSourceVersion: number;
  draftStatus: string;
  outdated: boolean;
  skillId: string;
  reviewStatus: string;
  diffLines: DiffLine[];
}

export interface ListSkillOptions {
  keyword?: string;
  category?: string;
  tags?: string[];
  page?: number;
  pageSize?: number;
}

export interface SkillAssetListResult {
  records: SkillAssetRecord[];
  total: number;
  page: number;
  pageSize: number;
}

export interface SkillOrganizeRunRecord {
  requestId: string;
  status: string;
  taskId: string;
}

export interface ShareSkillPayload {
  targetUserIds: string[];
  targetGroupIds?: string[];
  message?: string;
}

export interface SkillUpdatePayloadSource {
  name?: string;
  description?: string;
  category?: string;
  tags?: string[];
  autoEvo?: boolean;
  isEnabled?: boolean;
}

export type SkillShareStatus =
  | "pending"
  | "accepted"
  | "rejected"
  | "failed"
  | "unknown";

export interface SkillSharePrincipal {
  id: string;
  name: string;
  type: "user" | "group";
}

export interface SkillShareRecord {
  id: string;
  skillId: string;
  sourceSkillId: string;
  skillName: string;
  skillDescription: string;
  skillContent?: string;
  category: string;
  tags: string[];
  message: string;
  status: SkillShareStatus;
  rawStatus: string;
  errorMessage?: string;
  sender: SkillSharePrincipal | null;
  recipients: SkillSharePrincipal[];
  createdAt?: string;
  updatedAt?: string;
  decidedAt?: string;
}

export interface ResourceUpdateTaskRecord {
  id: string;
  taskType: string;
  resourceType: string;
  triggerType: string;
  triggerId: string;
  status: string;
  errorCode: string;
  errorMessage: string;
  createdAt: string;
  updatedAt: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface SkillReviewSummaryRecord {
  qualifiedSessionCount: number;
  userTurnCount: number;
  toolCallCount: number;
  minUserTurns: number;
  minToolTurns: number;
  quantityThreshold: number;
  windowStart: string;
  windowEnd: string;
  runningTask?: ResourceUpdateTaskRecord;
  runningRequestId: string;
}

export interface SkillReviewRunRecord {
  task: ResourceUpdateTaskRecord | null;
  summary: SkillReviewSummaryRecord;
  requestId: string;
}

export interface SkillReviewTaskStatusRecord {
  task: ResourceUpdateTaskRecord | null;
  requestId: string;
  status: string;
  runStatus: string;
  resultCount: number;
}

export interface SkillReviewTaskListResult {
  records: SkillReviewTaskStatusRecord[];
  total: number;
  page: number;
  pageSize: number;
}

export interface ListSkillReviewTaskOptions {
  status?: string;
  requestId?: string;
  page?: number;
  pageSize?: number;
}

export interface SkillReviewResultRecord {
  id: string;
  skillName: string;
  type: string;
  reviewStatus: string;
  requestId: string;
  summary: string;
  time: string;
}

export interface SkillRevisionRecord {
  id: string;
  revisionId: string;
  revisionNo: number;
  skillId: string;
  changeSource: string;
  message: string;
  createdAt: string;
  createdBy: string;
  parentRevisionId: string;
  treeHash: string;
  isHead: boolean;
}

export interface SkillDraftStatusRecord {
  baseRevisionId: string;
  conversationId: string;
  draftVersion: number;
  hasUncommittedDraft: boolean;
  overlayCount: number;
  taskId: string;
}

export const hasSkillDraftChanges = (status: SkillDraftStatusRecord): boolean =>
  status.hasUncommittedDraft || status.overlayCount > 0;

export const isSkillAgentDraftContext = (status: SkillDraftStatusRecord): boolean =>
  hasSkillDraftChanges(status) && Boolean(status.taskId || status.conversationId);

const emptySkillFileDiff = (path: string): SkillDiffFileRecord => ({
  path,
  status: "unchanged",
  binary: false,
  type: "file",
  tooLarge: false,
  diffEntryLines: [],
});

export async function probeSkillAgentReviewMode(
  skillId: string,
  status: SkillDraftStatusRecord,
  changedPaths: string[],
): Promise<boolean> {
  if (!isSkillAgentDraftContext(status)) {
    return false;
  }
  const firstChanged = changedPaths[0];
  if (!firstChanged) {
    return false;
  }
  const fileDiff = await compareSkillFileDiff(skillId, firstChanged).catch(() => null);
  return Boolean(fileDiff?.review?.reviewId);
}

export interface SkillDraftReviewMeta {
  reviewId: string;
  reviewVersion: number;
  canUndo: boolean;
  pendingCount?: number;
  acceptedCount?: number;
  rejectedCount?: number;
}

export type SkillDraftReviewDecision = "accept" | "reject";

const mapSkillDraftReviewDecisionToApi = (
  decision: SkillDraftReviewDecision,
): "accepted" | "rejected" => (decision === "accept" ? "accepted" : "rejected");

export interface SkillDraftReviewActionItem {
  hunkId: string;
  decision: SkillDraftReviewDecision;
  path?: string;
}

export interface SkillDraftReviewMutationResult {
  reviewVersion: number;
  canUndo: boolean;
  pendingCount?: number;
  acceptedCount?: number;
  rejectedCount?: number;
}

export interface SkillDiffFileRecord {
  path: string;
  status: string;
  binary: boolean;
  type: string;
  tooLarge: boolean;
  diffEntryLines: DiffEntryLineOpenAPIResponse[];
  review?: SkillDraftReviewMeta;
}

export interface SkillDiffTreeRecord {
  cacheWritten: boolean;
  files: SkillDiffFileRecord[];
}

export interface SkillTreeNodeRecord {
  name: string;
  path: string;
  type: "file" | "dir";
  fileType: string;
  mime: string;
  size: number;
  binary: boolean;
  blobHash: string;
  children: SkillTreeNodeRecord[];
}

export interface CreateSkillPayload {
  name: string;
  description?: string;
  category: string;
  tags?: string[];
  autoEvo?: boolean;
  isEnabled?: boolean;
  source:
    | { type: "uploaded_zip"; uploadId: string }
    | { type: "url"; url: string };
}

export interface PublishSkillToMarketPayload {
  name: string;
  category: string;
  source:
    | { type: "uploaded_zip"; uploadId: string }
    | { type: "url"; url: string };
}

export interface MarketSkillRecord extends SkillAssetRecord {
  marketItemId: string;
  sourceSkillId: string;
  marketSource: "builtin" | "admin";
  marketStatus?: string;
  installed?: boolean;
  installedSkillId?: string;
}

export interface MarketSkillListResult {
  records: MarketSkillRecord[];
  total: number;
  page: number;
  pageSize: number;
}

interface BuiltinSkillListItem {
  builtin_skill_uid: string;
  name: string;
  description: string;
  category: string;
  content: string;
  installed?: boolean;
  installed_skill_id?: string;
}

const normalizeDraftSummary = (
  draft: SkillDetailOpenAPIResponse["draft"] | undefined,
): SkillDraftSummary => ({
  hasUncommittedDraft: Boolean(draft?.has_uncommitted_draft),
  taskId: draft?.task_id || "",
  version: draft?.version ?? 0,
});

const normalizeMarketItem = (item: MarketItemOpenAPIResponse): MarketSkillRecord => {
  const source = item.source;
  const base = source
    ? normalizeSkillItem(source)
    : {
        id: item.market_item_id || item.id || "",
        skillId: item.source_skill_id || "",
        name: "",
        skillName: "",
        description: "",
        category: "",
        tags: [] as string[],
        content: "",
        headRevisionId: "",
        draft: { hasUncommittedDraft: false, taskId: "", version: 0 },
        autoEvo: false,
        isEnabled: true,
      };

  return {
    ...base,
    id: item.market_item_id || item.id || base.id,
    marketItemId: item.market_item_id || item.id || "",
    sourceSkillId: item.source_skill_id || base.skillId,
    marketSource: "admin",
    marketStatus: item.status,
    installed: Boolean(item.installed),
    installedSkillId: item.installed_skill_id || "",
  };
};

const normalizeSkillItem = (
  item: SkillListItemOpenAPIResponse | SkillDetailOpenAPIResponse,
  content = "",
): SkillAssetRecord => {
  const skillId = item.skill_id || item.id;
  const name = item.name || item.skill_name || skillId;

  return {
    id: item.id || skillId,
    skillId,
    name,
    skillName: item.skill_name || name,
    description: item.description || "",
    category: item.category || "",
    tags: toStringArray(item.tags),
    content: content || item.file_content || "",
    headRevisionId: item.head_revision_id || "",
    draft: normalizeDraftSummary(item.draft),
    autoEvo: toBoolean(item.auto_evo, false),
    isEnabled: toBoolean(item.is_enabled, true),
    deletedAt:
      typeof (item as { deleted_at?: unknown }).deleted_at === "string"
        ? (item as { deleted_at?: string }).deleted_at
        : undefined,
    deletedBy:
      typeof (item as { deleted_by?: unknown }).deleted_by === "string"
        ? (item as { deleted_by?: string }).deleted_by
        : undefined,
  };
};

const normalizeSkillShareStatus = (value: string): SkillShareStatus => {
  const normalized = value.trim().toLowerCase();
  if (!normalized) {
    return "pending";
  }
  if (normalized.includes("pending") || normalized.includes("wait")) {
    return "pending";
  }
  if (normalized.includes("fail") || normalized.includes("error")) {
    return "failed";
  }
  if (normalized.includes("accept") || normalized.includes("complete")) {
    return "accepted";
  }
  if (normalized.includes("reject") || normalized.includes("declin")) {
    return "rejected";
  }
  return "unknown";
};

const normalizeOutgoingShare = (
  item: SkillShareListItemOpenAPIResponse,
): SkillShareRecord => ({
  id: item.share_item_id,
  skillId: item.source_skill_id,
  sourceSkillId: item.source_skill_id,
  skillName: item.source_category || item.source_skill_id,
  skillDescription: "",
  skillContent: "",
  category: item.source_category || "",
  tags: [],
  message: item.message || "",
  status: normalizeSkillShareStatus(item.status),
  rawStatus: item.status,
  errorMessage: item.error_message
    ? localizeErrorCode("2000509")
    : undefined,
  sender: {
    id: item.source_user_id,
    name: item.source_user_name || item.source_user_id,
    type: "user",
  },
  recipients: [
    {
      id: item.target_user_id,
      name: item.target_user_name || item.target_user_id,
      type: "user",
    },
  ],
  createdAt: item.created_at,
  updatedAt: item.updated_at,
  decidedAt: item.accepted_at || item.rejected_at,
});

const normalizeIncomingShare = (
  item: SkillShareListItemOpenAPIResponse,
): SkillShareRecord => normalizeOutgoingShare(item);

const normalizeShareTarget = (
  item: SkillShareTargetItemOpenAPIResponse,
  skillId: string,
): SkillShareRecord => ({
  id: item.share_item_id,
  skillId,
  sourceSkillId: skillId,
  skillName: skillId,
  skillDescription: "",
  skillContent: "",
  category: "",
  tags: [],
  message: item.message || "",
  status: normalizeSkillShareStatus(item.status),
  rawStatus: item.status,
  errorMessage: item.error_message
    ? localizeErrorCode("2000509")
    : undefined,
  sender: null,
  recipients: [
    {
      id: item.target_user_id,
      name: item.target_user_name || item.target_user_id,
      type: "user",
    },
  ],
  createdAt: item.shared_at,
  updatedAt: item.updated_at,
  decidedAt: item.accepted_at || item.rejected_at,
});

const normalizeShareDetail = (
  payload: SkillShareDetailOpenAPIResponse,
): SkillShareRecord => {
  const source = payload.source;
  const skill = source ? normalizeSkillItem(source) : null;

  return {
    id: payload.share_item_id,
    skillId: skill?.id || "",
    sourceSkillId: skill?.id || "",
    skillName: skill?.name || "",
    skillDescription: skill?.description || "",
    skillContent: skill?.content || "",
    category: skill?.category || "",
    tags: skill?.tags || [],
    message: payload.message || "",
    status: normalizeSkillShareStatus(payload.status),
    rawStatus: payload.status,
    sender: null,
    recipients: [],
  };
};

const normalizeRevision = (item: SkillRevisionOpenAPIResponse): SkillRevisionRecord => ({
  id: item.id || item.revision_id,
  revisionId: item.revision_id || item.id,
  revisionNo: item.revision_no,
  skillId: item.skill_id,
  changeSource: item.change_source || "",
  message: item.message || "",
  createdAt: item.created_at || "",
  createdBy: item.created_by || "",
  parentRevisionId: item.parent_revision_id || "",
  treeHash: item.tree_hash || "",
  isHead: Boolean(item.is_head),
});

const normalizeTreeNode = (node: SkillTreeNodeOpenAPIResponse): SkillTreeNodeRecord => ({
  name: node.name || "",
  path: node.path || "",
  type: node.type === "dir" ? "dir" : "file",
  fileType: node.file_type || "",
  mime: node.mime || "",
  size: node.size ?? 0,
  binary: Boolean(node.binary),
  blobHash: node.blob_hash || "",
  children: (node.children || []).map(normalizeTreeNode),
});

const normalizeDraftStatus = (
  payload: SkillDraftStatusOpenAPIResponse,
): SkillDraftStatusRecord => ({
  baseRevisionId: payload.base_revision_id || "",
  conversationId: payload.conversation_id || "",
  draftVersion: payload.draft_version ?? 0,
  hasUncommittedDraft: Boolean(payload.has_uncommitted_draft),
  overlayCount: payload.overlay_count ?? 0,
  taskId: payload.task_id || "",
});

const readSkillFileContent = async (
  skillId: string,
  path = SKILL_MD_PATH,
): Promise<string> => {
  const response = await skillsApi.apiCoreSkillsSkillIdFileGet({ skillId, path });
  const payload = unwrapEnvelope<SkillFileOpenAPIResponse>(response.data);
  return payload.content || "";
};

export const buildSkillUpdatePayload = (
  skill: SkillUpdatePayloadSource,
): SkillUpdateManagedOpenAPIRequest => ({
  auto_evo: skill.autoEvo,
  category: skill.category,
  description: skill.description,
  is_enabled: skill.isEnabled,
  name: skill.name,
  tags: skill.tags,
});

type RawObject = Record<string, unknown>;

const toRawObject = (value: unknown): RawObject | null => {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as RawObject;
};

const toStringValue = (value: unknown, fallback = ""): string => {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number") {
    return String(value);
  }
  return fallback;
};

const toNumberValue = (value: unknown, fallback = 0): number => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return fallback;
};

const toBoolean = (value: unknown, fallback = false): boolean => {
  if (typeof value === "boolean") {
    return value;
  }

  if (typeof value === "number") {
    return value !== 0;
  }

  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (normalized === "true" || normalized === "1" || normalized === "yes") {
      return true;
    }
    if (normalized === "false" || normalized === "0" || normalized === "no") {
      return false;
    }
  }

  return fallback;
};

const normalizeResourceUpdateTask = (value: unknown): ResourceUpdateTaskRecord | null => {
  const raw = toRawObject(value);
  const id = toStringValue(raw?.id, "");

  if (!id) {
    return null;
  }

  return {
    id,
    taskType: toStringValue(raw?.task_type, ""),
    resourceType: toStringValue(raw?.resource_type, ""),
    triggerType: toStringValue(raw?.trigger_type, ""),
    triggerId: toStringValue(raw?.trigger_id, ""),
    status: toStringValue(raw?.status, ""),
    errorCode: toStringValue(raw?.error_code, ""),
    errorMessage: raw?.error_message
      ? localizeErrorCode(
          toStringValue(raw?.error_code, ""),
          localizeErrorCode("2000509"),
        )
      : "",
    createdAt: toStringValue(raw?.created_at, ""),
    updatedAt: toStringValue(raw?.updated_at, ""),
    startedAt: toStringValue(raw?.started_at, "") || undefined,
    finishedAt: toStringValue(raw?.finished_at, "") || undefined,
  };
};

const normalizeSkillReviewSummary = (payload: unknown): SkillReviewSummaryRecord => {
  const raw = toRawObject(payload);
  return {
    qualifiedSessionCount: toNumberValue(raw?.qualified_session_count, 0),
    userTurnCount: toNumberValue(raw?.user_turn_count, 0),
    toolCallCount: toNumberValue(raw?.tool_call_count, 0),
    minUserTurns: toNumberValue(raw?.min_user_turns, 0),
    minToolTurns: toNumberValue(raw?.min_tool_turns, 0),
    quantityThreshold: toNumberValue(raw?.quantity_threshold, 0),
    windowStart: toStringValue(raw?.window_start, ""),
    windowEnd: toStringValue(raw?.window_end, ""),
    runningTask: normalizeResourceUpdateTask(raw?.running_task) || undefined,
    runningRequestId: toStringValue(raw?.running_requestid, ""),
  };
};

const normalizeSkillReviewTaskStatus = (
  payload: unknown,
): SkillReviewTaskStatusRecord | null => {
  const raw = toRawObject(payload);
  if (!raw) {
    return null;
  }

  const task = normalizeResourceUpdateTask(raw?.task);
  const requestId = toStringValue(raw?.requestid, "");
  const status = toStringValue(raw?.status, "");
  if (!task && !requestId && !status) {
    return null;
  }

  return {
    task,
    requestId,
    status,
    runStatus: toStringValue(raw?.run_status, ""),
    resultCount: toNumberValue(raw?.result_count, 0),
  };
};

const normalizeSkillReviewResult = (value: unknown): SkillReviewResultRecord | null => {
  const raw = toRawObject(value);
  const id = toStringValue(raw?.id, "");

  if (!id) {
    return null;
  }

  return {
    id,
    skillName: toStringValue(raw?.skill_name, ""),
    type: toStringValue(raw?.type, ""),
    reviewStatus: toStringValue(raw?.review_status, ""),
    requestId: toStringValue(raw?.requestid, ""),
    summary: toStringValue(raw?.summary, ""),
    time: toStringValue(raw?.time, ""),
  };
};

export async function getSkillReviewSummary(): Promise<SkillReviewSummaryRecord> {
  const response = await axiosInstance.get(`${coreBasePath}/skill-review:summary`);
  const payload = unwrapEnvelope<unknown>(response.data);
  return normalizeSkillReviewSummary(payload);
}

export async function runSkillReview(): Promise<SkillReviewRunRecord> {
  const response = await axiosInstance.post(`${coreBasePath}/skill-review:run`);
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);
  return {
    task: normalizeResourceUpdateTask(raw?.task),
    summary: normalizeSkillReviewSummary(raw?.summary),
    requestId: toStringValue(raw?.requestid, ""),
  };
}

export async function getResourceUpdateTask(
  taskId: string,
): Promise<ResourceUpdateTaskRecord | null> {
  const response = await axiosInstance.get(
    `${coreBasePath}/evolution/tasks/${encodeURIComponent(taskId)}`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  return normalizeResourceUpdateTask(payload);
}

export async function listSkillReviewTasks(
  options: ListSkillReviewTaskOptions = {},
): Promise<SkillReviewTaskListResult> {
  const response = await axiosInstance.get(`${coreBasePath}/skill-review/tasks`, {
    params: {
      status: options.status,
      requestid: options.requestId,
      page: options.page ?? 1,
      page_size: options.pageSize ?? 20,
    },
  });
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);
  const items = Array.isArray(raw?.items) ? raw.items : [];
  return {
    records: items
      .map((item) => normalizeSkillReviewTaskStatus(item))
      .filter((item): item is SkillReviewTaskStatusRecord => Boolean(item)),
    total: toNumberValue(raw?.total, items.length),
    page: toNumberValue(raw?.page, options.page ?? 1),
    pageSize: toNumberValue(raw?.page_size ?? raw?.pageSize, options.pageSize ?? 20),
  };
}

export async function listSkillReviewResultsByRequest(
  requestId: string,
): Promise<SkillReviewResultRecord[]> {
  if (!requestId.trim()) {
    return [];
  }

  const response = await axiosInstance.get(`${coreBasePath}/skill-review-results`, {
    params: {
      page: 1,
      page_size: 50,
      requestid: requestId.trim(),
    },
  });
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);
  const items = Array.isArray(raw?.items) ? raw.items : [];
  return items
    .map((item) => normalizeSkillReviewResult(item))
    .filter((item): item is SkillReviewResultRecord => Boolean(item));
}

export async function listSkillAssets(
  options: ListSkillOptions = {},
): Promise<SkillAssetRecord[]> {
  const result = await listSkillAssetsPage(options);
  return result.records;
}

export async function organizeSkills(
  skills: string[],
): Promise<SkillOrganizeRunRecord> {
  const response = await skillsApi.apiCoreSkillOrganizePost(
    {
      skillOrganizeOpenAPIRequest: {
        requestid: createSkillOrganizeRequestId(),
        skills,
      },
    },
    { silentError: true } as never,
  );
  const payload = unwrapEnvelope<SkillOrganizeOpenAPIResponse>(response.data);
  return {
    requestId: payload.requestid || "",
    status: payload.status || "",
    taskId: payload.taskid || "",
  };
}

export async function listSkillTags(): Promise<string[]> {
  const response = await skillsApi.apiCoreSkillsTagsGet();
  const payload = unwrapEnvelope<{ tags?: string[] }>(response.data);
  return [...new Set(toStringArray(payload.tags))].sort((left, right) =>
    left.localeCompare(right),
  );
}

export async function listSkillCategories(): Promise<string[]> {
  const response = await skillsApi.apiCoreSkillsCategoriesGet();
  const payload = unwrapEnvelope<{ categories?: string[] }>(response.data);
  return [...new Set(toStringArray(payload.categories))].sort((left, right) =>
    left.localeCompare(right),
  );
}

export async function listSkillAssetsPage(
  options: ListSkillOptions = {},
): Promise<SkillAssetListResult> {
  const response = await skillsApi.apiCoreSkillsGet({
    keyword: options.keyword?.trim() || undefined,
    category: options.category?.trim() || undefined,
    tags: (options.tags ?? []).map((item) => item.trim()).filter(Boolean),
    page: options.page ?? 1,
    pageSize: options.pageSize ?? 200,
  });
  const payload = unwrapEnvelope<{
    items?: SkillListItemOpenAPIResponse[];
    page?: number;
    page_size?: number;
    total?: number;
  }>(response.data);

  const records = (payload.items || []).map((item) => normalizeSkillItem(item));

  return {
    records,
    total: payload.total ?? records.length,
    page: payload.page ?? options.page ?? 1,
    pageSize: payload.page_size ?? options.pageSize ?? 200,
  };
}

export async function getSkillAssetDetail(
  skillId: string,
  options?: { loadContent?: boolean },
): Promise<SkillAssetRecord | null> {
  const response = await skillsApi.apiCoreSkillsSkillIdGet({ skillId });
  const payload = unwrapEnvelope<SkillDetailOpenAPIResponse>(response.data);
  if (!payload?.id && !payload?.skill_id) {
    return null;
  }

  let content = payload.file_content || "";
  if (options?.loadContent !== false) {
    try {
      content = await readSkillFileContent(skillId);
    } catch {
      content = payload.file_content || "";
    }
  }

  return normalizeSkillItem(payload, content);
}

export async function createSkillAsset(payload: CreateSkillPayload): Promise<string> {
  const request: SkillCreateManagedOpenAPIRequest = {
    name: payload.name,
    description: payload.description,
    category: payload.category,
    tags: payload.tags,
    auto_evo: payload.autoEvo,
    is_enabled: payload.isEnabled,
    source:
      payload.source.type === "uploaded_zip"
        ? { type: "uploaded_zip", upload_id: payload.source.uploadId }
        : { type: "url", url: payload.source.url },
  };

  const response = await skillsApi.apiCoreSkillsPost({
    skillCreateManagedOpenAPIRequest: request,
  });
  const body = unwrapEnvelope<{ skill_id?: string }>(response.data);
  return body.skill_id || "";
}

export async function enableBuiltinSkill(
  builtinSkillUid: string,
): Promise<SkillAssetRecord | null> {
  const response = await skillsApi.apiCoreBuiltinSkillsBuiltinSkillUidEnablePost({
    builtinSkillUid,
  });
  const payload = unwrapEnvelope<SkillDetailOpenAPIResponse>(response.data);
  if (!payload?.id && !payload?.skill_id) {
    return null;
  }
  return normalizeSkillItem(payload);
}

export async function patchSkillAsset(
  skillId: string,
  payload: SkillUpdateManagedOpenAPIRequest,
) {
  return skillsApi.apiCoreSkillsSkillIdPatch({
    skillId,
    skillUpdateManagedOpenAPIRequest: payload,
  });
}

export async function generateSkillDraft(
  skillId: string,
  payload: SkillDraftGeneratePayload,
) {
  const requestPayload: {
    user_instruct: string;
    suggestion_ids?: string[];
  } = {
    user_instruct: payload.userInstruct.trim(),
  };

  if (payload.suggestionIds?.length) {
    requestPayload.suggestion_ids = payload.suggestionIds;
  }

  return skillsApi.apiCoreSkillsSkillIdGeneratePost({
    skillId,
    skillGenerateOpenAPIRequest: requestPayload,
  });
}

export async function previewSkillDraft(
  skillId: string,
): Promise<SkillDraftPreviewRecord> {
  const [detailResponse, draftStatus] = await Promise.all([
    skillsApi.apiCoreSkillsSkillIdGet({ skillId }),
    getSkillDraftStatus(skillId),
  ]);
  const detailPayload = unwrapEnvelope<SkillDetailOpenAPIResponse>(detailResponse.data);

  if (!hasSkillDraftChanges(draftStatus)) {
    const currentContent = await readSkillFileContent(skillId, SKILL_MD_PATH).catch(
      () => detailPayload.file_content || "",
    );
    return {
      currentContent,
      diff: "",
      draftContent: "",
      draftSourceVersion: draftStatus.draftVersion,
      draftStatus: "",
      outdated: false,
      skillId: detailPayload.skill_id || skillId,
      reviewStatus: "",
      diffLines: [],
    };
  }

  const [fileDiff, currentContent, draftContent] = await Promise.all([
    compareSkillFileDiff(skillId, SKILL_MD_PATH).catch(() => emptySkillFileDiff(SKILL_MD_PATH)),
    readSkillFileContent(skillId, SKILL_MD_PATH).catch(
      () => detailPayload.file_content || "",
    ),
    readSkillFsContent(skillId, SKILL_MD_PATH).catch(() => ""),
  ]);

  const diffLines = mapDiffEntryLines(fileDiff.diffEntryLines);
  const isAgentDraft = isSkillAgentDraftContext(draftStatus);
  const hasReviewSession = Boolean(fileDiff.review?.reviewId);

  return {
    currentContent,
    diff: "",
    draftContent,
    draftSourceVersion: draftStatus.draftVersion,
    draftStatus: isAgentDraft ? "pending_confirm" : "",
    outdated: false,
    skillId: detailPayload.skill_id || skillId,
    reviewStatus: isAgentDraft && hasReviewSession ? "pending_confirm" : "",
    diffLines,
  };
}

export async function confirmSkillDraft(skillId: string): Promise<SkillAssetRecord | null> {
  const response = await skillsApi.apiCoreSkillsSkillIdConfirmPost({ skillId });
  const payload = unwrapEnvelope<SkillDetailOpenAPIResponse>(response.data);
  if (!payload?.id && !payload?.skill_id) {
    return null;
  }
  return normalizeSkillItem(payload);
}

export async function discardSkillDraft(skillId: string): Promise<boolean> {
  const response = await skillsApi.apiCoreSkillsSkillIdDiscardPost({ skillId });
  const payload = unwrapEnvelope<{ discarded?: boolean }>(response.data);
  return Boolean(payload.discarded);
}

export async function removeSkillAsset(skillId: string) {
  return skillsApi.apiCoreSkillsSkillIdDelete({ skillId });
}

export async function trashSkillAsset(skillId: string) {
  return defaultCoreApi.apiCoreSkillsSkillIdTrashPost({ skillId });
}

export async function listTrashedSkillAssetsPage(
  options: ListSkillOptions = {},
): Promise<SkillAssetListResult> {
  const response = await defaultCoreApi.apiCoreSkillsTrashGet({
    params: {
      keyword: options.keyword?.trim() || undefined,
      category: options.category?.trim() || undefined,
      tags: (options.tags ?? []).map((item) => item.trim()).filter(Boolean),
      page: options.page ?? 1,
      page_size: options.pageSize ?? 200,
    },
  });
  const payload = unwrapEnvelope<{
    items?: SkillListItemOpenAPIResponse[];
    page?: number;
    page_size?: number;
    total?: number;
  }>(response.data);

  const records = (payload.items || []).map((item) => normalizeSkillItem(item));

  return {
    records,
    total: payload.total ?? records.length,
    page: payload.page ?? options.page ?? 1,
    pageSize: payload.page_size ?? options.pageSize ?? 200,
  };
}

export async function restoreSkillAsset(skillId: string): Promise<boolean> {
  const response = await defaultCoreApi.apiCoreSkillsSkillIdRestorePost({ skillId });
  const payload = unwrapEnvelope<{ restored?: boolean }>(response.data);
  return Boolean(payload.restored);
}

export async function purgeSkillAsset(skillId: string): Promise<boolean> {
  const response = await defaultCoreApi.apiCoreSkillsSkillIdPurgeDelete({ skillId });
  const payload = unwrapEnvelope<{ purged?: boolean }>(response.data);
  return Boolean(payload.purged);
}

export async function emptySkillTrash(): Promise<number> {
  const response = await defaultCoreApi.apiCoreSkillsTrashDelete();
  const payload = unwrapEnvelope<{ purged?: number }>(response.data);
  return payload.purged ?? 0;
}

export async function disableSkillAsset(skillId: string) {
  return removeSkillAsset(skillId);
}

export async function shareSkillAsset(skillId: string, payload: ShareSkillPayload) {
  return skillSharesApi.apiCoreSkillsSkillIdSharePost({
    skillId,
    shareSkillOpenAPIRequest: {
      target_user_ids: payload.targetUserIds,
      target_group_ids: payload.targetGroupIds || [],
      message: payload.message || "",
    },
  });
}

export async function listIncomingSkillShares(): Promise<SkillShareRecord[]> {
  const response = await skillSharesApi.apiCoreSkillSharesIncomingGet({
    page: 1,
    pageSize: 200,
  });
  const payload = unwrapEnvelope<{ items?: SkillShareListItemOpenAPIResponse[] }>(
    response.data,
  );
  return (payload.items || []).map(normalizeIncomingShare);
}

export async function listOutgoingSkillShares(): Promise<SkillShareRecord[]> {
  const response = await skillSharesApi.apiCoreSkillSharesOutgoingGet({
    page: 1,
    pageSize: 200,
  });
  const payload = unwrapEnvelope<{ items?: SkillShareListItemOpenAPIResponse[] }>(
    response.data,
  );
  return (payload.items || []).map(normalizeOutgoingShare);
}

export async function listSkillShareTargets(skillId: string): Promise<SkillShareRecord[]> {
  const response = await skillSharesApi.apiCoreSkillsSkillIdSharesGet({
    skillId,
    page: 1,
    pageSize: 200,
  });
  const payload = unwrapEnvelope<{
    items?: SkillShareTargetItemOpenAPIResponse[];
    skill_id?: string;
  }>(response.data);

  return (payload.items || []).map((item) =>
    normalizeShareTarget(item, payload.skill_id || skillId),
  );
}

export async function getSkillShareDetail(shareItemId: string): Promise<SkillShareRecord | null> {
  const response = await skillSharesApi.apiCoreSkillSharesShareItemIdGet({ shareItemId });
  const payload = unwrapEnvelope<SkillShareDetailOpenAPIResponse>(response.data);
  return normalizeShareDetail(payload);
}

export async function acceptSkillShare(shareItemId: string) {
  return skillSharesApi.apiCoreSkillSharesShareItemIdAcceptPost({ shareItemId });
}

export async function rejectSkillShare(shareItemId: string) {
  return skillSharesApi.apiCoreSkillSharesShareItemIdRejectPost({ shareItemId });
}

export async function getSkillDraftStatus(skillId: string): Promise<SkillDraftStatusRecord> {
  const response = await skillDraftsApi.apiCoreSkillsSkillIdDraftStatusGet({ skillId });
  const payload = unwrapEnvelope<SkillDraftStatusOpenAPIResponse>(response.data);
  return normalizeDraftStatus(payload);
}

export async function writeSkillDraftText(
  skillId: string,
  options: { path: string; content: string; expectedDraftVersion: number },
) {
  const response = await skillDraftsApi.apiCoreSkillsSkillIdDraftFsTextPut({
    skillId,
    skillDraftWriteTextOpenAPIRequest: {
      path: options.path,
      content: options.content,
      expected_draft_version: options.expectedDraftVersion,
    },
  });
  const payload = unwrapEnvelope<{ draft_version?: number }>(response.data);
  return payload.draft_version ?? options.expectedDraftVersion;
}

export async function commitSkillDraft(
  skillId: string,
  draftVersion: number,
): Promise<{ revisionId: string; revisionNo: number }> {
  const response = await skillRevisionsApi.apiCoreSkillsSkillIdCommitPost({
    skillId,
    skillCommitOpenAPIRequest: { draft_version: draftVersion },
  });
  const payload = unwrapEnvelope<{ revision_id?: string; revision_no?: number }>(response.data);
  return {
    revisionId: payload.revision_id || "",
    revisionNo: payload.revision_no ?? 0,
  };
}

export async function listSkillRevisions(skillId: string): Promise<SkillRevisionRecord[]> {
  const response = await skillRevisionsApi.apiCoreSkillsSkillIdRevisionsGet({ skillId });
  const payload = unwrapEnvelope<{ items?: SkillRevisionOpenAPIResponse[] }>(response.data);
  return (payload.items || []).map(normalizeRevision);
}

export async function getSkillRevisionFile(
  skillId: string,
  revisionId: string,
  path = SKILL_MD_PATH,
): Promise<string> {
  const response = await skillRevisionsApi.apiCoreSkillsSkillIdRevisionsRevisionIdFileGet({
    skillId,
    revisionId,
    path,
  });
  const payload = unwrapEnvelope<SkillFileOpenAPIResponse>(response.data);
  return payload.content || "";
}

export class RollbackConflictError extends Error {
  readonly isConflict = true;
  constructor(message = 'rollback conflict: uncommitted draft exists') {
    super(message);
    this.name = 'RollbackConflictError';
  }
}

export async function rollbackSkill(
  skillId: string,
  targetRevisionId: string,
): Promise<{ headRevisionId: string; revisionNo: number }> {
  const response = await skillRevisionsApi.apiCoreSkillsSkillIdRollbackPost({
    skillId,
    skillRollbackOpenAPIRequest: {
      revision_id: targetRevisionId,
      target_revision_id: targetRevisionId,
    },
  }).catch((err: unknown) => {
    const status = (err as { response?: { status?: number } })?.response?.status;
    if (status === 409) {
      throw new RollbackConflictError();
    }
    throw err;
  });
  const payload = unwrapEnvelope<{
    head_revision_id?: string;
    revision_no?: number;
  }>(response.data);
  return {
    headRevisionId: payload.head_revision_id || '',
    revisionNo: payload.revision_no ?? 0,
  };
}

const readRawString = (value: Record<string, unknown>, keys: string[]): string => {
  for (const key of keys) {
    const field = value[key];
    if (typeof field === 'string' && field.trim()) {
      return field.trim();
    }
    if (typeof field === 'number' && Number.isFinite(field)) {
      return String(field);
    }
  }
  return '';
};

const readRawBoolean = (value: Record<string, unknown>, keys: string[]) => {
  for (const key of keys) {
    const field = value[key];
    if (typeof field === "boolean") {
      return field;
    }
  }
  return false;
};

const readRawNumber = (value: Record<string, unknown>, keys: string[], fallback = 0) => {
  for (const key of keys) {
    const field = value[key];
    if (typeof field === "number" && Number.isFinite(field)) {
      return field;
    }
    if (typeof field === "string" && field.trim()) {
      const parsed = Number(field);
      if (Number.isFinite(parsed)) {
        return parsed;
      }
    }
  }
  return fallback;
};

const normalizeDraftReviewMeta = (
  payload: Record<string, unknown>,
): SkillDraftReviewMeta | undefined => {
  const reviewId = readRawString(payload, ["review_id", "reviewId"]);
  if (!reviewId) {
    return undefined;
  }
  return {
    reviewId,
    reviewVersion: readRawNumber(payload, [
      "review_version",
      "reviewVersion",
      "expected_review_version",
      "expectedReviewVersion",
    ]),
    canUndo: readRawBoolean(payload, ["can_undo", "canUndo"]),
    pendingCount: readRawNumber(payload, ["pending_count", "pendingCount"], 0),
    acceptedCount: readRawNumber(payload, ["accepted_count", "acceptedCount"], 0),
    rejectedCount: readRawNumber(payload, ["rejected_count", "rejectedCount"], 0),
  };
};

const normalizeDraftReviewMutation = (
  payload: Record<string, unknown>,
  fallbackReviewVersion: number,
): SkillDraftReviewMutationResult => ({
  reviewVersion: readRawNumber(
    payload,
    ["review_version", "reviewVersion", "expected_review_version", "expectedReviewVersion"],
    fallbackReviewVersion,
  ),
  canUndo: readRawBoolean(payload, ["can_undo", "canUndo"]),
  pendingCount: readRawNumber(payload, ["pending_count", "pendingCount"], 0),
  acceptedCount: readRawNumber(payload, ["accepted_count", "acceptedCount"], 0),
  rejectedCount: readRawNumber(payload, ["rejected_count", "rejectedCount"], 0),
});

const unwrapSkillFileDiffPayload = (
  payload: DiffFileOpenAPIResponse | Record<string, unknown>,
): { file: Record<string, unknown>; reviewSource: Record<string, unknown> } => {
  const raw = payload as Record<string, unknown>;
  const nestedFileDiff = raw.file_diff ?? raw.fileDiff;
  if (nestedFileDiff && typeof nestedFileDiff === "object") {
    return {
      file: nestedFileDiff as Record<string, unknown>,
      reviewSource: raw,
    };
  }
  return {
    file: raw,
    reviewSource: raw,
  };
};

const normalizeDiffFile = (
  payload: DiffFileOpenAPIResponse | Record<string, unknown>,
): SkillDiffFileRecord => {
  const { file, reviewSource } = unwrapSkillFileDiffPayload(payload);
  const diffEntryLines =
    (file.diffEntryLines as DiffEntryLineOpenAPIResponse[] | undefined) ||
    (file.diff_entry_lines as DiffEntryLineOpenAPIResponse[] | undefined) ||
    [];

  return {
    path: String(file.path || ""),
    status: String(file.status || ""),
    binary: Boolean(file.binary),
    type: String(file.type || "file"),
    tooLarge: Boolean(file.too_large ?? file.tooLarge),
    diffEntryLines,
    review: normalizeDraftReviewMeta(reviewSource),
  };
};

const normalizeDiffTree = (payload: DiffTreeOpenAPIResponse): SkillDiffTreeRecord => ({
  cacheWritten: Boolean(payload.cache_written),
  files: (payload.files || []).map(normalizeDiffFile),
});

const buildHeadDraftDiffRequest = (skillId: string, path?: string) => ({
  old: { type: "head", skill_id: skillId },
  new: { type: "draft", skill_id: skillId },
  context_lines: 3,
  ...(path ? { path } : {}),
});

export async function compareSkillTreeDiff(skillId: string): Promise<SkillDiffTreeRecord> {
  const response = await skillDiffApi.apiCoreSkillDiffTreePost({
    diffOpenAPIRequest: buildHeadDraftDiffRequest(skillId),
  });
  const payload = unwrapEnvelope<DiffTreeOpenAPIResponse>(response.data);
  return normalizeDiffTree(payload);
}

export async function compareSkillFileDiff(
  skillId: string,
  path: string,
): Promise<SkillDiffFileRecord> {
  const response = await skillDiffApi.apiCoreSkillDiffFilePost({
    diffOpenAPIRequest: buildHeadDraftDiffRequest(skillId, path),
  });
  const payload = unwrapEnvelope<DiffFileOpenAPIResponse | Record<string, unknown>>(response.data);
  return normalizeDiffFile(payload);
}

export async function submitSkillDraftReviewActions(
  skillId: string,
  reviewId: string,
  options: {
    expectedReviewVersion: number;
    items: SkillDraftReviewActionItem[];
  },
): Promise<SkillDraftReviewMutationResult> {
  const response = await axiosInstance.post(
    `${BASE_URL}/api/core/skills/${encodeURIComponent(skillId)}/draft-review/${encodeURIComponent(reviewId)}/actions`,
    {
      expected_review_version: options.expectedReviewVersion,
      items: options.items.map((item) => ({
        hunk_id: item.hunkId,
        decision: mapSkillDraftReviewDecisionToApi(item.decision),
        ...(item.path ? { path: item.path } : {}),
      })),
    },
  );
  const payload = unwrapEnvelope<Record<string, unknown>>(response.data);
  return normalizeDraftReviewMutation(payload, options.expectedReviewVersion);
}

export async function undoSkillDraftReview(
  skillId: string,
  reviewId: string,
  expectedReviewVersion: number,
): Promise<SkillDraftReviewMutationResult> {
  const response = await axiosInstance.post(
    `${BASE_URL}/api/core/skills/${encodeURIComponent(skillId)}/draft-review/${encodeURIComponent(reviewId)}:undo`,
    {
      expected_review_version: expectedReviewVersion,
    },
  );
  const payload = unwrapEnvelope<Record<string, unknown>>(response.data);
  return normalizeDraftReviewMutation(payload, expectedReviewVersion);
}

export async function commitSkillDraftReview(
  skillId: string,
  reviewId: string,
  expectedReviewVersion: number,
): Promise<SkillDraftReviewMutationResult> {
  const response = await axiosInstance.post(
    `${BASE_URL}/api/core/skills/${encodeURIComponent(skillId)}/draft-review/${encodeURIComponent(reviewId)}:commit`,
    {
      expected_review_version: expectedReviewVersion,
    },
  );
  const payload = unwrapEnvelope<Record<string, unknown>>(response.data);
  return normalizeDraftReviewMutation(payload, expectedReviewVersion);
}

export async function loadSkillFileDiffLines(
  skillId: string,
  path = SKILL_MD_PATH,
): Promise<{ fileDiff: SkillDiffFileRecord; lines: DiffLine[] }> {
  const fileDiff = await compareSkillFileDiff(skillId, path);
  return {
    fileDiff,
    lines: mapDiffEntryLines(fileDiff.diffEntryLines),
  };
}

export async function mkdirSkillDraftPath(
  skillId: string,
  options: { path: string; expectedDraftVersion: number },
): Promise<number> {
  const response = await skillDraftsApi.apiCoreSkillsSkillIdDraftFsDirPost({
    skillId,
    skillDraftMkdirOpenAPIRequest: {
      path: options.path,
      expected_draft_version: options.expectedDraftVersion,
    },
  });
  const payload = unwrapEnvelope<{ draft_version?: number }>(response.data);
  return payload.draft_version ?? options.expectedDraftVersion;
}

export async function deleteSkillDraftPath(
  skillId: string,
  options: { path: string; expectedDraftVersion: number; recursive?: boolean },
): Promise<number> {
  const response = await skillDraftsApi.apiCoreSkillsSkillIdDraftFsPathDelete({
    skillId,
    path: options.path,
    skillDraftDeleteOpenAPIRequest: {
      path: options.path,
      expected_draft_version: options.expectedDraftVersion,
      recursive: options.recursive,
    },
  });
  const payload = unwrapEnvelope<{ draft_version?: number }>(response.data);
  return payload.draft_version ?? options.expectedDraftVersion;
}

export async function moveSkillDraftPath(
  skillId: string,
  options: { from: string; to: string; expectedDraftVersion: number },
): Promise<number> {
  const response = await skillDraftsApi.apiCoreSkillsSkillIdDraftFsMovePost({
    skillId,
    skillDraftMoveOpenAPIRequest: {
      from: options.from,
      to: options.to,
      expected_draft_version: options.expectedDraftVersion,
    },
  });
  const payload = unwrapEnvelope<{ draft_version?: number }>(response.data);
  return payload.draft_version ?? options.expectedDraftVersion;
}

export async function uploadSkillDraftFile(
  skillId: string,
  options: { path: string; uploadId: string; expectedDraftVersion: number },
): Promise<number> {
  const response = await skillDraftsApi.apiCoreSkillsSkillIdDraftFsUploadPut({
    skillId,
    skillDraftUploadOpenAPIRequest: {
      path: options.path,
      upload_id: options.uploadId,
      expected_draft_version: options.expectedDraftVersion,
    },
  });
  const payload = unwrapEnvelope<{ draft_version?: number }>(response.data);
  return payload.draft_version ?? options.expectedDraftVersion;
}

export async function getSkillTree(skillId: string): Promise<SkillTreeNodeRecord> {
  const response = await skillsApi.apiCoreSkillsSkillIdTreeGet({ skillId });
  const payload = unwrapEnvelope<SkillTreeNodeOpenAPIResponse>(response.data);
  return normalizeTreeNode(payload);
}

export interface SkillFsFileRecord {
  path: string;
  binary: boolean;
  content: string;
  mime: string;
  fileType: string;
  downloadUrl: string;
  blobHash: string;
}

export async function readSkillFsFile(
  skillId: string,
  path: string,
): Promise<SkillFsFileRecord> {
  const response = await skillFsApi.apiCoreSkillsSkillIdFsContentGet({ skillId, path });
  const payload = unwrapEnvelope<SkillFileOpenAPIResponse>(response.data);
  return {
    path: payload.path || path,
    binary: Boolean(payload.binary),
    content: payload.content || "",
    mime: payload.mime || "",
    fileType: payload.file_type || "",
    downloadUrl: payload.download_url || "",
    blobHash: payload.blob_hash || "",
  };
}

export async function readSkillFsContent(skillId: string, path: string): Promise<string> {
  const file = await readSkillFsFile(skillId, path);
  return file.content;
}

export async function listSkillMarketPage(options?: {
  page?: number;
  pageSize?: number;
  keyword?: string;
  category?: string;
}): Promise<MarketSkillListResult> {
  const response = await skillMarketApi.apiCoreSkillMarketGet({
    page: options?.page ?? 1,
    pageSize: options?.pageSize ?? 20,
    keyword: options?.keyword?.trim() || undefined,
    category:
      options?.category && options.category !== "all"
        ? options.category.trim()
        : undefined,
  });
  const payload = unwrapEnvelope<MarketListOpenAPIResponse>(response.data);

  return {
    records: (payload.items || []).map((item) => normalizeMarketItem(item)),
    total: payload.total ?? 0,
    page: payload.page ?? 1,
    pageSize: payload.page_size ?? 20,
  };
}

export async function listBuiltinSkills(): Promise<MarketSkillRecord[]> {
  const response = await axiosInstance.get(`${coreBasePath}/builtin-skills`);
  const payload = unwrapEnvelope<{ items?: BuiltinSkillListItem[] }>(response.data);
  return (payload.items || []).map((item) => ({
    id: item.builtin_skill_uid,
    skillId: item.builtin_skill_uid,
    name: item.name,
    skillName: item.name,
    description: item.description,
    category: item.category,
    tags: [],
    content: item.content,
    headRevisionId: "",
    draft: { hasUncommittedDraft: false, taskId: "", version: 0 },
    autoEvo: false,
    isEnabled: true,
    marketItemId: item.builtin_skill_uid,
    sourceSkillId: item.builtin_skill_uid,
    marketSource: "builtin",
    installed: Boolean(item.installed),
    installedSkillId: item.installed_skill_id || "",
  }));
}

export async function getSkillMarketItem(
  marketItemId: string,
): Promise<MarketSkillRecord | null> {
  const response = await skillMarketApi.apiCoreSkillMarketMarketItemIdGet({
    marketItemId,
  });
  const payload = unwrapEnvelope<MarketItemOpenAPIResponse>(response.data);
  if (!payload?.market_item_id && !payload?.id) {
    return null;
  }
  return normalizeMarketItem(payload);
}

export async function publishSkillToMarket(
  payload: PublishSkillToMarketPayload,
): Promise<{ marketItemId: string; sourceSkillId: string }> {
  const response = await skillMarketApi.apiCoreSkillMarketAdminItemsPost({
    marketPublishOpenAPIRequest: {
      name: payload.name,
      category: payload.category,
      source:
        payload.source.type === "uploaded_zip"
          ? { type: "uploaded_zip", upload_id: payload.source.uploadId }
          : { type: "url", url: payload.source.url },
    },
  });
  const body = unwrapEnvelope<{ market_item_id?: string; source_skill_id?: string }>(
    response.data,
  );
  return {
    marketItemId: body.market_item_id || "",
    sourceSkillId: body.source_skill_id || "",
  };
}

export async function installSkillFromMarket(marketItemId: string): Promise<string> {
  const response = await skillMarketApi.apiCoreSkillMarketMarketItemIdInstallPost({
    marketItemId,
  });
  const payload = unwrapEnvelope<{ skill_id?: string }>(response.data);
  return payload.skill_id || "";
}

export { SKILL_MD_PATH };
