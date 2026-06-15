import dayjs from "dayjs";
import { diffLines } from "diff";
import type { EvolutionSuggestionRecord } from "./preferenceApi";
import type { SkillShareStatus } from "./skillApi";

export type MemoryTab = "tools" | "skills" | "experience" | "glossary";
export type ModalMode = "add" | "edit" | "view";
export type ShareableTab = "skills" | "experience";
export type ChangeProposalTab = Extract<MemoryTab, "skills" | "experience">;
export type SkillShareCenterTab = "incoming" | "outgoing";
export type SkillShareAction = "accept" | "reject" | "preview";
export type GlossarySource = "user" | "ai";

export const GLOSSARY_TERM_MAX_LENGTH = 50;
export const GLOSSARY_ALIAS_MAX_LENGTH = 50;
export const GLOSSARY_CONTENT_MAX_LENGTH = 300;

export interface BaseAsset {
  id: string;
  content: string;
  protect?: boolean;
  autoEvo?: boolean;
  autoEvoApplyStatus?: string;
  autoEvoGeneration?: number;
  autoEvoError?: string;
}

export interface StructuredAsset extends BaseAsset {
  name: string;
  description: string;
  category: string;
  tags: string[];
  parentId?: string;
  fileExt?: string;
  isEnabled?: boolean;
  builtinSkillUid?: string;
  originBuiltinSkillUid?: string;
  isBuiltinTemplate?: boolean;
  activationStatus?: string;
  readonly?: boolean;
  hasPendingReviewSuggestions?: boolean;
  hasPendingReviewResult?: boolean;
  hasPendingRemoveSuggestion?: boolean;
  reviewStatus?: string;
  suggestionStatus?: string;
  updateStatus?: string;
  nodeType?: string;
}

export interface ExperienceAsset extends BaseAsset {
  title: string;
  agentPersona?: string;
  hasPendingReviewSuggestions?: boolean;
  responseStyle?: string;
  resourceType?: string;
  reviewStatus?: string;
  suggestionStatus?: string;
  userAddress?: string;
}

export interface GlossaryAsset extends BaseAsset {
  term: string;
  group: string;
  aliases: string[];
  source: GlossarySource;
}

export interface GlossaryChangeProposal {
  id: string;
  targetId: string;
  before: GlossaryAsset | null;
  after: GlossaryAsset;
  reason: string;
  mergeFrom?: [GlossaryAsset, GlossaryAsset];
  requiresGroupConfirm?: boolean;
  groupCandidates?: string[];
  backendConflictId?: string;
  backendConflictWord?: string;
  backendConflictGroupIds?: string[];
  backendConflictGroups?: GlossaryAsset[];
}

export type GlossaryConflictResolveMode = "separate" | "merge" | "create";

export interface GlossaryMergeDraft {
  groupIds: string[];
  term: string;
  aliases: string[];
  content: string;
}

export interface GlossaryConflictResolution {
  mode: GlossaryConflictResolveMode;
  selectedGroupIds: string[];
  mergeGroupIds?: string[];
  mergeGroups?: string[][];
  mergeDrafts?: GlossaryMergeDraft[];
  writeGroupIds?: string[];
  newGroupTerm: string;
  newGroupAliases?: string[];
  newGroupContent?: string;
  mergedGroupTerm?: string;
  mergedGroupAliases?: string[];
  mergedGroupContent?: string;
}

export interface AssetDraft {
  id?: string;
  title: string;
  agentPersona: string;
  name: string;
  description: string;
  category: string;
  tags: string[];
  parentId: string;
  childSkills: ChildSkillDraft[];
  term: string;
  group: string;
  aliases: string[];
  source: GlossarySource;
  content: string;
  protect: boolean;
  responseStyle: string;
  userAddress: string;
}

export interface SkillTreeNode extends StructuredAsset {
  children?: SkillTreeNode[];
}

export interface ChildSkillDraft {
  tempId: string;
  name: string;
  description: string;
  tags: string[];
  content: string;
}

export interface ShareRecord {
  groupIds: string[];
  userIds: string[];
  message: string;
}

export interface ShareTarget {
  tab: ShareableTab;
  item: StructuredAsset | ExperienceAsset;
}

