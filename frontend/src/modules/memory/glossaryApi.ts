import { axiosInstance, BASE_URL } from "@/components/request";
import type { GlossaryAsset, GlossarySource } from "./shared";

const coreBasePath = `${BASE_URL}/api/core`;
const defaultPageSize = 200;

type RawObject = Record<string, unknown>;

interface ApiEnvelope<T> {
  code?: number;
  message?: string;
  data?: T;
}

export interface ListGlossaryOptions {
  keyword?: string;
  pageSize?: number;
  pageToken?: string;
  source?: GlossarySource;
}

export interface GlossaryAssetListResult {
  records: GlossaryAsset[];
  total: number;
  nextPageToken: string;
}

export interface CheckGlossaryWordsResult {
  existing: string[];
}

export interface GlossaryConflict {
  id: string;
  word: string;
  description: string;
  reason: string;
  groupIds: string[];
  createdAt: string;
  updatedAt: string;
}

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
    if (["true", "1", "yes", "locked"].includes(normalized)) {
      return true;
    }
    if (["false", "0", "no", "unlocked"].includes(normalized)) {
      return false;
    }
  }
  return fallback;
};

const toStringArray = (value: unknown): string[] => {
  if (Array.isArray(value)) {
    return value
      .map((item) => {
        if (typeof item === "string" || typeof item === "number") {
          return String(item).trim();
        }

        const raw = toRawObject(item);
        return toStringValue(raw?.word ?? raw?.name ?? raw?.term ?? raw?.value, "").trim();
      })
      .filter(Boolean);
  }

  if (typeof value === "string") {
    return value
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean);
  }

  return [];
};

const normalizeGlossarySource = (value: unknown): GlossarySource => {
  const normalized = toStringValue(value, "user").trim().toLowerCase();

  if (
    normalized.includes("ai") ||
    normalized.includes("llm") ||
    normalized.includes("model") ||
    normalized.includes("generated")
  ) {
    return "ai";
  }

  return "user";
};

const extractGlossaryList = (payload: unknown): RawObject[] => {
  const unwrapped = unwrapEnvelope<unknown>(payload);

  if (Array.isArray(unwrapped)) {
    return unwrapped
      .map((item) => toRawObject(item))
      .filter((item): item is RawObject => Boolean(item));
  }

  const rawPayload = toRawObject(unwrapped);
  if (!rawPayload) {
    return [];
  }

  const candidates = [
    rawPayload.items,
    rawPayload.word_groups,
    rawPayload.wordGroups,
    rawPayload.list,
    rawPayload.rows,
    rawPayload.records,
    rawPayload.results,
  ];

  for (const candidate of candidates) {
    if (!Array.isArray(candidate)) {
      continue;
    }

    const items = candidate
      .map((item) => toRawObject(item))
      .filter((item): item is RawObject => Boolean(item));
    if (items.length) {
      return items;
    }
  }

  if (rawPayload.group_id || rawPayload.term || rawPayload.term_id) {
    return [rawPayload];
  }

  return [];
};

export const normalizeGlossaryAsset = (value: unknown): GlossaryAsset | null => {
  const raw = toRawObject(unwrapEnvelope(value));
  if (!raw) {
    return null;
  }

  const id = toStringValue(raw.group_id ?? raw.groupId ?? raw.id ?? raw.term_id, "").trim();
  const term = toStringValue(raw.term ?? raw.word ?? raw.name, "").trim();
  if (!id && !term) {
    return null;
  }

  return {
    id: id || term,
    term: term || id,
    group: toStringValue(
      raw.reference ??
        raw.directory ??
        raw.directory_name ??
        raw.category ??
        raw.group ??
        raw.group_name,
      "",
    ).trim(),
    aliases: toStringArray(raw.aliases),
    source: normalizeGlossarySource(raw.source),
    content: toStringValue(raw.description ?? raw.content ?? raw.summary, "").trim(),
    protect: toBoolean(raw.lock ?? raw.is_locked ?? raw.protect, false),
  };
};

const normalizeGlossaryList = (payload: unknown): GlossaryAsset[] =>
  extractGlossaryList(payload)
    .map((item) => normalizeGlossaryAsset(item))
    .filter((item): item is GlossaryAsset => Boolean(item));

