import { axiosInstance, BASE_URL } from "@/components/request";
import type { DiffEntryLine } from "@/api/generated/core-client";

const coreBasePath = `${BASE_URL}/api/core`;
const defaultManagedDraftUserInstruct: Record<ManagedPreferenceDraftKind, string> = {
  memory: "再补一条：跨团队协作时才允许使用 merge",
  "user-preference": "请根据已接受的建议生成用户偏好草稿。",
};

interface ApiEnvelope<T> {
  code?: number;
  message?: string;
  data?: T;
}

interface ManagedStateItem {
  agent_persona?: string;
  auto_evo?: boolean;
  auto_evo_apply_status?: string;
  auto_evo_generation?: number;
  auto_evo_error?: string;
  content?: string;
  content_summary?: string;
  has_pending_review_suggestions?: boolean;
  response_style?: string;
  resource_id?: string;
  resource_type?: string;
  review_status?: string;
  suggestion_status?: string;
  title?: string;
  preferred_name?: string;
}

type RawObject = Record<string, unknown>;

export interface PreferenceAssetRecord {
  id: string;
  title: string;
  content: string;
  protect: boolean;
  autoEvo: boolean;
  agentPersona?: string;
  draftStatus?: string;
  hasPendingReviewSuggestions?: boolean;
  responseStyle?: string;
  resourceType?: string;
  reviewStatus?: string;
  summary?: string;
  suggestionStatus?: string;
  preferredName?: string;
  autoEvoApplyStatus?: string;
  autoEvoGeneration?: number;
  autoEvoError?: string;
}

export interface PreferenceSuggestionPayload {
  title: string;
  content: string;
  reason?: string;
}

export interface PreferenceDraftPreviewRecord {
  acceptedCount?: number;
  canUndo?: boolean;
  currentContent: string;
  diff: string;
  draftContent: string;
  draftSourceVersion: number;
  draftStatus: string;
  draftVersion?: number;
  fileDiff?: {
    binary: boolean;
    diffEntryLines: DiffEntryLine[];
    editableText: boolean;
    hunkCount: number;
    path: string;
    status: string;
    supported: boolean;
    tooLarge: boolean;
    unsupportedReason: string;
  };
  path?: string;
  pendingCount?: number;
  rejectedCount?: number;
  resourceType?: string;
  reviewId?: string;
  reviewVersion?: number;
}

export interface PreferenceDraftGeneratePayload {
  suggestionIds?: string[];
  userInstruct: string;
}

export interface PreferenceDraftGenerateRecord {
  draftContent: string;
  draftSourceVersion: number;
  draftStatus: string;
  suggestionIds: string[];
}

export interface PreferenceDraftConfirmRecord {
  content: string;
  revisionId: string;
  version: number;
}

export type ManagedPreferenceDraftKind = "memory" | "user-preference";
export type ManagedPreferenceDraftDecision = "accept" | "reject";

export interface ManagedPreferenceDraftReviewMutationRecord {
  canUndo: boolean;
  draftContent: string;
  draftVersion: number;
  reviewId: string;
  reviewVersion: number;
}

export interface PreferenceSuggestionRecord {
  id: string;
  status: string;
  invalidReason?: string;
}

export interface EvolutionSuggestionRecord {
  id: string;
  action: string;
  category: string;
  content: string;
  createdAt: string;
  fileExt: string;
  fullContent: string;
  invalidReason: string;
  outdated: boolean;
  parentSkillName: string;
  reason: string;
  relativePath: string;
  resourceKey: string;
  resourceType: string;
  reviewedAt?: string;
  reviewerId: string;
  reviewerName: string;
  sessionId: string;
  skillName: string;
  status: string;
  title: string;
  updatedAt: string;
  userId: string;
}

export interface EvolutionSuggestionListOptions {
  page?: number;
  pageSize?: number;
  statuses?: string[];
  evolutionId?: string;
  resourceId?: string;
  resourceType?: string;
  resourceKey?: string;
  keyword?: string;
  skillId?: string;
  preferenceId?: string;
  memoryId?: string;
}

export interface EvolutionSuggestionListResult {
  items: EvolutionSuggestionRecord[];
  page: number;
  pageSize: number;
  total: number;
  hasMore: boolean;
}

