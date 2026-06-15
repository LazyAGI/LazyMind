import { axiosInstance, BASE_URL } from "@/components/request";

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
  user_address?: string;
}

type RawObject = Record<string, unknown>;

export interface PreferenceAssetRecord {
  id: string;
  title: string;
  content: string;
  protect: boolean;
  autoEvo: boolean;
  agentPersona?: string;
  hasPendingReviewSuggestions?: boolean;
  responseStyle?: string;
  resourceType?: string;
  reviewStatus?: string;
  summary?: string;
  suggestionStatus?: string;
  userAddress?: string;
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
  currentContent: string;
  diff: string;
  draftContent: string;
  draftSourceVersion: number;
  draftStatus: string;
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
  version: number;
}

export type ManagedPreferenceDraftKind = "memory" | "user-preference";

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
  userAddress?: string;
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
    if (key === "user_address") {
      frontMatter.userAddress = value;
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

const getManagedPreferenceDraftEndpoint = (
  kind: ManagedPreferenceDraftKind,
  action: "generate" | "preview" | "confirm" | "discard",
) => `${coreBasePath}/${kind}:${action === "preview" ? "draft-preview" : action}`;

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
    userAddress: frontMatter.userAddress ?? fallback?.userAddress,
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
  const userAddress = toStringValue(item.user_address, "");
  const responseStyle = toStringValue(item.response_style, "");
  const hasPendingReviewSuggestions = toBoolean(
    item.has_pending_review_suggestions,
    false,
  );
  const reviewStatus = toStringValue(
    item.review_status ||
      item.suggestion_status ||
      (hasPendingReviewSuggestions ? "pending" : ""),
    "",
  );
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
    userAddress,
  });

  return {
    ...parsed,
    hasPendingReviewSuggestions,
    title: sanitizeInlineValue(title) || parsed.title,
    agentPersona: agentPersona || parsed.agentPersona,
    userAddress: userAddress || parsed.userAddress,
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

const pickUpsertedPreferenceRecord = (
  records: PreferenceAssetRecord[],
  expected: { title: string; content: string },
) => {
  if (!records.length) {
    return null;
  }

  const expectedTitle = sanitizeInlineValue(expected.title);
  const expectedContent = expected.content.trim();

  return [...records]
    .sort((left, right) => {
      const leftScore =
        Number(left.title === expectedTitle) * 2 +
        Number(left.content.trim() === expectedContent);
      const rightScore =
        Number(right.title === expectedTitle) * 2 +
        Number(right.content.trim() === expectedContent);

      return rightScore - leftScore;
    })[0];
};

export async function listPreferenceAssets(): Promise<PreferenceAssetRecord[]> {
  const response = await axiosInstance.get(`${coreBasePath}/personalization-items`);
  return extractManagedPreferenceRecords(response.data);
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

export async function upsertPreferenceAsset(item: {
  title: string;
  content: string;
  protect?: boolean;
  autoEvo?: boolean;
  agentPersona?: string;
  responseStyle?: string;
  resourceType?: string;
  userAddress?: string;
}): Promise<PreferenceAssetRecord> {
  const endpoint = isMemoryResourceType(item.resourceType) ? "memory" : "user-preference";
  const requestPayload: RawObject = {
    content: serializePreferenceContent(item),
  };
  if (item.autoEvo !== undefined) {
    requestPayload.auto_evo = Boolean(item.autoEvo);
  }
  if (item.agentPersona !== undefined) {
    requestPayload.agent_persona = item.agentPersona;
  }
  if (item.userAddress !== undefined) {
    requestPayload.user_address = item.userAddress;
  }
  if (item.responseStyle !== undefined) {
    requestPayload.response_style = item.responseStyle;
  }
  const response = await axiosInstance.put(`${coreBasePath}/${endpoint}`, requestPayload);
  const records = extractManagedPreferenceRecords(response.data);
  const payload = pickUpsertedPreferenceRecord(records, item);

  return (
    payload ||
    parsePreferenceContent(serializePreferenceContent(item), {
      id: item.title,
      title: item.title,
      protect: Boolean(item.protect),
      autoEvo: Boolean(item.autoEvo),
      resourceType: item.resourceType,
    })
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
  const requestPayload: {
    suggestion_ids?: string[];
    user_instruct?: string;
  } = {};

  if (input.suggestionIds) {
    requestPayload.suggestion_ids = input.suggestionIds;
  }

  if (normalizedUserInstruct || kind !== "memory") {
    requestPayload.user_instruct = resolveManagedDraftUserInstruct(
      kind,
      normalizedUserInstruct,
    );
  }

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

  return {
    currentContent: toStringValue(payload?.current_content, ""),
    diff: toStringValue(payload?.diff, ""),
    draftContent: toStringValue(payload?.draft_content, ""),
    draftSourceVersion: Number(payload?.draft_source_version || 0),
    draftStatus: toStringValue(payload?.draft_status, ""),
  };
}

export async function confirmPreferenceDraft(): Promise<PreferenceDraftConfirmRecord> {
  return confirmManagedPreferenceDraft("user-preference");
}

export async function confirmManagedPreferenceDraft(
  kind: ManagedPreferenceDraftKind,
): Promise<PreferenceDraftConfirmRecord> {
  const response = await axiosInstance.post(getManagedPreferenceDraftEndpoint(kind, "confirm"));
  const payload = unwrapEnvelope<RawObject>(response.data);

  return {
    content: toStringValue(payload?.content, ""),
    version: Number(payload?.version || 0),
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
  return toBoolean(payload?.discarded, false);
}