const extractGlossaryConflictList = (payload: unknown): RawObject[] => {
  const unwrapped = unwrapEnvelope<unknown>(payload);

  if (Array.isArray(unwrapped)) {
    return unwrapped
      .map((item) => toRawObject(item))
      .filter((item): item is RawObject => Boolean(item));
  }

  const rawPayload = toRawObject(unwrapped);
  if (!rawPayload) {
    return [];
  }

  const candidates = [
    rawPayload.items,
    rawPayload.conflicts,
    rawPayload.word_group_conflicts,
    rawPayload.wordGroupConflicts,
    rawPayload.list,
    rawPayload.rows,
    rawPayload.records,
    rawPayload.results,
  ];

  for (const candidate of candidates) {
    if (!Array.isArray(candidate)) {
      continue;
    }

    const items = candidate
      .map((item) => toRawObject(item))
      .filter((item): item is RawObject => Boolean(item));
    if (items.length) {
      return items;
    }
  }

  if (rawPayload.id && rawPayload.word) {
    return [rawPayload];
  }

  return [];
};

const normalizeGlossaryConflict = (value: unknown): GlossaryConflict | null => {
  const raw = toRawObject(unwrapEnvelope(value));
  if (!raw) {
    return null;
  }

  const id = toStringValue(raw.id ?? raw.conflict_id ?? raw.conflictId, "").trim();
  const word = toStringValue(raw.word ?? raw.term ?? raw.name, "").trim();
  if (!id || !word) {
    return null;
  }

  return {
    id,
    word,
    description: toStringValue(raw.description ?? raw.content ?? raw.summary, "").trim(),
    reason: toStringValue(raw.reason ?? raw.message ?? raw.remark, "").trim(),
    groupIds: toStringArray(raw.group_ids ?? raw.groupIds),
    createdAt: toStringValue(raw.created_at ?? raw.createdAt, "").trim(),
    updatedAt: toStringValue(raw.updated_at ?? raw.updatedAt, "").trim(),
  };
};

const normalizeGlossaryConflictList = (payload: unknown): GlossaryConflict[] =>
  extractGlossaryConflictList(payload)
    .map((item) => normalizeGlossaryConflict(item))
    .filter((item): item is GlossaryConflict => Boolean(item));

export async function listGlossaryAssets(
  options: ListGlossaryOptions = {},
): Promise<GlossaryAsset[]> {
  const result = await listGlossaryAssetsPage(options);
  return result.records;
}

export async function listGlossaryAssetsPage(
  options: ListGlossaryOptions = {},
): Promise<GlossaryAssetListResult> {
  const keyword = (options.keyword || "").trim();
  const pageSize = options.pageSize || defaultPageSize;
  const pageToken = options.pageToken || "";
  let responseData: unknown;

  if (keyword || options.source) {
    const response = await axiosInstance.post(`${coreBasePath}/word_group:search`, {
      keyword,
      source: options.source || "",
      page_size: pageSize,
      page_token: pageToken,
    });
    responseData = response.data;
  } else {
    const response = await axiosInstance.get(`${coreBasePath}/word_group`, {
      params: {
        page_size: pageSize,
        page_token: pageToken,
      },
    });
    responseData = response.data;
  }

  const payload = unwrapEnvelope<unknown>(responseData);
  const rawPayload = toRawObject(payload);
  const rawEnvelope = toRawObject(responseData);
  const records = normalizeGlossaryList(payload);
  const totalCandidate =
    rawPayload?.total_size ??
    rawPayload?.totalSize ??
    rawPayload?.total ??
    rawEnvelope?.total_size ??
    rawEnvelope?.totalSize ??
    rawEnvelope?.total;
  const nextPageToken = toStringValue(
    rawPayload?.next_page_token ??
      rawPayload?.nextPageToken ??
      rawEnvelope?.next_page_token ??
      rawEnvelope?.nextPageToken ??
      "",
  );

  return {
    records,
    total:
      totalCandidate === undefined || totalCandidate === null
        ? records.length
        : Math.max(0, toNumberValue(totalCandidate, records.length)),
    nextPageToken,
  };
}

export async function getGlossaryAssetDetail(groupId: string): Promise<GlossaryAsset | null> {
  const response = await axiosInstance.get(
    `${coreBasePath}/word_group/${encodeURIComponent(groupId)}`,
  );
  return normalizeGlossaryAsset(response.data);
}

export async function createGlossaryAsset(
  item: GlossaryAsset,
  options?: { conflictId?: string },
): Promise<GlossaryAsset | null> {
  const response = await axiosInstance.post(`${coreBasePath}/word_group`, {
    ...(options?.conflictId ? { id: options.conflictId, conflict: true } : {}),
    term: item.term,
    aliases: item.aliases,
    description: item.content,
    lock: Boolean(item.protect),
  });
  return normalizeGlossaryAsset(response.data);
}