interface PreferenceFrontMatter {
  agentPersona?: string;
  title?: string;
  protect?: boolean;
  responseStyle?: string;
  preferredName?: string;
}

const normalizeResourceType = (resourceType?: string) =>
  (resourceType || "").trim().toLowerCase();

const buildEvolutionId = (resourceType?: string, resourceId?: string) => {
  const normalizedResourceType = (resourceType || "").trim();
  const normalizedResourceId = (resourceId || "").trim();
  if (!normalizedResourceType || !normalizedResourceId) {
    return "";
  }

  return `${normalizedResourceType}:${normalizedResourceId}`;
};

const resolveEvolutionId = (options: EvolutionSuggestionListOptions) =>
  options.evolutionId ||
  buildEvolutionId(options.resourceType, options.resourceId) ||
  buildEvolutionId("skill", options.skillId) ||
  buildEvolutionId("memory", options.memoryId) ||
  buildEvolutionId(options.resourceType || "user-preference", options.preferenceId);

const unwrapEnvelope = <T>(payload: unknown): T => {
  if (!payload || typeof payload !== "object") {
    return payload as T;
  }

  const envelope = payload as ApiEnvelope<T>;
  if ("data" in envelope && envelope.data !== undefined) {
    return envelope.data as T;
  }

  return payload as T;
};

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

const normalizeEvolutionSuggestion = (
  item: RawObject,
): EvolutionSuggestionRecord | null => {
  const id = toStringValue(item.id, "");
  if (!id) {
    return null;
  }

  return {
    id,
    action: toStringValue(item.action, ""),
    category: toStringValue(item.category, ""),
    content: toStringValue(item.content, ""),
    createdAt: toStringValue(item.created_at, ""),
    fileExt: toStringValue(item.file_ext, ""),
    fullContent: toStringValue(item.full_content, ""),
    invalidReason: toStringValue(item.invalid_reason, ""),
    outdated: toBoolean(item.outdated, false),
    parentSkillName: toStringValue(item.parent_skill_name, ""),
    reason: toStringValue(item.reason, ""),
    relativePath: toStringValue(item.relative_path, ""),
    resourceKey: toStringValue(item.resource_key, ""),
    resourceType: toStringValue(item.resource_type, ""),
    reviewedAt: toStringValue(item.reviewed_at, ""),
    reviewerId: toStringValue(item.reviewer_id, ""),
    reviewerName: toStringValue(item.reviewer_name, ""),
    sessionId: toStringValue(item.session_id, ""),
    skillName: toStringValue(item.skill_name, ""),
    status: toStringValue(item.status, ""),
    title: toStringValue(item.title, ""),
    updatedAt: toStringValue(item.updated_at, ""),
    userId: toStringValue(item.user_id, ""),
  };
};

const extractEvolutionSuggestionList = (
  payload: unknown,
  options: EvolutionSuggestionListOptions = {},
): EvolutionSuggestionListResult => {
  const unwrapped = unwrapEnvelope<unknown>(payload);
  const rawPayload = toRawObject(unwrapped);
  const rawItems = Array.isArray(rawPayload?.items)
    ? rawPayload.items
    : Array.isArray(unwrapped)
      ? unwrapped
      : [];
  const items = rawItems
    .map((item) => toRawObject(item))
    .filter((item): item is RawObject => Boolean(item))
    .map((item) => normalizeEvolutionSuggestion(item))
    .filter((item): item is EvolutionSuggestionRecord => Boolean(item));
  const page = Math.max(1, toNumberValue(rawPayload?.page, options.page || 1));
  const pageSize = Math.max(1, toNumberValue(rawPayload?.page_size, options.pageSize || 20));
  const total = Math.max(items.length, toNumberValue(rawPayload?.total, items.length));

  return {
    items,
    page,
    pageSize,
    total,
    hasMore: page * pageSize < total,
  };
};

const sanitizeInlineValue = (value: string) => value.replace(/\r?\n/g, " ").trim();

