import { axiosInstance, BASE_URL } from "@/components/request";

const coreBasePath = `${BASE_URL}/api/core`;

interface ApiEnvelope<T> {
  code?: number;
  message?: string;
  data?: T;
}

interface SkillNode {
  id?: string;
  skill_id?: string;
  skillId?: string;
  name?: string;
  skill_name?: string;
  description?: string;
  desc?: string;
  category?: string;
  tags?: unknown;
  content?: string;
  parent_id?: string;
  parent_skill_id?: string;
  parentSkillId?: string;
  auto_evo?: boolean;
  is_locked?: boolean;
  is_enabled?: boolean;
  file_ext?: string;
  node_type?: string;
  update_status?: string;
  review_status?: string;
  has_pending_review_result?: boolean;
  has_pending_review_suggestions?: boolean;
  has_pending_remove_suggestion?: boolean;
  suggestion_status?: string;
  auto_evo_apply_status?: string;
  auto_evo_generation?: number;
  auto_evo_error?: string;
  builtin_skill_uid?: string;
  builtinSkillUid?: string;
  origin_builtin_skill_uid?: string;
  originBuiltinSkillUid?: string;
  is_builtin_template?: boolean;
  isBuiltinTemplate?: boolean;
  activation_status?: string;
  activationStatus?: string;
  readonly?: boolean;
  children?: SkillNode[];
  [key: string]: unknown;
}

export interface SkillAssetRecord {
  id: string;
  name: string;
  description: string;
  category: string;
  tags: string[];
  content: string;
  parentId?: string;
  protect: boolean;
  autoEvo: boolean;
  isEnabled: boolean;
  fileExt?: string;
  nodeType?: string;
  hasPendingReviewSuggestions?: boolean;
  hasPendingReviewResult?: boolean;
  hasPendingRemoveSuggestion?: boolean;
  reviewStatus?: string;
  suggestionStatus?: string;
  updateStatus?: string;
  autoEvoApplyStatus?: string;
  autoEvoGeneration?: number;
  autoEvoError?: string;
  builtinSkillUid?: string;
  originBuiltinSkillUid?: string;
  isBuiltinTemplate?: boolean;
  activationStatus?: string;
  readonly?: boolean;
}

export interface SkillDraftGeneratePayload {
  suggestionIds?: string[];
  userInstruct: string;
}