export interface StructuredChangeProposal {
  id: string;
  tab: "skills";
  targetId: string;
  before: StructuredAsset;
  after: StructuredAsset;
  backendDraftPreview?: {
    currentContent: string;
    diff: string;
    draftContent: string;
    draftSourceVersion: number;
    draftStatus: string;
  };
  backendSuggestionId?: string;
  backendSuggestionIdsByField?: Partial<Record<ProposalFieldKey, string>>;
  backendSuggestions?: EvolutionSuggestionRecord[];
  backendSuggestionPage?: number;
  backendSuggestionPageSize?: number;
  backendSuggestionTotal?: number;
}

export interface ExperienceChangeProposal {
  id: string;
  tab: "experience";
  targetId: string;
  before: ExperienceAsset;
  after: ExperienceAsset;
  backendDraftPreview?: {
    currentContent: string;
    diff: string;
    draftContent: string;
    draftSourceVersion: number;
    draftStatus: string;
  };
  backendSuggestionId?: string;
  backendSuggestionIdsByField?: Partial<Record<ProposalFieldKey, string>>;
  backendSuggestions?: EvolutionSuggestionRecord[];
  backendSuggestionPage?: number;
  backendSuggestionPageSize?: number;
  backendSuggestionTotal?: number;
}

export type ChangeProposal = StructuredChangeProposal | ExperienceChangeProposal;

export type DiffLineType = "add" | "remove" | "same";

export interface DiffLine {
  type: DiffLineType;
  text: string;
}

export type ProposalFieldKey =
  | "name"
  | "description"
  | "category"
  | "tags"
  | "content"
  | "protect"
  | "title";

export type ProposalFieldDecision = "accept" | "reject" | "pending";

export interface ProposalFieldChange {
  key: ProposalFieldKey;
  label: string;
  before: string;
  after: string;
  backendSuggestionId?: string;
}

export interface StructuredDiffLabels {
  name: string;
  description: string;
  category: string;
  tags: string;
  protect: string;
  content: string;
  yes: string;
  no: string;
}

export interface ExperienceDiffLabels {
  title: string;
  protect: string;
  content: string;
  yes: string;
  no: string;
}

export const isSkillShareActionable = (status: SkillShareStatus) =>
  status === "pending" || status === "unknown";

const normalizeSkillUpdateStatus = (value?: string) => (value || "").trim().toLowerCase();

export const isSkillUpdatePending = (value?: string) =>
  normalizeSkillUpdateStatus(value) === "pending";

export const formatDateTime = (value?: string) => {
  if (!value) {
    return "-";
  }

  const parsed = dayjs(value);
  if (!parsed.isValid()) {
    return value;
  }

  return parsed.format("YYYY-MM-DD HH:mm");
};

export const createDraft = (): AssetDraft => ({
  title: "",
  agentPersona: "",
  name: "",
  description: "",
  category: "",
  tags: [],
  parentId: "",
  childSkills: [],
  term: "",
  group: "",
  aliases: [],
  source: "user",
  content: "",
  protect: false,
  responseStyle: "",
  userAddress: "",
});

export const createStructuredDraft = (
  item: StructuredAsset,
  options?: {
    stripFrontMatter?: boolean;
  },
): AssetDraft => {
  const shouldStripFrontMatter = Boolean(options?.stripFrontMatter);
  const normalizedContent = shouldStripFrontMatter
    ? parseMarkdownFrontMatter(item.content)?.content ?? item.content
    : item.content;

  return {
    id: item.id,
    title: "",
    agentPersona: "",
    name: item.name,
    description: item.description,
    category: item.category,
    tags: item.tags,
    parentId: item.parentId || "",
    childSkills: [],
    term: "",
    group: "",
    aliases: [],
    source: "user",
    content: normalizedContent,
    protect: Boolean(item.protect),
    responseStyle: "",
    userAddress: "",
  };
};