const parsePreferenceFrontMatter = (rawContent: string) => {
  const normalized = rawContent.replace(/\r\n/g, "\n");
  if (!normalized.startsWith("---\n")) {
    return {
      frontMatter: {} as PreferenceFrontMatter,
      body: normalized,
    };
  }

  const endMarker = "\n---\n";
  const endIndex = normalized.indexOf(endMarker, 4);
  if (endIndex < 0) {
    return {
      frontMatter: {} as PreferenceFrontMatter,
      body: normalized,
    };
  }

  const metaBlock = normalized.slice(4, endIndex);
  const body = normalized.slice(endIndex + endMarker.length);
  const frontMatter: PreferenceFrontMatter = {};

  metaBlock.split("\n").forEach((line) => {
    const separatorIndex = line.indexOf(":");
    if (separatorIndex < 0) {
      return;
    }

    const key = line.slice(0, separatorIndex).trim().toLowerCase();
    const value = line.slice(separatorIndex + 1).trim();

    if (key === "title") {
      frontMatter.title = value;
    }
    if (key === "protect") {
      frontMatter.protect = toBoolean(value, false);
    }
    if (key === "agent_persona") {
      frontMatter.agentPersona = value;
    }
    if (key === "preferred_name") {
      frontMatter.preferredName = value;
    }
    if (key === "response_style") {
      frontMatter.responseStyle = value;
    }
  });

  return {
    frontMatter,
    body,
  };
};

const fallbackTitleFromContent = (content: string, fallback = "") => {
  const firstLine = content
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find(Boolean);

  return firstLine || fallback;
};

const isUserPreferenceResourceType = (resourceType?: string) => {
  const normalized = normalizeResourceType(resourceType);

  if (
    normalized.includes("user_preference") ||
    normalized.includes("user-preference") ||
    normalized.includes("preference") ||
    normalized.includes("habit") ||
    normalized.includes("experience") ||
    normalized.includes("memory")
  ) {
    return true;
  }

  return false;
};

const isMemoryResourceType = (resourceType?: string) => {
  const normalized = normalizeResourceType(resourceType);
  return Boolean(normalized) && normalized.includes("memory") && !normalized.includes("preference");
};

export const resolveManagedPreferenceDraftKind = (
  resourceType?: string,
): ManagedPreferenceDraftKind =>
  isMemoryResourceType(resourceType) ? "memory" : "user-preference";

export type PersonalResourceApiType = "memory" | "user_preference";

const getPersonalResourceType = (kind: ManagedPreferenceDraftKind): PersonalResourceApiType =>
  kind === "user-preference" ? "user_preference" : kind;

export const resolvePersonalResourceApiType = (
  resourceType?: string,
): PersonalResourceApiType => getPersonalResourceType(resolveManagedPreferenceDraftKind(resourceType));

export interface PersonalResourceFileRecord {
  content: string;
  draftVersion: number;
  draftStatus: string;
  revisionId: string;
  revisionNo: number;
  binary: boolean;
  agentPersona?: string;
  preferredName?: string;
  responseStyle?: string;
}

export const hasPersonalResourceDraftChanges = (options: {
  draftStatus?: string;
  headContent: string;
  draftContent: string;
}): boolean => {
  const normalizedStatus = (options.draftStatus || "").trim().toLowerCase();
  if (normalizedStatus && normalizedStatus !== "none") {
    return true;
  }
  return options.draftContent.trim() !== options.headContent.trim();
};

const getManagedPreferenceDraftEndpoint = (
  kind: ManagedPreferenceDraftKind,
  action: "generate" | "preview" | "commit" | "discard",
) => {
  if (action === "generate") {
    return `${coreBasePath}/${kind}:generate`;
  }

  return `${coreBasePath}/personal-resource/${getPersonalResourceType(kind)}:${
    action === "preview" ? "draft-preview" : action
  }`;
};

const resolveManagedDraftUserInstruct = (
  kind: ManagedPreferenceDraftKind,
  userInstruct: string,
) => {
  const normalizedUserInstruct = userInstruct.trim();
  return normalizedUserInstruct || defaultManagedDraftUserInstruct[kind];
};

const isManagedStateLike = (item: RawObject) =>
  ["resource_id", "resource_type", "title", "content", "content_summary"].some(
    (key) => key in item,
  );

export const serializePreferenceContent = (item: {
  title: string;
  content: string;
  protect?: boolean;
}) => {
  return item.content.trim();
};