export interface SkillDraftPreviewRecord {
  currentContent: string;
  diff: string;
  draftContent: string;
  draftSourceVersion: number;
  draftStatus: string;
  outdated: boolean;
  skillId: string;
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

export interface ShareSkillPayload {
  targetUserIds: string[];
  targetGroupIds: string[];
  message?: string;
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
  skillContent: string;
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

type RawObject = Record<string, unknown>;

const toRawObject = (value: unknown): RawObject | null => {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as RawObject;
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
    if (normalized === "true" || normalized === "1") {
      return true;
    }
    if (normalized === "false" || normalized === "0") {
      return false;
    }
  }
  return fallback;
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

const toStringArray = (value: unknown): string[] => {
  if (Array.isArray(value)) {
    return value
      .map((item) =>
        typeof item === "string"
          ? item.trim()
          : typeof item === "number"
            ? String(item)
            : "",
      )
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

const toNodeArray = (value: unknown): SkillNode[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is SkillNode => Boolean(item && typeof item === "object"));
};

const getFirstValue = (sources: Array<RawObject | null>, keys: string[]): unknown => {
  for (const source of sources) {
    if (!source) {
      continue;
    }

    for (const key of keys) {
      if (!(key in source)) {
        continue;
      }

      const candidate = source[key];
      if (candidate !== undefined && candidate !== null) {
        return candidate;
      }
    }
  }

  return undefined;
};

const getFirstString = (
  sources: Array<RawObject | null>,
  keys: string[],
  fallback = "",
): string => {
  const value = getFirstValue(sources, keys);
  return toStringValue(value, fallback).trim();
};

const getObjectCandidate = (sources: Array<RawObject | null>, keys: string[]): RawObject | null => {
  const value = getFirstValue(sources, keys);
  return toRawObject(value);
};

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

const extractSkillId = (raw: SkillNode): string => {
  return toStringValue(raw.skill_id || raw.skillId || raw.id || raw.name || "");
};

const normalizeSkillNode = (raw: SkillNode, parentId?: string): SkillAssetRecord | null => {
  const id = extractSkillId(raw);
  const name = toStringValue(raw.name || raw.skill_name || "");

  if (!id && !name) {
    return null;
  }

  const resolvedId = id || name;
  const resolvedParentId = toStringValue(
    parentId || raw.parent_skill_id || raw.parentSkillId || raw.parent_id || "",
  );

  return {
    id: resolvedId,
    name: name || resolvedId,
    description: toStringValue(raw.description || raw.desc || ""),
    category: toStringValue(raw.category || ""),
    tags: toStringArray(raw.tags),
    content: toStringValue(raw.content || ""),
    parentId: resolvedParentId || undefined,
    protect: toBoolean(raw.is_locked, false),
    autoEvo: toBoolean(raw.auto_evo, false),
    isEnabled: toBoolean(raw.is_enabled, true),
    fileExt: toStringValue(raw.file_ext || ""),
    nodeType: toStringValue(raw.node_type || ""),
    hasPendingReviewSuggestions: toBoolean(raw.has_pending_review_suggestions, false),
    hasPendingReviewResult: toBoolean(raw.has_pending_review_result, false),
    hasPendingRemoveSuggestion: toBoolean(raw.has_pending_remove_suggestion, false),
    reviewStatus: toStringValue(raw.review_status || ""),
    suggestionStatus: toStringValue(raw.suggestion_status || ""),
    updateStatus: toStringValue(raw.update_status || ""),
    autoEvoApplyStatus: toStringValue(raw.auto_evo_apply_status || ""),
    autoEvoGeneration: typeof raw.auto_evo_generation === "number" ? raw.auto_evo_generation : 0,
    autoEvoError: toStringValue(raw.auto_evo_error || ""),
    builtinSkillUid: toStringValue(raw.builtin_skill_uid || raw.builtinSkillUid || ""),
    originBuiltinSkillUid: toStringValue(
      raw.origin_builtin_skill_uid || raw.originBuiltinSkillUid || "",
    ),
    isBuiltinTemplate: toBoolean(raw.is_builtin_template || raw.isBuiltinTemplate, false),
    activationStatus: toStringValue(raw.activation_status || raw.activationStatus || ""),
    readonly: toBoolean(raw.readonly, false),
  };
};

const flattenSkillNodes = (nodes: SkillNode[], parentId?: string): SkillAssetRecord[] => {
  const flattened: SkillAssetRecord[] = [];

  nodes.forEach((node) => {
    const normalized = normalizeSkillNode(node, parentId);
    if (!normalized) {
      return;
    }

    flattened.push(normalized);
    const children = toNodeArray(node.children);
    if (children.length) {
      flattened.push(...flattenSkillNodes(children, normalized.id));
    }
  });

  return flattened;
};

const extractSkillList = (payload: unknown): SkillNode[] => {
  if (Array.isArray(payload)) {
    return payload as SkillNode[];
  }

  if (!payload || typeof payload !== "object") {
    return [];
  }

  const data = payload as RawObject;
  const candidates = [
    data.skills,
    data.list,
    data.items,
    data.rows,
    data.records,
    data.results,
  ];

  for (const candidate of candidates) {
    const nodes = toNodeArray(candidate);
    if (nodes.length) {
      return nodes;
    }
  }

  return [];
};

const normalizeSkillShareStatus = (value: unknown): SkillShareStatus => {
  const normalized = toStringValue(value, "").trim().toLowerCase();

  if (!normalized) {
    return "pending";
  }

  // Pending-like states (for example `pending_accept`) should stay actionable.
  if (
    normalized.includes("pending") ||
    normalized.includes("wait") ||
    normalized.includes("review") ||
    normalized.includes("queue") ||
    normalized.includes("open") ||
    normalized.includes("new")
  ) {
    return "pending";
  }

  if (
    normalized.includes("fail") ||
    normalized.includes("error") ||
    normalized.includes("timeout")
  ) {
    return "failed";
  }

  if (
    normalized === "accepted" ||
    normalized === "accept" ||
    normalized === "confirm" ||
    normalized === "confirmed" ||
    normalized.includes("accepted") ||
    normalized.includes("confirm_success") ||
    normalized.includes("complete") ||
    normalized.includes("success")
  ) {
    return "accepted";
  }

  if (
    normalized.includes("reject") ||
    normalized.includes("declin") ||
    normalized.includes("discard") ||
    normalized.includes("deny")
  ) {
    return "rejected";
  }

  if (
    normalized.includes("accept") ||
    normalized.includes("approved") ||
    normalized.includes("share")
  ) {
    return "pending";
  }

  return "unknown";
};

const normalizePrincipal = (
  value: unknown,
  typeHint: "user" | "group",
): SkillSharePrincipal | null => {
  if (typeof value === "string" || typeof value === "number") {
    const normalized = String(value).trim();
    if (!normalized) {
      return null;
    }
    return {
      id: normalized,
      name: normalized,
      type: typeHint,
    };
  }

  const raw = toRawObject(value);
  if (!raw) {
    return null;
  }

  const id =
    typeHint === "group"
      ? getFirstString([raw], ["group_id", "groupId", "id"])
      : getFirstString([raw], ["user_id", "userId", "id"]);
  const name =
    typeHint === "group"
      ? getFirstString([raw], ["group_name", "groupName", "name"])
      : getFirstString([raw], [
          "display_name_zh",
          "displayNameZh",
          "display_name_cn",
          "displayNameCn",
          "name_zh",
          "nameZh",
          "nickname",
          "nick_name",
          "nickName",
          "display_name",
          "displayName",
          "username",
          "user_name",
          "userName",
          "name",
        ]);

  if (!id && !name) {
    return null;
  }

  const resolved = id || name;
  return {
    id: resolved,
    name: name || resolved,
    type: typeHint,
  };
};

const zipPrincipalArrays = (
  ids: string[],
  names: string[],
  type: "user" | "group",
): SkillSharePrincipal[] => {
  const maxLength = Math.max(ids.length, names.length);
  const principals: SkillSharePrincipal[] = [];

  for (let index = 0; index < maxLength; index += 1) {
    const id = (ids[index] || names[index] || "").trim();
    const name = (names[index] || ids[index] || "").trim();

    if (!id && !name) {
      continue;
    }

    principals.push({
      id: id || name,
      name: name || id,
      type,
    });
  }

  return principals;
};

const dedupePrincipals = (principals: SkillSharePrincipal[]): SkillSharePrincipal[] => {
  const deduped = new Map<string, SkillSharePrincipal>();

  principals.forEach((principal) => {
    if (!principal.id && !principal.name) {
      return;
    }

    const key = `${principal.type}:${principal.id || principal.name}`;
    const existing = deduped.get(key);
    if (!existing) {
      deduped.set(key, principal);
      return;
    }

    const existingHasDisplayName = existing.name && existing.name !== existing.id;
    const nextHasDisplayName = principal.name && principal.name !== principal.id;
    deduped.set(key, {
      ...existing,
      id: existing.id || principal.id,
      name: existingHasDisplayName
        ? existing.name
        : nextHasDisplayName
          ? principal.name
          : existing.name || principal.name,
    });
  });

  return Array.from(deduped.values());
};

const extractPrincipals = (
  raw: RawObject,
  type: "user" | "group",
  arrayKeys: string[],
  idKeys: string[],
  nameKeys: string[],
  singleKeys: string[] = [],
): SkillSharePrincipal[] => {
  const principals: SkillSharePrincipal[] = [];

  arrayKeys.forEach((key) => {
    const value = raw[key];
    if (Array.isArray(value)) {
      value.forEach((item) => {
        const normalized = normalizePrincipal(item, type);
        if (normalized) {
          principals.push(normalized);
        }
      });
    } else {
      const normalized = normalizePrincipal(value, type);
      if (normalized) {
        principals.push(normalized);
      }
    }
  });

  principals.push(
    ...zipPrincipalArrays(
      idKeys.flatMap((key) => toStringArray(raw[key])),
      nameKeys.flatMap((key) => toStringArray(raw[key])),
      type,
    ),
  );

  singleKeys.forEach((key) => {
    const normalized = normalizePrincipal(raw[key], type);
    if (normalized) {
      principals.push(normalized);
    }
  });

  return dedupePrincipals(principals);
};

const extractShareList = (payload: unknown): RawObject[] => {
  if (Array.isArray(payload)) {
    return payload
      .map((item) => toRawObject(item))
      .filter((item): item is RawObject => Boolean(item));
  }

  const raw = toRawObject(payload);
  if (!raw) {
    return [];
  }

  const candidates = [
    raw.share_items,
    raw.shareItems,
    raw.skill_shares,
    raw.skillShares,
    raw.shares,
    raw.items,
    raw.list,
    raw.rows,
    raw.records,
    raw.results,
    raw.incoming,
    raw.outgoing,
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

  if (
    getFirstString([raw], ["share_item_id", "shareItemId", "id"]) ||
    getFirstString([raw], ["skill_id", "skillId", "skill_name", "skillName"])
  ) {
    return [raw];
  }

  return [];
};

const normalizeSkillShareRecord = (raw: RawObject): SkillShareRecord | null => {
  const shareNode = raw;
  const skillNode =
    getObjectCandidate([raw], [
      "skill",
      "shared_skill",
      "sharedSkill",
      "skill_item",
      "skillItem",
      "item",
      "asset",
      "detail",
      "payload",
      "data",
    ]) || raw;
  const senderNode =
    getObjectCandidate([raw], [
      "sender",
      "from_user",
      "fromUser",
      "share_user",
      "shareUser",
      "creator",
      "owner",
    ]) ||
    toRawObject({
      user_id:
        getFirstString([raw], [
          "sender_user_id",
          "senderUserId",
          "from_user_id",
          "fromUserId",
          "share_user_id",
          "shareUserId",
          "creator_user_id",
          "creatorUserId",
          "source_user_id",
          "sourceUserId",
        ]) || undefined,
      display_name:
        getFirstString([raw], [
          "sender_display_name",
          "senderDisplayName",
          "sender_name",
          "senderName",
          "from_user_name",
          "fromUserName",
          "share_user_name",
          "shareUserName",
          "creator_name",
          "creatorName",
          "source_user_name",
          "sourceUserName",
        ]) || undefined,
      username:
        getFirstString([raw], [
          "sender_username",
          "senderUsername",
          "from_username",
          "fromUsername",
          "creator_username",
          "creatorUsername",
          "source_username",
          "sourceUsername",
        ]) || undefined,
    });

  const recipients = dedupePrincipals([
    ...extractPrincipals(
      raw,
      "user",
      [
        "target_users",
        "targetUsers",
        "users",
        "user_list",
        "userList",
        "receivers",
        "receiver_users",
        "receiverUsers",
      ],
      [
        "target_user_ids",
        "targetUserIds",
        "user_ids",
        "userIds",
        "target_user_id",
        "targetUserId",
      ],
      [
        "target_user_names",
        "targetUserNames",
        "user_names",
        "userNames",
        "target_user_name",
        "targetUserName",
      ],
      [
        "target_user",
        "targetUser",
        "receiver",
        "receiver_user",
        "receiverUser",
      ],
    ),
    ...extractPrincipals(
      raw,
      "group",
      [
        "target_groups",
        "targetGroups",
        "groups",
        "group_list",
        "groupList",
        "receiver_groups",
        "receiverGroups",
      ],
      [
        "target_group_ids",
        "targetGroupIds",
        "group_ids",
        "groupIds",
        "target_group_id",
        "targetGroupId",
      ],
      [
        "target_group_names",
        "targetGroupNames",
        "group_names",
        "groupNames",
        "target_group_name",
        "targetGroupName",
      ],
      [
        "target_group",
        "targetGroup",
        "receiver_group",
        "receiverGroup",
      ],
    ),
  ]);

  const id = getFirstString([shareNode], ["share_item_id", "shareItemId", "item_id", "itemId", "id"]);
  const sourceSkillId = getFirstString([shareNode, skillNode], [
    "source_skill_id",
    "sourceSkillId",
  ]);
  const skillId = getFirstString([shareNode, skillNode], [
    "skill_id",
    "skillId",
    "id",
  ]) || sourceSkillId;
  const skillName = getFirstString([shareNode, skillNode], [
    "skill_name",
    "skillName",
    "source_parent_skill_name",
    "sourceParentSkillName",
    "source_skill_name",
    "sourceSkillName",
    "name",
    "title",
  ]);

  if (!id && !skillId && !skillName) {
    return null;
  }

  const rawStatus = getFirstString([shareNode], [
    "status",
    "share_status",
    "shareStatus",
    "state",
    "decision",
    "result",
  ]);

  return {
    id: id || skillId || skillName,
    skillId: skillId || skillName,
    sourceSkillId,
    skillName: skillName || skillId || id,
    skillDescription: getFirstString([shareNode, skillNode], [
      "skill_description",
      "skillDescription",
      "description",
      "desc",
      "summary",
    ]),
    skillContent: getFirstString([shareNode, skillNode], [
      "skill_content",
      "skillContent",
      "content",
      "markdown",
      "body",
    ]),
    category: getFirstString([shareNode, skillNode], [
      "category",
      "source_category",
      "sourceCategory",
    ]),
    tags: toStringArray(getFirstValue([shareNode, skillNode], ["tags", "labels"])),
    message: getFirstString([shareNode], ["share_message", "shareMessage", "message", "remark", "note"]),
    status: normalizeSkillShareStatus(rawStatus),
    rawStatus,
    errorMessage: getFirstString([shareNode], [
      "error_message",
      "errorMessage",
      "fail_reason",
      "failReason",
    ]),
    sender: normalizePrincipal(senderNode, "user"),
    recipients,
    createdAt: getFirstString([shareNode], [
      "shared_at",
      "sharedAt",
      "create_time",
      "createTime",
      "created_at",
      "createdAt",
    ]),
    updatedAt: getFirstString([shareNode], [
      "updated_at",
      "updatedAt",
      "update_time",
      "updateTime",
      "handled_at",
      "handledAt",
    ]),
    decidedAt: getFirstString([shareNode], [
      "accepted_at",
      "acceptedAt",
      "rejected_at",
      "rejectedAt",
      "decided_at",
      "decidedAt",
      "handled_at",
      "handledAt",
    ]),
  };
};

export async function listSkillAssets(
  options: ListSkillOptions = {},
): Promise<SkillAssetRecord[]> {
  const result = await listSkillAssetsPage(options);
  return result.records;
}

export async function listSkillTags(): Promise<string[]> {
  const response = await axiosInstance.get(`${coreBasePath}/skills/tags`);
  const payload = unwrapEnvelope<unknown>(response.data);
  const rawPayload = toRawObject(payload);
  const rawEnvelope = toRawObject(response.data);
  const tags = toStringArray(
    Array.isArray(payload)
      ? payload
      : getFirstValue([rawPayload, rawEnvelope], [
          "tags",
          "list",
          "items",
          "records",
        ]),
  );

  return [...new Set(tags)].sort((left, right) => left.localeCompare(right));
}

export async function listSkillCategories(): Promise<string[]> {
  const response = await axiosInstance.get(`${coreBasePath}/skills/categories`);
  const payload = unwrapEnvelope<unknown>(response.data);
  const rawPayload = toRawObject(payload);
  const rawEnvelope = toRawObject(response.data);
  const categories = toStringArray(
    Array.isArray(payload)
      ? payload
      : getFirstValue([rawPayload, rawEnvelope], [
          "categories",
          "list",
          "items",
          "records",
        ]),
  );

  return [...new Set(categories)].sort((left, right) => left.localeCompare(right));
}

export async function listSkillAssetsPage(
  options: ListSkillOptions = {},
): Promise<SkillAssetListResult> {
  const params = new URLSearchParams();
  const keyword = options.keyword?.trim();
  const category = options.category?.trim();
  const tags = (options.tags ?? []).map((item) => item.trim()).filter(Boolean);

  params.set("page", String(options.page ?? 1));
  params.set("page_size", String(options.pageSize ?? 200));
  if (keyword) {
    params.set("keyword", keyword);
  }
  if (category) {
    params.set("category", category);
  }
  tags.forEach((item) => {
    params.append("tags", item);
  });

  const response = await axiosInstance.get(`${coreBasePath}/skills`, {
    params,
  });

  const payload = unwrapEnvelope<unknown>(response.data);
  const rawPayload = toRawObject(payload);
  const rawEnvelope = toRawObject(response.data);
  const skillNodes = extractSkillList(payload);
  const flattened = flattenSkillNodes(skillNodes);
  const enabledOnly = flattened.filter((item) => item.isEnabled !== false);
  const deduped = new Map<string, SkillAssetRecord>();

  enabledOnly.forEach((item) => {
    deduped.set(item.id, item);
  });

  const records = Array.from(deduped.values());
  const page = Math.max(1, toNumberValue(rawPayload?.page ?? rawEnvelope?.page, options.page ?? 1));
  const pageSize = Math.max(
    1,
    toNumberValue(
      rawPayload?.page_size ??
        rawPayload?.pageSize ??
        rawEnvelope?.page_size ??
        rawEnvelope?.pageSize,
      options.pageSize ?? 200,
    ),
  );
  const paginationNode = getObjectCandidate([rawPayload, rawEnvelope], [
    "pagination",
    "pager",
    "page_info",
    "pageInfo",
  ]);
  const totalCandidate = getFirstValue(
    [rawPayload, rawEnvelope, paginationNode],
    [
      "total",
      "totle",
      "total_count",
      "totalCount",
      "total_size",
      "totalSize",
      "count",
    ],
  );
  const total =
    totalCandidate === undefined || totalCandidate === null
      ? records.length
      : Math.max(0, toNumberValue(totalCandidate, records.length));

  return {
    records,
    total,
    page,
    pageSize,
  };
}

export async function getSkillAssetDetail(skillId: string): Promise<SkillAssetRecord | null> {
  const response = await axiosInstance.get(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  const rootNode = normalizeSkillNode((payload || {}) as SkillNode);

  if (rootNode) {
    return rootNode;
  }

  const list = extractSkillList(payload);
  if (!list.length) {
    return null;
  }

  const normalized = normalizeSkillNode(list[0]);
  return normalized;
}

export async function createSkillAsset(payload: RawObject) {
  return axiosInstance.post(`${coreBasePath}/skills`, payload);
}

export async function enableBuiltinSkill(
  builtinSkillUid: string,
): Promise<SkillAssetRecord | null> {
  const response = await axiosInstance.post(
    `${coreBasePath}/builtin-skills/${encodeURIComponent(builtinSkillUid)}:enable`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  return normalizeSkillNode((payload || {}) as SkillNode);
}

export async function patchSkillAsset(skillId: string, payload: RawObject) {
  return axiosInstance.patch(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}`,
    payload,
  );
}

export async function generateSkillDraft(
  skillId: string,
  payload: SkillDraftGeneratePayload,
) {
  const requestPayload: {
    suggestion_ids?: string[];
    user_instruct: string;
  } = {
    user_instruct: payload.userInstruct.trim(),
  };

  if (payload.suggestionIds) {
    requestPayload.suggestion_ids = payload.suggestionIds;
  }

  return axiosInstance.post(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}:generate`,
    requestPayload,
  );
}

export async function previewSkillDraft(skillId: string): Promise<SkillDraftPreviewRecord> {
  const response = await axiosInstance.get(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}:draft-preview`,
  );
  const payload = unwrapEnvelope<RawObject>(response.data);

  return {
    currentContent: toStringValue(payload?.current_content, ""),
    diff: toStringValue(payload?.diff, ""),
    draftContent: toStringValue(payload?.draft_content, ""),
    draftSourceVersion: Number(payload?.draft_source_version || 0),
    draftStatus: toStringValue(payload?.draft_status, ""),
    outdated: toBoolean(payload?.outdated, false),
    skillId: toStringValue(payload?.skill_id, skillId),
  };
}

export async function confirmSkillDraft(skillId: string): Promise<SkillAssetRecord | null> {
  const response = await axiosInstance.post(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}:confirm`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  return normalizeSkillNode((payload || {}) as SkillNode);
}

export async function discardSkillDraft(skillId: string): Promise<boolean> {
  const response = await axiosInstance.post(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}:discard`,
  );
  const payload = unwrapEnvelope<RawObject>(response.data);
  return toBoolean(payload?.discarded, false);
}

export async function removeSkillAsset(skillId: string) {
  return axiosInstance.delete(`${coreBasePath}/skills/${encodeURIComponent(skillId)}`);
}

export async function disableSkillAsset(skillId: string) {
  return removeSkillAsset(skillId);
}

export async function shareSkillAsset(skillId: string, payload: ShareSkillPayload) {
  return axiosInstance.post(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}:share`,
    {
      target_user_ids: payload.targetUserIds,
      target_group_ids: payload.targetGroupIds,
      message: payload.message || "",
    },
  );
}

async function listSkillShares(path: string): Promise<SkillShareRecord[]> {
  const response = await axiosInstance.get(`${coreBasePath}${path}`);
  const payload = unwrapEnvelope<unknown>(response.data);
  const shares = extractShareList(payload)
    .map((item) => normalizeSkillShareRecord(item))
    .filter((item): item is SkillShareRecord => Boolean(item));
  const deduped = new Map<string, SkillShareRecord>();

  shares.forEach((item) => {
    deduped.set(item.id, item);
  });

  return Array.from(deduped.values());
}

export async function listIncomingSkillShares(): Promise<SkillShareRecord[]> {
  return listSkillShares("/skill-shares/incoming");
}

export async function listOutgoingSkillShares(): Promise<SkillShareRecord[]> {
  return listSkillShares("/skill-shares/outgoing");
}

export async function listSkillShareTargets(skillId: string): Promise<SkillShareRecord[]> {
  const response = await axiosInstance.get(
    `${coreBasePath}/skills/${encodeURIComponent(skillId)}:shares`,
    {
      params: {
        page: 1,
        page_size: 200,
      },
    },
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  const shares = extractShareList(payload)
    .map((item) => normalizeSkillShareRecord(item))
    .filter((item): item is SkillShareRecord => Boolean(item));
  const deduped = new Map<string, SkillShareRecord>();

  shares.forEach((item) => {
    const primaryRecipient = item.recipients[0];
    const dedupeKey = primaryRecipient
      ? `${primaryRecipient.type}:${primaryRecipient.id || primaryRecipient.name}`
      : item.id;
    deduped.set(dedupeKey, item);
  });

  return Array.from(deduped.values());
}

export async function getSkillShareDetail(shareItemId: string): Promise<SkillShareRecord | null> {
  const response = await axiosInstance.get(
    `${coreBasePath}/skill-shares/${encodeURIComponent(shareItemId)}`,
  );
  const payload = unwrapEnvelope<unknown>(response.data);
  const normalized = normalizeSkillShareRecord(toRawObject(payload) || {});

  if (normalized) {
    return normalized;
  }

  const shares = extractShareList(payload)
    .map((item) => normalizeSkillShareRecord(item))
    .filter((item): item is SkillShareRecord => Boolean(item));

  return shares[0] || null;
}

export async function acceptSkillShare(shareItemId: string) {
  return axiosInstance.post(
    `${coreBasePath}/skill-shares/${encodeURIComponent(shareItemId)}:accept`,
  );
}

export async function rejectSkillShare(shareItemId: string) {
  return axiosInstance.post(
    `${coreBasePath}/skill-shares/${encodeURIComponent(shareItemId)}:reject`,
  );
}