export const createId = (prefix: string) =>
  `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

export const createChildSkillDraft = (): ChildSkillDraft => ({
  tempId: createId("child-skill"),
  name: "",
  description: "",
  tags: [],
  content: "",
});

export const parentSkillUploadAccept = ".md,.markdown";
const parentSkillUploadSuffixes = ["md", "markdown"];
export const skillUploadAccept = ".md,.markdown,.txt,.json,.yaml,.yml";
const skillUploadSuffixes = ["md", "markdown", "txt", "json", "yaml", "yml"];
const markdownFrontMatterPattern = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/;
const frontMatterFieldPattern = /^([a-zA-Z0-9_-]+)\s*:\s*(.*)$/;
const skillDisplayMetaLinePattern =
  /^\s*(?:\*\*)?\s*(?:name|description|名称|描述)\s*(?:\*\*)?\s*[:：].*$/i;
const skillDisplayDividerPattern = /^\s*---\s*$/;

export const getBaseName = (filename: string) => filename.replace(/\.[^/.]+$/, "");

export const canUploadSkillFile = (filename: string, parentOnly = false) => {
  const lowerName = filename.toLowerCase();
  const targetSuffixes = parentOnly ? parentSkillUploadSuffixes : skillUploadSuffixes;
  return targetSuffixes.some((suffix) => lowerName.endsWith(`.${suffix}`));
};

export const isMarkdownSkillFile = (filename: string) => {
  const lowerName = filename.toLowerCase();
  return lowerName.endsWith(".md") || lowerName.endsWith(".markdown");
};

export const parseMarkdownFrontMatter = (content: string) => {
  const matched = content.match(markdownFrontMatterPattern);
  if (!matched) {
    return null;
  }

  const rawFields = matched[1] || "";
  const metadata: Record<string, string> = {};
  rawFields.split(/\r?\n/).forEach((line) => {
    const trimmed = line.trim();
    if (!trimmed) {
      return;
    }
    const fieldMatch = trimmed.match(frontMatterFieldPattern);
    if (!fieldMatch) {
      return;
    }
    const key = fieldMatch[1].trim().toLowerCase();
    const value = fieldMatch[2].trim();
    if (!key) {
      return;
    }
    metadata[key] = value;
  });

  return {
    name: metadata.name || "",
    description: metadata.description || "",
    content: content.slice(matched[0].length),
  };
};

export const getSkillBodyContentForDisplay = (content: string) => {
  if (!content) {
    return "";
  }

  const contentWithoutFrontMatter = parseMarkdownFrontMatter(content)?.content ?? content;
  const normalizedContent = contentWithoutFrontMatter.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const lines = normalizedContent.split("\n");
  let bodyStartIndex = 0;

  while (bodyStartIndex < lines.length) {
    const trimmedLine = lines[bodyStartIndex].trim();

    if (
      !trimmedLine ||
      skillDisplayDividerPattern.test(trimmedLine) ||
      skillDisplayMetaLinePattern.test(trimmedLine)
    ) {
      bodyStartIndex += 1;
      continue;
    }

    break;
  }

  return lines
    .slice(bodyStartIndex)
    .join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .replace(/^\s+/, "");
};

export const inferSkillFileExt = (filename?: string, content?: string) => {
  if (filename) {
    const lowerName = filename.toLowerCase();
    const matched = skillUploadSuffixes.find((suffix) => lowerName.endsWith(`.${suffix}`));
    if (matched) {
      return matched === "markdown" ? "md" : matched;
    }
  }

  const trimmed = (content || "").trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    return "json";
  }

  return "md";
};

export const normalizeTagValues = (values: string[]) =>
  Array.from(
    new Set(
      values
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  );

export const SKILL_TAG_MAX_COUNT = 10;

export const normalizeTextValues = (values: string[]) =>
  Array.from(
    new Set(
      values
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  );

export const initialSkills: StructuredAsset[] = [];

export const initialGlossary: GlossaryAsset[] = [
  {
    id: "glossary-rainfall-threshold",
    term: "雨强阈值",
    group: "降雨监测",
    aliases: ["降雨阈值", "触发雨量阈值"],
    source: "user",
    content: "用于判定地质灾害预警等级的降雨强度临界值。",
    protect: false,
  },
  {
    id: "glossary-rock-pile",
    term: "岩堆体",
    group: "不良地质体",
    aliases: ["崩塌堆积体", "松散堆积体"],
    source: "user",
    content: "常见不良地质体，检索阶段需与边坡失稳风险词联动。",
    protect: true,
  },
  {
    id: "glossary-chainage",
    term: "里程桩号",
    group: "线路定位",
    aliases: ["桩号", "线路里程"],
    source: "ai",
    content: "用于定位铁路线路具体位置的标准标识，通常格式为 Kxx+xxx。",
    protect: false,
  },
];

export const cloneStructuredAsset = (item: StructuredAsset): StructuredAsset => ({
  ...item,
  tags: [...item.tags],
});

export const cloneExperienceAsset = (item: ExperienceAsset): ExperienceAsset => ({
  ...item,
});

export const cloneGlossaryAsset = (item: GlossaryAsset): GlossaryAsset => ({
  ...item,
  aliases: [...item.aliases],
});

export const serializeStructuredAsset = (
  item: StructuredAsset,
  labels: StructuredDiffLabels,
) => {
  const tags = item.tags.length ? item.tags.join(", ") : "-";
  const lines = [
    `${labels.name}: ${item.name}`,
    `${labels.description}: ${item.description}`,
    `${labels.category}: ${item.category || "-"}`,
    `${labels.tags}: ${tags}`,
    `${labels.protect}: ${item.protect ? labels.yes : labels.no}`,
    "",
    `${labels.content}:`,
    item.content,
  ];

  return lines.join("\n");
};

export const serializeExperienceAsset = (
  item: ExperienceAsset,
  labels: ExperienceDiffLabels,
) => {
  const lines = [
    `${labels.title}: ${item.title}`,
    `${labels.protect}: ${item.protect ? labels.yes : labels.no}`,
    "",
    `${labels.content}:`,
    item.content,
  ];

  return lines.join("\n");
};

export const buildDiffLines = (beforeText: string, afterText: string): DiffLine[] => {
  const segments = diffLines(beforeText, afterText);
  const lines: DiffLine[] = [];

  segments.forEach((segment) => {
    const type: DiffLineType = segment.added
      ? "add"
      : segment.removed
        ? "remove"
        : "same";

    segment.value.split("\n").forEach((line, index, allLines) => {
      const isTrailingEmpty = index === allLines.length - 1 && line === "";
      if (isTrailingEmpty) {
        return;
      }
      lines.push({ type, text: line });
    });
  });

  return lines;
};

export const buildUnifiedDiffLines = (diffText: string): DiffLine[] =>
  diffText
    .split("\n")
    .filter((line, index, allLines) => !(index === allLines.length - 1 && line === ""))
    .map((line) => {
      if (line.startsWith("+") && !line.startsWith("+++")) {
        return { type: "add", text: line.slice(1) || " " };
      }
      if (line.startsWith("-") && !line.startsWith("---")) {
        return { type: "remove", text: line.slice(1) || " " };
      }
      if (line.startsWith(" ")) {
        return { type: "same", text: line.slice(1) || " " };
      }
      return { type: "same", text: line || " " };
    });

export const normalizeSuggestionValue = (value: string) => {
  const compact = value.replace(/\s+/g, " ").trim();
  if (!compact) {
    return "-";
  }
  return compact.length > 120 ? `${compact.slice(0, 120)}...` : compact;
};

export const defaultMemoryGenerateInstruction = "再补一条：跨团队协作时才允许使用 merge";

const buildEvolutionId = (resourceType: string, resourceId: string) => {
  const normalizedResourceType = resourceType.trim();
  const normalizedResourceId = resourceId.trim();
  if (!normalizedResourceType || !normalizedResourceId) {
    return "";
  }

  return `${normalizedResourceType}:${normalizedResourceId}`;
};

export const getPreferenceSuggestionResourceParam = (item: ExperienceAsset) => {
  const rawResourceType = (item.resourceType || "").trim();
  const resourceType = rawResourceType.toLowerCase();
  if (resourceType.includes("skill")) {
    return { evolutionId: buildEvolutionId(rawResourceType || "skill", item.id) };
  }
  if (resourceType.includes("memory") && !resourceType.includes("preference")) {
    return { evolutionId: buildEvolutionId(rawResourceType || "memory", item.id) };
  }
  return { evolutionId: buildEvolutionId(rawResourceType || "user-preference", item.id) };
};

export const getSkillSuggestionResourceParam = (item: StructuredAsset) => ({
  evolutionId: buildEvolutionId("skill", item.id),
});

export const buildSkillProposalFromSuggestions = (
  item: StructuredAsset,
  suggestions: EvolutionSuggestionRecord[],
  metadata?: {
    page?: number;
    pageSize?: number;
    total?: number;
  },
): StructuredChangeProposal | null => {
  if (!suggestions.length) {
    return null;
  }

  return {
    id: `skill-suggestions-${suggestions.map((suggestion) => suggestion.id).join("-")}`,
    tab: "skills",
    targetId: item.id,
    backendSuggestionId: suggestions[0].id,
    before: cloneStructuredAsset(item),
    after: cloneStructuredAsset(item),
    backendSuggestions: suggestions,
    backendSuggestionPage: metadata?.page,
    backendSuggestionPageSize: metadata?.pageSize,
    backendSuggestionTotal: metadata?.total ?? suggestions.length,
  };
};

export const buildExperienceProposalFromSuggestions = (
  item: ExperienceAsset,
  suggestions: EvolutionSuggestionRecord[],
  metadata?: {
    page?: number;
    pageSize?: number;
    total?: number;
  },
): ExperienceChangeProposal | null => {
  if (!suggestions.length) {
    return null;
  }

  return {
    id: `experience-suggestions-${suggestions.map((suggestion) => suggestion.id).join("-")}`,
    tab: "experience",
    targetId: item.id,
    backendSuggestionId: suggestions[0].id,
    before: cloneExperienceAsset(item),
    after: cloneExperienceAsset(item),
    backendSuggestions: suggestions,
    backendSuggestionPage: metadata?.page,
    backendSuggestionPageSize: metadata?.pageSize,
    backendSuggestionTotal: metadata?.total ?? suggestions.length,
  };
};

export const initialChangeProposals: ChangeProposal[] = (() => {
  const skillCandidate = initialSkills.find((item) => item.id === "skill-emergency-qa");
  if (!skillCandidate) {
    return [];
  }

  return [
    {
      id: "proposal-skill-emergency-qa",
      tab: "skills",
      targetId: skillCandidate.id,
      before: cloneStructuredAsset(skillCandidate),
      after: {
        ...cloneStructuredAsset(skillCandidate),
        description:
          "突发事件报告分诊模板，新增处置时效与跨部门升级规则，减少遗漏与延迟。",
        tags: ["模板", "研判", "事件流转", "时效"],
        content:
          "# 分诊框架\n\n- 事件类型\n- 风险等级\n- 通知对象\n- 建议动作\n- 升级阈值\n- 处置时效\n- 缺失信息",
      },
    },
  ];
})();

export const initialGlossaryChangeProposals: GlossaryChangeProposal[] = (() => {
  const rainfallItem = initialGlossary.find(
    (item) => item.id === "glossary-rainfall-threshold",
  );
  if (!rainfallItem) {
    return [];
  }

  return [
    {
      id: "glossary-proposal-rainfall-threshold",
      targetId: rainfallItem.id,
      before: cloneGlossaryAsset(rainfallItem),
      after: {
        ...cloneGlossaryAsset(rainfallItem),
        aliases: [...rainfallItem.aliases, "预警雨量阈值"],
        content: "用于判定地质灾害预警等级与触发条件的关键雨强临界值。",
      },
      reason: "根据近期负反馈补全常见别名，并统一术语解释口径。",
    },
    {
      id: "glossary-proposal-new-duration-curve",
      targetId: "glossary-rainfall-duration-curve",
      before: null,
      after: {
        id: "glossary-rainfall-duration-curve",
        term: "雨量历时曲线",
        group: "",
        aliases: ["降雨历时曲线", "雨量-历时曲线"],
        source: "ai",
        content: "用于判断不同历时降雨过程与灾害触发概率关系的分析曲线。",
        protect: false,
      },
      reason: "AI 从近期对话中提炼的高频术语，建议纳入词表以提升召回。",
      requiresGroupConfirm: true,
      groupCandidates: ["降雨监测", "灾害触发机制", "地质风险评估"],
    },
    {
      id: "glossary-proposal-trigger-model",
      targetId: "glossary-rainfall-trigger-model",
      before: null,
      after: {
        id: "glossary-rainfall-trigger-model",
        term: "降雨触发阈值模型",
        group: "灾害触发机制",
        aliases: ["雨强-历时触发模型", "降雨触发模型", "ID 触发模型"],
        source: "user",
        content: "用于灾害触发条件检索与研判的统一词条。",
        protect: false,
      },
      reason: "模型识别到该术语在近期问答中频繁出现，建议新增为独立词条。",
    },
  ];
})();

export const memoryTabOrder: MemoryTab[] = ["skills", "experience", "glossary", "tools"];
export const MEMORY_BASE_PATH = "/memory-management";

export const parseMemoryTab = (value?: string | null): MemoryTab | null => {
  if (value === "tools" || value === "skills" || value === "experience" || value === "glossary") {
    return value;
  }

  return null;
};

export const parseChangeProposalTab = (value?: string | null): ChangeProposalTab | null => {
  if (value === "skills" || value === "experience") {
    return value;
  }

  return null;
};