export const parsePreferenceContent = (
  rawContent: string,
  fallback?: Partial<PreferenceAssetRecord>,
): PreferenceAssetRecord => {
  const normalizedContent = toStringValue(rawContent, "");
  const { frontMatter, body } = parsePreferenceFrontMatter(normalizedContent);
  const parsedBody = body.trim();
  const title =
    sanitizeInlineValue(frontMatter.title || "") ||
    sanitizeInlineValue(fallback?.title || "") ||
    fallbackTitleFromContent(parsedBody, fallback?.id || "preference");

  return {
    id: fallback?.id || title,
    title,
    content: parsedBody,
    protect: frontMatter.protect ?? Boolean(fallback?.protect),
    autoEvo: Boolean(fallback?.autoEvo),
    agentPersona: frontMatter.agentPersona ?? fallback?.agentPersona,
    responseStyle: frontMatter.responseStyle ?? fallback?.responseStyle,
    resourceType: fallback?.resourceType,
    summary: fallback?.summary,
    preferredName: frontMatter.preferredName ?? fallback?.preferredName,
  };
};

const normalizeManagedPreference = (item: ManagedStateItem): PreferenceAssetRecord | null => {
  const resourceType = toStringValue(item.resource_type, "");
  if (!isUserPreferenceResourceType(resourceType)) {
    return null;
  }

  const id = toStringValue(item.resource_id, "");
  const title = toStringValue(item.title, "");
  const summary = toStringValue(item.content_summary, "");
  const content = toStringValue(item.content, "");
  const agentPersona = toStringValue(item.agent_persona, "");
  const preferredName = toStringValue(item.preferred_name, "");
  const responseStyle = toStringValue(item.response_style, "");
  const hasPendingReviewSuggestions = toBoolean(
    item.has_pending_review_suggestions,
    false,
  );
  const reviewStatus = toStringValue(item.review_status, "none");
  const suggestionStatus = toStringValue(item.suggestion_status, "");

  if (!id && !title && !content) {
    return null;
  }

  const backendAutoEvo = toBoolean(item.auto_evo, false);
  const parsed = parsePreferenceContent(content, {
    id: id || title || summary || "preference",
    title: title || summary || "",
    content,
    protect: false,
    autoEvo: backendAutoEvo,
    agentPersona,
    responseStyle,
    resourceType,
    summary,
    preferredName,
  });

  return {
    ...parsed,
    hasPendingReviewSuggestions,
    title: sanitizeInlineValue(title) || parsed.title,
    agentPersona: agentPersona || parsed.agentPersona,
    preferredName: preferredName || parsed.preferredName,
    responseStyle: responseStyle || parsed.responseStyle,
    reviewStatus,
    suggestionStatus,
    autoEvoApplyStatus: toStringValue(item.auto_evo_apply_status, ""),
    autoEvoGeneration: toNumberValue(item.auto_evo_generation, 0),
    autoEvoError: toStringValue(item.auto_evo_error, ""),
  };
};

const extractManagedPreferenceRecords = (payload: unknown): PreferenceAssetRecord[] => {
  const unwrapped = unwrapEnvelope<unknown>(payload);
  const rawPayload = toRawObject(unwrapped);
  if (!rawPayload) {
    return [];
  }

  const rawItems = Array.isArray(rawPayload.items) ? rawPayload.items : [rawPayload];
  const deduped = new Map<string, PreferenceAssetRecord>();

  rawItems
    .map((item) => toRawObject(item))
    .filter((item): item is RawObject => Boolean(item))
    .filter((item) => isManagedStateLike(item))
    .map((item) => normalizeManagedPreference(item as ManagedStateItem))
    .filter((item): item is PreferenceAssetRecord => Boolean(item))
    .forEach((item) => {
      deduped.set(item.id, item);
    });

  return Array.from(deduped.values());
};

export async function listPreferenceAssets(): Promise<PreferenceAssetRecord[]> {
  const response = await axiosInstance.get(`${coreBasePath}/personalization-items`);
  return extractManagedPreferenceRecords(response.data);
}

export async function checkUserPreferenceConfigured(): Promise<boolean> {
  try {
    const assets = await listPreferenceAssets();
    const pref = assets.find(
      (a) =>
        a.resourceType?.includes("user") ||
        a.resourceType?.includes("preference"),
    );
    if (!pref) return false;
    return !!(pref.agentPersona || pref.preferredName || pref.responseStyle);
  } catch {
    return false;
  }
}