export async function updateGlossaryAsset(item: GlossaryAsset): Promise<GlossaryAsset | null> {
  const response = await axiosInstance.post(`${coreBasePath}/word_group:update`, {
    group_id: item.id,
    term: item.term,
    aliases: item.aliases,
    description: item.content,
    lock: Boolean(item.protect),
  });
  return normalizeGlossaryAsset(response.data);
}

export async function removeGlossaryAsset(groupId: string): Promise<void> {
  await axiosInstance.delete(`${coreBasePath}/word_group/${encodeURIComponent(groupId)}`);
}

export async function batchRemoveGlossaryAssets(groupIds: string[]): Promise<void> {
  await axiosInstance.post(`${coreBasePath}/word_group:batchDelete`, {
    group_ids: groupIds,
  });
}

export interface GlossaryMergeGroupRequest {
  group_ids: string[];
  term: string;
  aliases?: string[];
  description?: string;
}

export async function mergeGlossaryAssets(
  payload: GlossaryMergeGroupRequest,
): Promise<GlossaryAsset | null> {
  const response = await axiosInstance.post(`${coreBasePath}/word_group:merge`, {
    group_ids: payload.group_ids,
    term: payload.term.trim(),
    aliases: payload.aliases || [],
    description: `${payload.description || ""}`.trim(),
  });
  return normalizeGlossaryAsset(response.data);
}

export async function mergeGlossaryAssetsAndAddConflictWord(
  conflict: Pick<GlossaryConflict, "id" | "word" | "groupIds">,
): Promise<GlossaryAsset | null> {
  const response = await axiosInstance.post(`${coreBasePath}/word_group:mergeAndAddWord`, {
    id: conflict.id,
    word: conflict.word,
    group_ids: conflict.groupIds,
  });
  return normalizeGlossaryAsset(response.data);
}

export async function checkGlossaryWordsExist(
  term: string,
  aliases: string[],
): Promise<CheckGlossaryWordsResult> {
  const response = await axiosInstance.post(`${coreBasePath}/word_group:checkExists`, {
    term,
    aliases,
  });
  const payload = unwrapEnvelope<unknown>(response.data);
  const raw = toRawObject(payload);

  return {
    existing: toStringArray(raw?.existing),
  };
}

export async function listGlossaryConflicts(
  options: { pageSize?: number; pageToken?: string } = {},
): Promise<GlossaryConflict[]> {
  const response = await axiosInstance.get(`${coreBasePath}/word_group_conflict`, {
    params: {
      page_size: options.pageSize || defaultPageSize,
      page_token: options.pageToken || "",
    },
  });

  return normalizeGlossaryConflictList(response.data);
}

export async function removeGlossaryConflict(conflictId: string): Promise<void> {
  await axiosInstance.delete(
    `${coreBasePath}/word_group_conflict/${encodeURIComponent(conflictId)}`,
  );
}

export async function addGlossaryConflictToGroups(
  conflict: Pick<GlossaryConflict, "id" | "word" | "groupIds">,
): Promise<void> {
  await axiosInstance.post(`${coreBasePath}/word_group_conflict:addToGroup`, {
    id: conflict.id,
    word: conflict.word,
    group_ids: conflict.groupIds,
  });
}

export interface GlossaryConflictMergeGroupRequest {
  group_ids: string[];
  term: string;
  aliases?: string[];
  description: string;
}

export async function mergeGlossaryConflictAndAddWord(
  payload: {
    id: string;
    word: string;
    group_ids?: string[];
    merges?: GlossaryConflictMergeGroupRequest[];
  },
): Promise<GlossaryAsset[]> {
  const response = await axiosInstance.post(
    `${coreBasePath}/word_group_conflict:mergeAndAddWord`,
    payload,
  );
  const raw = toRawObject(unwrapEnvelope<unknown>(response.data));
  const items = Array.isArray(raw?.items) ? raw.items : [];
  return items
    .map((item) => normalizeGlossaryAsset(item))
    .filter((item): item is GlossaryAsset => Boolean(item));
}

export async function createGlossaryGroupFromConflict(
  payload: {
    id: string;
    word: string;
    term: string;
    aliases?: string[];
    description: string;
    group_ids?: string[];
  },
): Promise<void> {
  await axiosInstance.post(`${coreBasePath}/word_group_conflict:createGroup`, payload);
}