export async function listEvolutionSuggestions(
  options: EvolutionSuggestionListOptions = {},
): Promise<EvolutionSuggestionListResult> {
  const params = new URLSearchParams();
  params.set("page", String(options.page || 1));
  params.set("page_size", String(options.pageSize || 20));

  const evolutionId = resolveEvolutionId(options);
  if (evolutionId) {
    params.set("evolution_id", evolutionId);
  }
  if (options.resourceType) {
    params.set("resource_type", options.resourceType);
  }
  if (options.resourceKey) {
    params.set("resource_key", options.resourceKey);
  }
  if (options.keyword) {
    params.set("keyword", options.keyword);
  }
  if (options.statuses?.length) {
    options.statuses
      .map((status) => status.trim())
      .filter(Boolean)
      .forEach((status) => params.append("status", status));
  }

  const response = await axiosInstance.get(
    `${coreBasePath}/evolution/suggestions?${params.toString()}`,
  );
  return extractEvolutionSuggestionList(response.data, options);
}

export async function getEvolutionSuggestion(
  suggestionId: string,
): Promise<EvolutionSuggestionRecord | null> {
  const response = await axiosInstance.get(
    `${coreBasePath}/evolution/suggestions/${encodeURIComponent(suggestionId)}`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);
  return raw ? normalizeEvolutionSuggestion(raw) : null;
}

export async function approveEvolutionSuggestion(
  suggestionId: string,
): Promise<EvolutionSuggestionRecord | null> {
  const response = await axiosInstance.post(
    `${coreBasePath}/evolution/suggestions/${encodeURIComponent(suggestionId)}:approve`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);
  return raw ? normalizeEvolutionSuggestion(raw) : null;
}

const submitEvolutionSuggestionBatchDecision = async (
  action: "batchApprove" | "batchReject",
  suggestionIds: string[],
): Promise<EvolutionSuggestionRecord[]> => {
  const ids = suggestionIds
    .map((item) => item.trim())
    .filter(Boolean)
    .filter((item, index, array) => array.indexOf(item) === index);

  if (!ids.length) {
    return [];
  }

  const response = await axiosInstance.post(
    `${coreBasePath}/evolution/suggestions:${action}`,
    { ids },
  );
  return extractEvolutionSuggestionList(response.data).items;
};

export async function batchApproveEvolutionSuggestions(
  suggestionIds: string[],
): Promise<EvolutionSuggestionRecord[]> {
  return submitEvolutionSuggestionBatchDecision("batchApprove", suggestionIds);
}

export async function batchRejectEvolutionSuggestions(
  suggestionIds: string[],
): Promise<EvolutionSuggestionRecord[]> {
  return submitEvolutionSuggestionBatchDecision("batchReject", suggestionIds);
}

export async function rejectEvolutionSuggestion(
  suggestionId: string,
): Promise<EvolutionSuggestionRecord | null> {
  const response = await axiosInstance.post(
    `${coreBasePath}/evolution/suggestions/${encodeURIComponent(suggestionId)}:reject`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);
  return raw ? normalizeEvolutionSuggestion(raw) : null;
}

export async function getPersonalizationSetting(): Promise<boolean> {
  const response = await axiosInstance.get(`${coreBasePath}/personalization-setting`);
  const payload = unwrapEnvelope<{ enabled?: boolean }>(response.data);
  return Boolean(payload?.enabled);
}

export async function updatePersonalizationSetting(enabled: boolean): Promise<boolean> {
  const response = await axiosInstance.put(`${coreBasePath}/personalization-setting`, {
    enabled,
  });
  const payload = unwrapEnvelope<{ enabled?: boolean }>(response.data);
  return Boolean(payload?.enabled);
}

export interface PersonalResourceMetadataPatch {
  agentPersona?: string;
  preferredName?: string;
  responseStyle?: string;
  autoEvo?: boolean;
}

const buildPersonalResourcePatchPayload = (
  patch: PersonalResourceMetadataPatch,
): RawObject => {
  const requestPayload: RawObject = {};
  if (patch.agentPersona !== undefined) {
    requestPayload.agent_persona = patch.agentPersona;
  }
  if (patch.preferredName !== undefined) {
    requestPayload.preferred_name = patch.preferredName;
  }
  if (patch.responseStyle !== undefined) {
    requestPayload.response_style = patch.responseStyle;
  }
  if (patch.autoEvo !== undefined) {
    requestPayload.auto_evo = patch.autoEvo;
  }
  return requestPayload;
};

export async function patchPersonalResourceMetadata(
  resourceType: PersonalResourceApiType,
  patch: PersonalResourceMetadataPatch,
): Promise<void> {
  const requestPayload = buildPersonalResourcePatchPayload(patch);
  if (!Object.keys(requestPayload).length) {
    return;
  }

  await axiosInstance.patch(
    `${coreBasePath}/personal-resource/${resourceType}`,
    requestPayload,
  );
}

export async function createPreferenceSuggestions(input: {
  sessionId: string;
  suggestions: PreferenceSuggestionPayload[];
}): Promise<PreferenceSuggestionRecord[]> {
  const response = await axiosInstance.post(`${coreBasePath}/user_preference/suggestion`, {
    session_id: input.sessionId,
    suggestions: input.suggestions.map((item) => ({
      title: item.title,
      content: item.content,
      reason: item.reason || "",
    })),
  });
  const payload = unwrapEnvelope<{ items?: Array<RawObject | null> }>(response.data);
  const items = Array.isArray(payload?.items) ? payload.items : [];

  return items
    .map((item) => toRawObject(item))
    .filter((item): item is RawObject => Boolean(item))
    .map((item) => ({
      id: toStringValue(item.id, ""),
      status: toStringValue(item.status, ""),
      invalidReason: toStringValue(item.invalid_reason, ""),
    }))
    .filter((item) => Boolean(item.id));
}

export async function generatePreferenceDraft(
  input: PreferenceDraftGeneratePayload,
): Promise<PreferenceDraftGenerateRecord> {
  return generateManagedPreferenceDraft("user-preference", input);
}

export async function generateManagedPreferenceDraft(
  kind: ManagedPreferenceDraftKind,
  input: PreferenceDraftGeneratePayload,
): Promise<PreferenceDraftGenerateRecord> {
  const normalizedUserInstruct = input.userInstruct.trim();
  const requestPayload = {
    user_instruct: resolveManagedDraftUserInstruct(kind, normalizedUserInstruct),
  };

  const response = await axiosInstance.post(
    getManagedPreferenceDraftEndpoint(kind, "generate"),
    requestPayload,
  );
  const payload = unwrapEnvelope<RawObject>(response.data);

  return {
    draftContent: toStringValue(payload?.draft_content, ""),
    draftSourceVersion: Number(payload?.draft_source_version || 0),
    draftStatus: toStringValue(payload?.draft_status, ""),
    suggestionIds: Array.isArray(payload?.suggestion_ids)
      ? payload.suggestion_ids
          .map((item) => toStringValue(item, ""))
          .filter(Boolean)
      : [],
  };
}

export async function previewPreferenceDraft(): Promise<PreferenceDraftPreviewRecord> {
  return previewManagedPreferenceDraft("user-preference");
}

export async function previewManagedPreferenceDraft(
  kind: ManagedPreferenceDraftKind,
): Promise<PreferenceDraftPreviewRecord> {
  const response = await axiosInstance.get(getManagedPreferenceDraftEndpoint(kind, "preview"));
  const payload = unwrapEnvelope<RawObject>(response.data);
  const rawFileDiff = toRawObject(payload?.file_diff);
  const rawEntryLines = Array.isArray(rawFileDiff?.diff_entry_lines)
    ? rawFileDiff.diff_entry_lines
    : [];

  return {
    acceptedCount: toNumberValue(payload?.accepted_count, 0),
    canUndo: toBoolean(payload?.can_undo, false),
    currentContent: toStringValue(payload?.head_content, ""),
    diff: toStringValue(payload?.diff, ""),
    draftContent: toStringValue(payload?.draft_content, ""),
    draftSourceVersion: Number(payload?.draft_source_version || 0),
    draftStatus: toStringValue(payload?.draft_status, ""),
    draftVersion: toNumberValue(payload?.draft_version, 0),
    fileDiff: {
      binary: toBoolean(rawFileDiff?.binary, false),
      diffEntryLines: rawEntryLines
        .map((line) => toRawObject(line))
        .filter((line): line is RawObject => Boolean(line)) as unknown as DiffEntryLine[],
      editableText: toBoolean(rawFileDiff?.editable_text, true),
      hunkCount: toNumberValue(rawFileDiff?.hunk_count, 0),
      path: toStringValue(rawFileDiff?.path, toStringValue(payload?.path, "")),
      status: toStringValue(rawFileDiff?.status, ""),
      supported: toBoolean(rawFileDiff?.supported, true),
      tooLarge: toBoolean(rawFileDiff?.too_large, false),
      unsupportedReason: toStringValue(rawFileDiff?.unsupported_reason, ""),
    },
    path: toStringValue(payload?.path, ""),
    pendingCount: toNumberValue(payload?.pending_count, 0),
    rejectedCount: toNumberValue(payload?.rejected_count, 0),
    resourceType: toStringValue(payload?.resource_type, getPersonalResourceType(kind)),
    reviewId: toStringValue(payload?.review_id, ""),
    reviewVersion: toNumberValue(payload?.review_version, 0),
  };
}

export async function confirmPreferenceDraft(): Promise<PreferenceDraftConfirmRecord> {
  return confirmManagedPreferenceDraft("user-preference");
}

export async function confirmManagedPreferenceDraft(
  kind: ManagedPreferenceDraftKind,
): Promise<PreferenceDraftConfirmRecord> {
  const preview = await previewManagedPreferenceDraft(kind);
  const response = await axiosInstance.post(getManagedPreferenceDraftEndpoint(kind, "commit"), {
    expected_draft_version: preview.draftVersion,
    message: "Confirm reviewed draft",
    source_ref_type: "draft_review",
    source_ref_id: preview.reviewId,
  });
  const payload = unwrapEnvelope<RawObject>(response.data);

  return {
    content: toStringValue(payload?.content, ""),
    revisionId: toStringValue(payload?.revision_id, ""),
    version: toNumberValue(payload?.revision_no, 0),
  };
}

export async function reviewManagedPreferenceDraftHunks(
  kind: ManagedPreferenceDraftKind,
  options: {
    reviewId: string;
    expectedReviewVersion: number;
    items: Array<{ hunkId: string; decision: ManagedPreferenceDraftDecision }>;
  },
): Promise<ManagedPreferenceDraftReviewMutationRecord> {
  const resourceType = getPersonalResourceType(kind);
  const response = await axiosInstance.post(
    `${coreBasePath}/personal-resource/${resourceType}/draft-review/${encodeURIComponent(
      options.reviewId,
    )}/actions`,
    {
      expected_review_version: options.expectedReviewVersion,
      items: options.items.map((item) => ({
        hunk_id: item.hunkId,
        decision: item.decision,
      })),
    },
  );
  const payload = unwrapEnvelope<RawObject>(response.data);

  return {
    canUndo: toBoolean(payload?.can_undo, false),
    draftContent: toStringValue(payload?.draft_content, ""),
    draftVersion: toNumberValue(payload?.draft_version, 0),
    reviewId: toStringValue(payload?.review_id, options.reviewId),
    reviewVersion: toNumberValue(payload?.review_version, options.expectedReviewVersion),
  };
}

export async function undoManagedPreferenceDraftReview(
  kind: ManagedPreferenceDraftKind,
  options: { reviewId: string; expectedReviewVersion: number },
): Promise<ManagedPreferenceDraftReviewMutationRecord> {
  const resourceType = getPersonalResourceType(kind);
  const response = await axiosInstance.post(
    `${coreBasePath}/personal-resource/${resourceType}/draft-review/${encodeURIComponent(
      options.reviewId,
    )}:undo`,
    {
      expected_review_version: options.expectedReviewVersion,
    },
  );
  const payload = unwrapEnvelope<RawObject>(response.data);

  return {
    canUndo: toBoolean(payload?.can_undo, false),
    draftContent: toStringValue(payload?.draft_content, ""),
    draftVersion: toNumberValue(payload?.draft_version, 0),
    reviewId: toStringValue(payload?.review_id, options.reviewId),
    reviewVersion: toNumberValue(payload?.review_version, options.expectedReviewVersion),
  };
}

export async function discardPreferenceDraft(): Promise<boolean> {
  return discardManagedPreferenceDraft("user-preference");
}

export async function discardManagedPreferenceDraft(
  kind: ManagedPreferenceDraftKind,
): Promise<boolean> {
  const response = await axiosInstance.post(getManagedPreferenceDraftEndpoint(kind, "discard"));
  const payload = unwrapEnvelope<RawObject>(response.data);
  return toBoolean(payload?.discarded, true);
}

const normalizePersonalResourceFile = (payload: RawObject): PersonalResourceFileRecord => ({
  content: toStringValue(payload.content, ""),
  draftVersion: toNumberValue(payload.draft_version, 0),
  draftStatus: toStringValue(payload.draft_status, ""),
  revisionId: toStringValue(payload.revision_id, ""),
  revisionNo: toNumberValue(payload.revision_no, 0),
  binary: toBoolean(payload.binary, false),
  agentPersona: toStringValue(payload.agent_persona, ""),
  preferredName: toStringValue(payload.preferred_name, ""),
  responseStyle: toStringValue(payload.response_style, ""),
});

export async function readPersonalResourceFile(
  resourceType: PersonalResourceApiType,
  options?: { ref?: "head" | "draft"; revisionId?: string },
): Promise<PersonalResourceFileRecord> {
  const params = new URLSearchParams();
  if (options?.ref) {
    params.set("ref", options.ref);
  }
  if (options?.revisionId) {
    params.set("revision_id", options.revisionId);
  }
  const query = params.toString();
  const response = await axiosInstance.get(
    `${coreBasePath}/personal-resource/${resourceType}:file${query ? `?${query}` : ""}`,
  );
  const payload = unwrapEnvelope<RawObject>(response.data);
  return normalizePersonalResourceFile(payload || {});
}

export async function writePersonalResourceDraft(
  resourceType: PersonalResourceApiType,
  options: { content: string; expectedDraftVersion?: number },
): Promise<number> {
  const requestPayload: RawObject = {
    content: options.content,
  };
  if (options.expectedDraftVersion && options.expectedDraftVersion > 0) {
    requestPayload.expected_draft_version = options.expectedDraftVersion;
  }

  const response = await axiosInstance.put(
    `${coreBasePath}/personal-resource/${resourceType}:file`,
    requestPayload,
  );
  const payload = unwrapEnvelope<RawObject>(response.data);
  return toNumberValue(payload?.draft_version, options.expectedDraftVersion || 0);
}

export async function commitPersonalResourceDraft(
  resourceType: PersonalResourceApiType,
  expectedDraftVersion: number,
  message = "update personal resource content",
): Promise<{ revisionId: string; revisionNo: number }> {
  const response = await axiosInstance.post(
    `${coreBasePath}/personal-resource/${resourceType}:commit`,
    {
      expected_draft_version: expectedDraftVersion,
      message,
    },
  );
  const payload = unwrapEnvelope<RawObject>(response.data);
  return {
    revisionId: toStringValue(payload?.revision_id, ""),
    revisionNo: toNumberValue(payload?.revision_no, 0),
  };
}

export async function saveAndCommitPersonalResourceContent(
  resourceType: PersonalResourceApiType,
  content: string,
  options?: {
    expectedDraftVersion?: number;
    message?: string;
  },
): Promise<{ revisionId: string; revisionNo: number; draftVersion: number }> {
  const draftVersion = await writePersonalResourceDraft(resourceType, {
    content,
    expectedDraftVersion: options?.expectedDraftVersion,
  });
  const committed = await commitPersonalResourceDraft(
    resourceType,
    draftVersion,
    options?.message || "update personal resource content",
  );

  return {
    ...committed,
    draftVersion,
  };
}

export async function discardPersonalResourceDraft(
  resourceType: PersonalResourceApiType,
): Promise<boolean> {
  const response = await axiosInstance.post(
    `${coreBasePath}/personal-resource/${resourceType}:discard`,
  );
  const payload = unwrapEnvelope<RawObject>(response.data);
  return toBoolean(payload?.discarded, true);
}
