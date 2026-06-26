import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  Button,
  Input,
  Modal,
  Space,
  Switch,
  Tag,
  Tooltip,
  Upload,
  type UploadProps,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  AppstoreOutlined,
  BookOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  HistoryOutlined,
  LinkOutlined,
  LockOutlined,
} from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import { useTranslation } from "react-i18next";
import {
  Outlet,
  useLocation,
  useMatch,
  useNavigate,
  useSearchParams,
} from "react-router-dom";
import type { GroupItem, UserItem } from "@/api/generated/auth-client";
import { createGroupApi, createUserApi } from "@/modules/signin/utils/request";
import GlossaryInboxModal from "./components/GlossaryInboxModal";
import MemoryDraftModal from "./components/MemoryDraftModal";
import ShareModal from "./components/ShareModal";
import SkillShareCenterModal from "./components/SkillShareCenterModal";
import {
  acceptSkillShare,
  buildSkillUpdatePayload,
  confirmSkillDraft,
  createSkillAsset,
  discardSkillDraft,
  enableBuiltinSkill,
  generateSkillDraft,
  getSkillAssetDetail,
  listIncomingSkillShares,
  listOutgoingSkillShares,
  listSkillShareTargets,
  listSkillAssetsPage,
  listSkillCategories,
  listSkillTags,
  patchSkillAsset,
  previewSkillDraft,
  rejectSkillShare,
  removeSkillAsset,
  shareSkillAsset,
  type SkillAssetRecord,
  type SkillShareRecord,
  type SkillShareStatus,
} from "./skillApi";
import {
  approveEvolutionSuggestion,
  batchApproveEvolutionSuggestions,
  batchRejectEvolutionSuggestions,
  confirmManagedPreferenceDraft,
  discardManagedPreferenceDraft,
  generateManagedPreferenceDraft,
  getPersonalizationSetting,
  listPreferenceAssets,
  listEvolutionSuggestions,
  previewManagedPreferenceDraft,
  rejectEvolutionSuggestion,
  resolveManagedPreferenceDraftKind,
  upsertPreferenceAsset,
  updatePersonalizationSetting,
  type EvolutionSuggestionListResult,
  type EvolutionSuggestionRecord,
  type ManagedPreferenceDraftKind,
  type PreferenceDraftPreviewRecord,
} from "./preferenceApi";
import {
  addGlossaryConflictToGroups,
  batchRemoveGlossaryAssets,
  checkGlossaryWordsExist,
  createGlossaryGroupFromConflict,
  createGlossaryAsset,
  getGlossaryAssetDetail,
  listGlossaryAssetsPage,
  listGlossaryConflicts,
  mergeGlossaryAssets,
  mergeGlossaryConflictAndAddWord,
  removeGlossaryConflict,
  removeGlossaryAsset,
  updateGlossaryAsset,
  type GlossaryConflict,
} from "./glossaryApi";
import {
  type AssetDraft,
  type ChangeProposal,
  type ChangeProposalTab,
  type ChildSkillDraft,
  type ExperienceAsset,
  type ExperienceChangeProposal,
  type GlossaryAsset,
  type GlossaryChangeProposal,
  type GlossaryConflictResolution,
  type GlossarySource,
  type MemoryTab,
  type ModalMode,
  type ProposalFieldChange,
  type ProposalFieldDecision,
  type ProposalFieldKey,
  type ShareRecord,
  type ShareTarget,
  type ShareableTab,
  type SkillShareAction,
  type SkillShareCenterTab,
  type SkillTreeNode,
  type StructuredAsset,
  GLOSSARY_ALIAS_MAX_LENGTH,
  GLOSSARY_CONTENT_MAX_LENGTH,
  GLOSSARY_TERM_MAX_LENGTH,
  MEMORY_BASE_PATH,
  buildDiffLinesWithInline,
  buildUnifiedDiffLines,
  canUploadSkillFile,
  cloneExperienceAsset,
  cloneGlossaryAsset,
  cloneStructuredAsset,
  createChildSkillDraft,
  createDraft,
  createId,
  createStructuredDraft,
  formatDateTime,
  getBaseName,
  getPreferenceSuggestionResourceParam,
  getSkillBodyContentForDisplay,
  inferSkillFileExt,
  initialChangeProposals,
  initialSkills,
  isMarkdownSkillFile,
  isSkillShareActionable,
  isSkillUpdatePending,
  memoryTabOrder,
  normalizeSuggestionValue,
  normalizeTagValues,
  normalizeTextValues,
  parentSkillUploadAccept,
  parseChangeProposalTab,
  parseMarkdownFrontMatter,
  parseMemoryTab,
  serializeExperienceAsset,
  serializeStructuredAsset,
  serializePreferenceYaml,
  parsePreferenceYamlAndBody,
  SKILL_TAG_MAX_COUNT,
  skillUploadAccept,
} from "./shared";
import "./index.scss";

const backendSuggestionPageSize = 20;
const defaultSkillListPageSize = 6;
const parentSkillOptionPageSize = 200;
const defaultGlossaryListPageSize = 4;
const showGlossaryInboxUi = true;
const MERGED_GLOSSARY_GROUP_OPTION_ID = "__merged_glossary_group__";
const MERGED_GLOSSARY_GROUP_OPTION_ID_PREFIX = `${MERGED_GLOSSARY_GROUP_OPTION_ID}:`;
const NEW_GLOSSARY_GROUP_OPTION_ID = "__new_glossary_group__";
const isReviewableSuggestionStatus = (status?: string) => {
  const normalized = String(status || "").trim().toLowerCase();
  return normalized === "pending";
};
const isPendingReviewStatus = (status?: string) =>
  String(status || "").trim().toLowerCase() === "pending";
const isSkillRemoveSuggestion = (suggestion: EvolutionSuggestionRecord) =>
  String(suggestion.action || "").trim().toLowerCase() === "remove";
const normalizeAutoEvoApplyStatus = (status?: string) =>
  String(status || "").trim().toLowerCase();
const getAutoEvoStatusMeta = (status?: string) => {
  const normalizedStatus = normalizeAutoEvoApplyStatus(status);
  if (normalizedStatus === "running") {
    return {
      color: "blue" as const,
      textKey: "admin.memoryAutoEvoStatusRunning",
    };
  }
  if (normalizedStatus === "failed") {
    return {
      color: "red" as const,
      textKey: "admin.memoryAutoEvoStatusFailed",
    };
  }
  return {
    color: "blue" as const,
    textKey: "admin.memoryAutoEvoStatusWaiting",
  };
};

const mapSkillAssetRecordToStructuredAsset = (
  item: SkillAssetRecord,
): StructuredAsset => ({
  id: item.id,
  name: item.name,
  description: item.description,
  category: item.category,
  tags: item.tags,
  content: item.content,
  parentId: item.parentId,
  parentSkillName: item.parentSkillName,
  protect: item.protect,
  autoEvo: item.autoEvo,
  autoEvoApplyStatus: item.autoEvoApplyStatus,
  autoEvoGeneration: item.autoEvoGeneration,
  autoEvoError: item.autoEvoError,
  fileExt: item.fileExt,
  isEnabled: item.isEnabled,
  hasPendingReviewSuggestions: item.hasPendingReviewSuggestions,
  hasPendingReviewResult: item.hasPendingReviewResult,
  hasPendingRemoveSuggestion: item.hasPendingRemoveSuggestion,
  reviewStatus: item.reviewStatus,
  suggestionStatus: item.suggestionStatus,
  nodeType: item.nodeType,
  updateStatus: item.updateStatus,
  builtinSkillUid: item.builtinSkillUid,
  originBuiltinSkillUid: item.originBuiltinSkillUid,
  isBuiltinTemplate: item.isBuiltinTemplate,
  activationStatus: item.activationStatus,
  readonly: item.readonly,
});
const hasDraftPreviewStatus = (record: ExperienceAsset) =>
  isPendingReviewStatus(record.reviewStatus);
const hasSkillDraftPreviewStatus = (record: StructuredAsset) =>
  Boolean(record.hasPendingReviewResult) ||
  Boolean(record.hasPendingReviewSuggestions) ||
  isReviewableSuggestionStatus(record.reviewStatus) ||
  isReviewableSuggestionStatus(record.suggestionStatus) ||
  isSkillUpdatePending(record.updateStatus);
type ExperienceProfileFieldKey = "agentPersona" | "preferredName" | "responseStyle";
type ExperienceProfileDraft = Record<ExperienceProfileFieldKey, string>;
type ExperienceProfileFieldConfig = {
  key: ExperienceProfileFieldKey;
  label: string;
  description: string;
  placeholder: string;
};
type ExperienceProfileEditTarget = {
  recordId: string;
  fieldKey: ExperienceProfileFieldKey;
};
const USER_PROFILE_FIELD_MAX_LENGTH = 500;
const getExperienceProfileDraft = (record: ExperienceAsset): ExperienceProfileDraft => ({
  agentPersona: record.agentPersona || "",
  preferredName: record.preferredName || "",
  responseStyle: record.responseStyle || "",
});
const isExperienceProfileAsset = (record: ExperienceAsset) => {
  const resourceType = String(record.resourceType || "").toLowerCase();
  return (
    resourceType.includes("user_preference") ||
    resourceType.includes("user-preference") ||
    resourceType.includes("preference") ||
    record.title === "用户画像"
  );
};

const mergeEvolutionSuggestionRecords = (
  current: EvolutionSuggestionRecord[],
  incoming: EvolutionSuggestionRecord[],
) => {
  const seenIds = new Set(current.map((item) => item.id));
  const merged = [...current];

  incoming.forEach((item) => {
    if (seenIds.has(item.id)) {
      return;
    }
    seenIds.add(item.id);
    merged.push(item);
  });

  return merged;
};

export default function MemoryManagement() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabRouteMatch = useMatch(`${MEMORY_BASE_PATH}/:tab`);
  const skillDetailMatch = useMatch(`${MEMORY_BASE_PATH}/skills/:itemId`);
  const experienceDetailMatch = useMatch(`${MEMORY_BASE_PATH}/experience/:itemId`);
  const glossaryDetailMatch = useMatch(`${MEMORY_BASE_PATH}/glossary/:itemId`);
  const reviewRouteMatch = useMatch(`${MEMORY_BASE_PATH}/review/:tab/:itemId`);
  const reviewRouteReloadKeyRef = useRef("");
  const skillRouteItemId = skillDetailMatch?.params.itemId;
  const experienceRouteItemId = experienceDetailMatch?.params.itemId;
  const glossaryRouteItemId = glossaryDetailMatch?.params.itemId;
  const reviewRouteTab = parseChangeProposalTab(reviewRouteMatch?.params.tab);
  const reviewRouteItemId = reviewRouteMatch?.params.itemId;
  const isReviewRouteRequested = Boolean(reviewRouteTab && reviewRouteItemId);
  const routeListTab = parseMemoryTab(tabRouteMatch?.params.tab);
  const queryRouteTab = parseMemoryTab(searchParams.get("tab"));
  const routeMemoryTab =
    (skillRouteItemId
      ? "skills"
      : experienceRouteItemId
      ? "experience"
      : glossaryRouteItemId
      ? "glossary"
      : reviewRouteTab ||
        routeListTab ||
        queryRouteTab ||
        "skills") as MemoryTab;
  const initialGlossaryDetailTarget = null;
  const initialReviewProposalId = (() => {
    const routeTab = parseChangeProposalTab(reviewRouteMatch?.params.tab);
    const routeItemId = reviewRouteMatch?.params.itemId;
    if (!routeTab || !routeItemId) {
      return undefined;
    }

    return initialChangeProposals.find(
      (item) => item.tab === routeTab && item.targetId === routeItemId,
    )?.id;
  })();
  const [activeTab, setActiveTab] = useState<MemoryTab>(routeMemoryTab);
  const [skillAssets, setSkillAssets] = useState<StructuredAsset[]>(initialSkills);
  const [parentSkillAssets, setParentSkillAssets] =
    useState<StructuredAsset[]>(initialSkills);
  const [parentSkillOptionsLoading, setParentSkillOptionsLoading] = useState(false);
  const [skillLoading, setSkillLoading] = useState(false);
  const [skillCategories, setSkillCategories] = useState<string[]>([]);
  const [skillCategoriesLoaded, setSkillCategoriesLoaded] = useState(false);
  const [skillCategoriesLoading, setSkillCategoriesLoading] = useState(false);
  const [skillTags, setSkillTags] = useState<string[]>([]);
  const [skillTagsLoaded, setSkillTagsLoaded] = useState(false);
  const [skillTagsLoading, setSkillTagsLoading] = useState(false);
  const [skillAutoEvoLoading, setSkillAutoEvoLoading] = useState<Set<string>>(new Set());
  const [builtinSkillEnableLoading, setBuiltinSkillEnableLoading] = useState<Set<string>>(new Set());
  const [skillsInitialized, setSkillsInitialized] = useState(false);
  const skillListRequestIdRef = useRef(0);
  const parentSkillListRequestIdRef = useRef(0);
  const skillListRouteLocationKeyRef = useRef("");
  const skillListRefreshKeyRef = useRef("");
  const experienceSectionRefreshKeyRef = useRef("");
  const glossaryAssetsRefreshKeyRef = useRef("");
  const glossaryAssetsFilterKeyRef = useRef("");
  const glossaryAssetsRouteLocationKeyRef = useRef("");
  const glossaryConflictsRefreshKeyRef = useRef("");
  const [skillListPage, setSkillListPage] = useState(1);
  const [skillListPageSize, setSkillListPageSize] = useState(defaultSkillListPageSize);
  const [skillListTotal, setSkillListTotal] = useState(initialSkills.length);
  const [experienceAssets, setExperienceAssets] = useState<ExperienceAsset[]>([]);
  const [experienceFeatureEnabled, setExperienceFeatureEnabled] = useState(true);
  const [experienceLoading, setExperienceLoading] = useState(false);
  const [experienceAutoEvoLoading, setExperienceAutoEvoLoading] = useState<Set<string>>(new Set());
  const [experienceInitialized, setExperienceInitialized] = useState(false);
  const [experienceSaving, setExperienceSaving] = useState(false);
  const [experienceProfileDrafts, setExperienceProfileDrafts] = useState<
    Record<string, ExperienceProfileDraft>
  >({});
  const [experienceProfileSaving, setExperienceProfileSaving] = useState<Set<string>>(
    new Set(),
  );
  const [expandedExperienceProfileIds, setExpandedExperienceProfileIds] = useState<string[]>(
    [],
  );
  const [experienceProfileEditTarget, setExperienceProfileEditTarget] =
    useState<ExperienceProfileEditTarget | null>(null);
  const [experienceSettingSaving, setExperienceSettingSaving] = useState(false);
  const [glossaryAssets, setGlossaryAssets] = useState<GlossaryAsset[]>([]);
  const [glossaryLoading, setGlossaryLoading] = useState(false);
  const [glossaryInitialized, setGlossaryInitialized] = useState(false);
  const [glossaryLoadError, setGlossaryLoadError] = useState("");
  const [glossarySaving, setGlossarySaving] = useState(false);
  const [glossaryListPage, setGlossaryListPage] = useState(1);
  const [glossaryListPageSize, setGlossaryListPageSize] = useState(
    defaultGlossaryListPageSize,
  );
  const [glossaryListTotal, setGlossaryListTotal] = useState(0);
  const [query, setQuery] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [category, setCategory] = useState<string>();
  const [tag, setTag] = useState<string>();
  const skillKeyword = query.trim();
  const [glossarySource, setGlossarySource] = useState<GlossarySource>();
  const [glossaryInboxOpen, setGlossaryInboxOpen] = useState(false);
  const [glossaryInboxLoading, setGlossaryInboxLoading] = useState(false);
  const [glossaryInboxError, setGlossaryInboxError] = useState("");
  const [glossaryInboxSubmitting, setGlossaryInboxSubmitting] = useState<
    "" | "accept" | "reject"
  >("");
  const [selectedGlossaryAssetIds, setSelectedGlossaryAssetIds] = useState<string[]>([]);
  const [pendingGlossaryMergeSourceIds, setPendingGlossaryMergeSourceIds] = useState<string[]>(
    [],
  );
  const [glossaryDetailTarget, setGlossaryDetailTarget] =
    useState<GlossaryAsset | null>(initialGlossaryDetailTarget);
  const [modalMode, setModalMode] = useState<ModalMode>("view");
  const [draft, setDraft] = useState<AssetDraft>(createDraft());
  const [modalOpen, setModalOpen] = useState(false);
  const [shareModalOpen, setShareModalOpen] = useState(false);
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  const [skillShareCenterOpen, setSkillShareCenterOpen] = useState(false);
  const [skillShareCenterTab, setSkillShareCenterTab] =
    useState<SkillShareCenterTab>("incoming");
  const [incomingSkillShares, setIncomingSkillShares] = useState<SkillShareRecord[]>([]);
  const [outgoingSkillShares, setOutgoingSkillShares] = useState<SkillShareRecord[]>([]);
  const [skillShareCenterLoading, setSkillShareCenterLoading] = useState(false);
  const [skillShareCenterError, setSkillShareCenterError] = useState("");
  const [skillShareActionState, setSkillShareActionState] = useState<
    Record<string, SkillShareAction | undefined>
  >({});
  const [changeProposals, setChangeProposals] =
    useState<ChangeProposal[]>(initialChangeProposals);
  const [reviewSuggestionLoadingId, setReviewSuggestionLoadingId] = useState("");
  const [backendSuggestionLoadingMore, setBackendSuggestionLoadingMore] = useState(false);
  const [backendSuggestionLoadMoreError, setBackendSuggestionLoadMoreError] = useState("");
  const [reviewSuggestionSubmitting, setReviewSuggestionSubmitting] = useState(false);
  const [fieldDecisionSubmitting, setFieldDecisionSubmitting] = useState<
    Record<string, ProposalFieldDecision | undefined>
  >({});
  const backendSuggestionMutationLockRef = useRef(false);
  const [backendSuggestionSubmitting, setBackendSuggestionSubmitting] = useState<
    Record<string, ProposalFieldDecision | undefined>
  >({});
  const [backendSuggestionBatchSubmitting, setBackendSuggestionBatchSubmitting] = useState<
    "" | "accept" | "reject"
  >("");
  const [selectedBackendSuggestionIds, setSelectedBackendSuggestionIds] = useState<string[]>([]);
  const [reviewedBackendSuggestionIds, setReviewedBackendSuggestionIds] = useState<
    string[]
  >([]);
  const [approvedBackendSuggestionIds, setApprovedBackendSuggestionIds] = useState<string[]>(
    [],
  );
  const [rejectedBackendSuggestionIds, setRejectedBackendSuggestionIds] = useState<string[]>(
    [],
  );
  const [backendDraftKind, setBackendDraftKind] =
    useState<ManagedPreferenceDraftKind>("user-preference");
  const [backendDraftPreview, setBackendDraftPreview] =
    useState<PreferenceDraftPreviewRecord | null>(null);
  const [backendDraftLoading, setBackendDraftLoading] = useState(false);
  const [backendDraftSubmitting, setBackendDraftSubmitting] = useState<
    "confirm" | "discard" | ""
  >("");
  const [glossaryChangeProposals, setGlossaryChangeProposals] =
    useState<GlossaryChangeProposal[]>([]);
  const [activeProposalId, setActiveProposalId] = useState<string | undefined>(
    initialReviewProposalId,
  );
  const [activeReviewStep, setActiveReviewStep] = useState<0 | 1>(0);
  const [proposalFieldDecisions, setProposalFieldDecisions] =
    useState<Record<string, ProposalFieldDecision>>({});
  const [selectedFieldKeys, setSelectedFieldKeys] = useState<ProposalFieldKey[]>([]);
  const [manualMergedDraft, setManualMergedDraft] =
    useState<StructuredAsset | ExperienceAsset | null>(null);
  const [isPreviewContentEditing, setIsPreviewContentEditing] = useState(false);
  const [manualPreviewContentDraft, setManualPreviewContentDraft] = useState("");
  const [qaQuestionDraft, setQaQuestionDraft] = useState("");
  const [shareDraft, setShareDraft] = useState<ShareRecord>({
    groupIds: [],
    userIds: [],
    message: "",
  });
  const [shareUsers, setShareUsers] = useState<UserItem[]>([]);
  const [shareGroups, setShareGroups] = useState<GroupItem[]>([]);
  const [shareLoading, setShareLoading] = useState(false);
  const [shareStatusLoading, setShareStatusLoading] = useState(false);
  const [shareStatusError, setShareStatusError] = useState("");
  const [shareStatusRecords, setShareStatusRecords] = useState<SkillShareRecord[]>([]);
  const handledShareKeyRef = useRef("");
  const skillShareRequestIdRef = useRef(0);
  const shareStatusRequestIdRef = useRef(0);
  const glossaryRequestIdRef = useRef(0);
  const glossaryConflictRequestIdRef = useRef(0);
  const backendSuggestionLoadMoreRequestIdRef = useRef(0);
  const confirmedDraftProposalIdsRef = useRef<Set<string>>(new Set());
  const activeProposalFieldChangesRef = useRef<ProposalFieldChange[]>([]);

  const tabMeta: Record<
    MemoryTab,
    { title: string; description: string; unit: string; icon: ReactNode }
  > = {
    skills: {
      title: t("admin.memoryTabSkills"),
      description: t("admin.memoryTabSkillsDesc"),
      unit: t("admin.memoryUnitSkill"),
      icon: <AppstoreOutlined />,
    },
    experience: {
      title: t("admin.memoryTabExperience"),
      description: t("admin.memoryTabExperienceDesc"),
      unit: t("admin.memoryUnitExperience"),
      icon: <HistoryOutlined />,
    },
    glossary: {
      title: t("admin.memoryTabGlossary"),
      description: t("admin.memoryTabGlossaryDesc"),
      unit: t("admin.memoryUnitGlossary"),
      icon: <BookOutlined />,
    },
  };

  const currentTabMeta = tabMeta[activeTab];
  const currentStructuredItems = activeTab === "skills" ? skillAssets : [];
  const parentSkillCandidateAssets =
    parentSkillAssets.length > 0 ? parentSkillAssets : skillAssets;

  const topLevelSkills = useMemo(
    () => parentSkillCandidateAssets.filter((item) => !item.parentId),
    [parentSkillCandidateAssets],
  );
  const parentSkillOptions = useMemo(
    () =>
      topLevelSkills
        .filter((item) => item.id !== draft.id && !item.isBuiltinTemplate)
        .map((item) => ({
          label: item.name,
          value: item.id,
        })),
    [draft.id, topLevelSkills],
  );
  const resolveSkillParentName = useCallback(
    (item: StructuredAsset) =>
      item.parentSkillName ||
      (item.parentId
        ? parentSkillCandidateAssets.find((candidate) => candidate.id === item.parentId)?.name || ""
        : ""),
    [parentSkillCandidateAssets],
  );
  const buildSkillPatchPayload = useCallback(
    (item: StructuredAsset, overrides: Record<string, unknown> = {}) =>
      buildSkillUpdatePayload(
        {
          ...item,
          content: item.parentId ? item.content : getSkillBodyContentForDisplay(item.content || ""),
          parentSkillName: resolveSkillParentName(item),
        },
        {
          is_locked: Boolean(item.protect),
          ...overrides,
        },
      ),
    [resolveSkillParentName],
  );

  const localAvailableCategories = [...new Set(currentStructuredItems.map((item) => item.category))]
    .filter(Boolean)
    .sort((left, right) => left.localeCompare(right));
  const availableCategories =
    activeTab === "skills" && skillCategoriesLoaded
      ? skillCategories
      : localAvailableCategories;
  const localAvailableTags = [
    ...new Set(currentStructuredItems.flatMap((item) => item.tags)),
  ].sort((left, right) => left.localeCompare(right));
  const availableTags =
    activeTab === "skills" && skillTagsLoaded
      ? skillTags
      : localAvailableTags;

  const shareableItems = useMemo(
    () => ({
      skills: skillAssets,
      experience: experienceAssets,
    }),
    [experienceAssets, skillAssets],
  );
  const buildMemoryTabPath = useCallback(
    (tab?: MemoryTab) => (tab ? `${MEMORY_BASE_PATH}/${tab}` : MEMORY_BASE_PATH),
    [],
  );
  const buildMemorySearch = useCallback((tab?: MemoryTab, itemId?: string) => {
    const nextSearchParams = new URLSearchParams();

    if (tab) {
      nextSearchParams.set("tab", tab);
    }

    if (itemId) {
      nextSearchParams.set("item", itemId);
    }

    const search = nextSearchParams.toString();
    return search ? `?${search}` : "";
  }, []);
  const navigateToMemoryList = useCallback(
    (tab?: MemoryTab, options?: { replace?: boolean }) => {
      navigate(
        {
          pathname: buildMemoryTabPath(tab || "skills"),
          search: buildMemorySearch(),
        },
        { replace: options?.replace },
      );
    },
    [buildMemorySearch, buildMemoryTabPath, navigate],
  );
  const navigateToGlossaryDetail = useCallback(
    (itemId: string) => {
      navigate({
        pathname: `${MEMORY_BASE_PATH}/glossary/${itemId}`,
      });
    },
    [navigate],
  );
  const navigateToSkillDetail = useCallback(
    (itemId: string) => {
      navigate({
        pathname: `${MEMORY_BASE_PATH}/skills/${encodeURIComponent(itemId)}`,
      });
    },
    [navigate],
  );
  const navigateToExperienceDetail = useCallback(
    (itemId: string) => {
      navigate({
        pathname: `${MEMORY_BASE_PATH}/experience/${encodeURIComponent(itemId)}`,
      });
    },
    [navigate],
  );
  const navigateToChangeReview = useCallback(
    (tab: ChangeProposalTab, itemId: string, options?: { replace?: boolean }) => {
      navigate(
        {
          pathname: `${MEMORY_BASE_PATH}/review/${tab}/${itemId}`,
        },
        { replace: options?.replace },
      );
    },
    [navigate],
  );
  const actionableIncomingSkillShares = useMemo(
    () =>
      incomingSkillShares.filter((item) =>
        isSkillShareActionable(item.status),
      ),
    [incomingSkillShares],
  );
  const incomingPendingCount = actionableIncomingSkillShares.length;
  const currentSkillShareList = useMemo(
    () =>
      skillShareCenterTab === "incoming"
        ? actionableIncomingSkillShares
        : outgoingSkillShares,
    [actionableIncomingSkillShares, outgoingSkillShares, skillShareCenterTab],
  );
  const refreshExperienceAssets = useCallback(
    async (options?: { silent?: boolean }) => {
      if (!options?.silent) {
        setExperienceLoading(true);
      }

      try {
        const records = await listPreferenceAssets();
        setExperienceAssets(
          records.map((item) => ({
            id: item.id,
            title: item.title,
            content: item.content,
            agentPersona: item.agentPersona,
            hasPendingReviewSuggestions: item.hasPendingReviewSuggestions,
            protect: item.protect,
            responseStyle: item.responseStyle,
            autoEvo: item.autoEvo,
            autoEvoApplyStatus: item.autoEvoApplyStatus,
            autoEvoGeneration: item.autoEvoGeneration,
            autoEvoError: item.autoEvoError,
            resourceType: item.resourceType,
            reviewStatus: item.reviewStatus,
            suggestionStatus: item.suggestionStatus,
            preferredName: item.preferredName,
          })),
        );
      } catch (error) {
        console.error("Load preference assets failed:", error);
        if (options?.silent) {
          throw error;
        }
        if (!options?.silent) {
          message.error(
            getLocalizedErrorMessage(error, t("admin.memoryExperienceLoadFailed")) ||
              t("admin.memoryExperienceLoadFailed"),
          );
        }
      } finally {
        if (!options?.silent) {
          setExperienceLoading(false);
        }
      }
    },
    [t],
  );
  const refreshExperienceSetting = useCallback(
    async (options?: { silent?: boolean }) => {
      try {
        const enabled = await getPersonalizationSetting();
        setExperienceFeatureEnabled(enabled);
      } catch (error) {
        console.error("Load preference setting failed:", error);
        if (options?.silent) {
          throw error;
        }
        if (!options?.silent) {
          message.error(
            getLocalizedErrorMessage(error, t("admin.memoryExperienceSettingLoadFailed")) ||
              t("admin.memoryExperienceSettingLoadFailed"),
          );
        }
      }
    },
    [t],
  );
  const refreshExperienceSection = useCallback(
    async (options?: { silent?: boolean }) => {
      const silent = Boolean(options?.silent);
      if (!silent) {
        setExperienceLoading(true);
      }

      try {
        await Promise.all([
          refreshExperienceAssets({ silent: true }),
          refreshExperienceSetting({ silent: true }),
        ]);
      } catch (error) {
        console.error("Refresh preference section failed:", error);
        if (!silent) {
          message.error(
            getLocalizedErrorMessage(error, t("admin.memoryExperienceLoadFailed")) ||
              t("admin.memoryExperienceLoadFailed"),
          );
        }
      } finally {
        setExperienceInitialized(true);
        if (!silent) {
          setExperienceLoading(false);
        }
      }
    },
    [refreshExperienceAssets, refreshExperienceSetting, t],
  );
  const refreshSkillAssets = useCallback(async (
    options: { page?: number; pageSize?: number; preserveChangeProposals?: boolean } = {},
  ) => {
    const requestId = skillListRequestIdRef.current + 1;
    skillListRequestIdRef.current = requestId;
    setSkillLoading(true);

    try {
      const result = await listSkillAssetsPage({
        keyword: skillKeyword,
        category,
        tags: tag ? [tag] : [],
        page: options.page ?? skillListPage,
        pageSize: options.pageSize ?? skillListPageSize,
      });
      const records = result.records;
      if (skillListRequestIdRef.current !== requestId) {
        return;
      }

      setSkillListTotal(result.total);
      setSkillListPage(result.page);
      setSkillListPageSize(result.pageSize);
      setSkillAssets(records.map(mapSkillAssetRecordToStructuredAsset));
      if (!options.preserveChangeProposals) {
        setChangeProposals((previous) =>
          previous.filter((proposal) => proposal.tab !== "skills"),
        );
      }
    } catch (error) {
      if (skillListRequestIdRef.current !== requestId) {
        return;
      }
      console.error("Load skill assets failed:", error);
    } finally {
      if (skillListRequestIdRef.current === requestId) {
        setSkillLoading(false);
        setSkillsInitialized(true);
      }
    }
  }, [category, skillKeyword, skillListPage, skillListPageSize, tag]);

  const refreshParentSkillAssets = useCallback(async () => {
    const requestId = parentSkillListRequestIdRef.current + 1;
    parentSkillListRequestIdRef.current = requestId;
    setParentSkillOptionsLoading(true);

    try {
      const firstResult = await listSkillAssetsPage({
        page: 1,
        pageSize: parentSkillOptionPageSize,
      });
      if (parentSkillListRequestIdRef.current !== requestId) {
        return;
      }

      const records = [...firstResult.records];
      const pageSize = Math.max(1, firstResult.pageSize || parentSkillOptionPageSize);
      const totalPages = Math.ceil(firstResult.total / pageSize);

      for (let page = 2; page <= totalPages; page += 1) {
        const pageResult = await listSkillAssetsPage({ page, pageSize });
        if (parentSkillListRequestIdRef.current !== requestId) {
          return;
        }
        records.push(...pageResult.records);
      }

      const deduped = new Map<string, SkillAssetRecord>();
      records.forEach((item) => {
        deduped.set(item.id, item);
      });
      setParentSkillAssets(
        Array.from(deduped.values()).map(mapSkillAssetRecordToStructuredAsset),
      );
    } catch (error) {
      if (parentSkillListRequestIdRef.current !== requestId) {
        return;
      }
      console.error("Load parent skill options failed:", error);
      setParentSkillAssets(skillAssets);
    } finally {
      if (parentSkillListRequestIdRef.current === requestId) {
        setParentSkillOptionsLoading(false);
      }
    }
  }, [skillAssets]);

  const refreshGlossaryAssets = useCallback(
    async (options?: {
      keyword?: string;
      page?: number;
      pageSize?: number;
      silent?: boolean;
      source?: GlossarySource;
    }) => {
      const requestId = glossaryRequestIdRef.current + 1;
      glossaryRequestIdRef.current = requestId;
      const nextPage = Math.max(1, options?.page ?? glossaryListPage);
      const nextPageSize = Math.max(1, options?.pageSize ?? glossaryListPageSize);
      const totalForToken = Math.max(glossaryListTotal, (nextPage - 1) * nextPageSize);
      const pageToken =
        nextPage > 1
          ? window.btoa(
              JSON.stringify({
                Start: (nextPage - 1) * nextPageSize,
                Limit: nextPageSize,
                TotalCount: totalForToken,
              }),
            )
          : "";

      if (!options?.silent) {
        setGlossaryLoading(true);
      }
      setGlossaryLoadError("");

      try {
        const result = await listGlossaryAssetsPage({
          keyword: options?.keyword,
          source: options?.source,
          pageSize: nextPageSize,
          pageToken,
        });

        if (glossaryRequestIdRef.current !== requestId) {
          return;
        }

        const records = result.records;
        setGlossaryListPage(nextPage);
        setGlossaryListPageSize(nextPageSize);
        setGlossaryListTotal(result.total);
        setGlossaryAssets(records);
        setSelectedGlossaryAssetIds((previous) => {
          const validIds = new Set(records.map((item) => item.id));
          return previous.filter((id) => validIds.has(id));
        });
        setGlossaryDetailTarget((previous) => {
          if (!previous) {
            return previous;
          }
          const refreshed = records.find((item) => item.id === previous.id);
          return refreshed ? cloneGlossaryAsset(refreshed) : previous;
        });
      } catch (error) {
        if (glossaryRequestIdRef.current !== requestId) {
          return;
        }

        const errorMessage =
          getLocalizedErrorMessage(error, t("admin.memoryGlossaryLoadFailed")) ||
          t("admin.memoryGlossaryLoadFailed");

        setGlossaryLoadError(errorMessage);
        if (!options?.silent) {
          message.error(errorMessage);
        }
      } finally {
        if (glossaryRequestIdRef.current === requestId) {
          setGlossaryInitialized(true);
          if (!options?.silent) {
            setGlossaryLoading(false);
          }
        }
      }
    },
    [glossaryListPage, glossaryListPageSize, glossaryListTotal, t],
  );

  const buildGlossaryProposalFromConflict = useCallback(
    (
      conflict: GlossaryConflict,
      conflictGroups: GlossaryAsset[] = [],
    ): GlossaryChangeProposal => ({
      id: conflict.id,
      targetId: conflict.id,
      before: null,
      after: {
        id: conflict.id,
        term: conflict.word,
        group: "",
        aliases: conflict.word ? [conflict.word] : [],
        source: "user",
        content: conflict.description,
        protect: false,
      },
      reason: conflict.reason || t("admin.memoryGlossaryInboxConflictDefaultReason"),
      backendConflictId: conflict.id,
      backendConflictWord: conflict.word,
      backendConflictGroupIds: conflict.groupIds,
      backendConflictGroups: conflictGroups,
    }),
    [t],
  );

  const loadGlossaryConflictGroups = useCallback(
    async (groupIds: string[]): Promise<GlossaryAsset[]> => {
      if (!groupIds.length) {
        return [];
      }

      const uniqueGroupIds = [...new Set(groupIds)];
      const details = await Promise.all(
        uniqueGroupIds.map(async (groupId) => {
          try {
            const detail = await getGlossaryAssetDetail(groupId);
            if (detail) {
              return detail;
            }
          } catch (error) {
            console.error("Load glossary conflict group detail failed:", error);
          }

          return {
            id: groupId,
            term: groupId,
            group: "",
            aliases: [],
            source: "user" as GlossarySource,
            content: "",
            protect: false,
          };
        }),
      );

      return details;
    },
    [],
  );

  const refreshGlossaryConflicts = useCallback(
    async (options?: { silent?: boolean; showErrorToast?: boolean }) => {
      const requestId = glossaryConflictRequestIdRef.current + 1;
      glossaryConflictRequestIdRef.current = requestId;

      if (!options?.silent) {
        setGlossaryInboxLoading(true);
      }
      setGlossaryInboxError("");

      try {
        const conflicts = await listGlossaryConflicts({ pageSize: 200 });
        const proposals = await Promise.all(
          conflicts.map(async (conflict) => {
            const conflictGroups = await loadGlossaryConflictGroups(conflict.groupIds);
            return buildGlossaryProposalFromConflict(conflict, conflictGroups);
          }),
        );
        if (glossaryConflictRequestIdRef.current !== requestId) {
          return;
        }

        setGlossaryChangeProposals(proposals);
      } catch (error) {
        if (glossaryConflictRequestIdRef.current !== requestId) {
          return;
        }

        const errorMessage =
          getLocalizedErrorMessage(error, t("admin.memoryGlossaryInboxLoadFailed")) ||
          t("admin.memoryGlossaryInboxLoadFailed");

        setGlossaryInboxError(errorMessage);
        if (options?.showErrorToast) {
          message.error(errorMessage);
        }
      } finally {
        if (glossaryConflictRequestIdRef.current === requestId) {
          setGlossaryInboxLoading(false);
        }
      }
    },
    [buildGlossaryProposalFromConflict, loadGlossaryConflictGroups, t],
  );

  const setSkillShareAction = useCallback(
    (shareItemId: string, action?: SkillShareAction) => {
      setSkillShareActionState((previous) => {
        const next = { ...previous };

        if (!action) {
          delete next[shareItemId];
          return next;
        }

        next[shareItemId] = action;
        return next;
      });
    },
    [],
  );

  const refreshSkillShareCenter = useCallback(
    async (options?: { silent?: boolean; showErrorToast?: boolean }) => {
      const requestId = skillShareRequestIdRef.current + 1;
      skillShareRequestIdRef.current = requestId;

      if (!options?.silent) {
        setSkillShareCenterLoading(true);
      }
      setSkillShareCenterError("");

      try {
        const [incoming, outgoing] = await Promise.all([
          listIncomingSkillShares(),
          listOutgoingSkillShares(),
        ]);

        if (skillShareRequestIdRef.current !== requestId) {
          return;
        }

        setIncomingSkillShares(incoming);
        setOutgoingSkillShares(outgoing);
      } catch (error) {
        if (skillShareRequestIdRef.current !== requestId) {
          return;
        }

        const errorMessage =
          getLocalizedErrorMessage(error, t("admin.memorySkillShareLoadFailed")) ||
          t("admin.memorySkillShareLoadFailed");

        setSkillShareCenterError(errorMessage);
        if (options?.showErrorToast) {
          message.error(errorMessage);
        }
      } finally {
        if (skillShareRequestIdRef.current === requestId) {
          setSkillShareCenterLoading(false);
        }
      }
    },
    [t],
  );

  const refreshShareStatus = useCallback(
    async (
      skillId: string,
      options?: { silent?: boolean; showErrorToast?: boolean },
    ) => {
      const requestId = shareStatusRequestIdRef.current + 1;
      shareStatusRequestIdRef.current = requestId;

      if (!options?.silent) {
        setShareStatusLoading(true);
      }
      setShareStatusError("");

      try {
        const records = await listSkillShareTargets(skillId);
        if (shareStatusRequestIdRef.current !== requestId) {
          return;
        }

        setShareStatusRecords(records);
      } catch (error) {
        if (shareStatusRequestIdRef.current !== requestId) {
          return;
        }

        const errorMessage =
          getLocalizedErrorMessage(error, t("admin.memoryShareStatusLoadFailed")) ||
          t("admin.memoryShareStatusLoadFailed");

        setShareStatusError(errorMessage);
        if (options?.showErrorToast) {
          message.error(errorMessage);
        }
      } finally {
        if (shareStatusRequestIdRef.current === requestId) {
          setShareStatusLoading(false);
        }
      }
    },
    [t],
  );

  useEffect(() => {
    const shouldRefreshSkillAssets =
      Boolean(skillRouteItemId) ||
      reviewRouteTab === "skills" ||
      routeMemoryTab === "skills";

    if (!shouldRefreshSkillAssets) {
      return;
    }

    const isNewSkillListEntry =
      !skillRouteItemId &&
      reviewRouteTab !== "skills" &&
      skillListRouteLocationKeyRef.current !== location.key;
    const requestPage = isNewSkillListEntry ? 1 : skillListPage;
    const refreshKey = [
      location.key,
      location.pathname,
      location.search,
      skillKeyword,
      category || "",
      tag || "",
      requestPage,
      skillListPageSize,
    ].join("|");

    if (skillListRefreshKeyRef.current === refreshKey) {
      return;
    }
    skillListRouteLocationKeyRef.current = location.key;
    skillListRefreshKeyRef.current = refreshKey;

    void refreshSkillAssets({ page: requestPage });
  }, [
    category,
    location.key,
    location.pathname,
    location.search,
    refreshSkillAssets,
    reviewRouteTab,
    routeMemoryTab,
    skillKeyword,
    skillListPage,
    skillListPageSize,
    skillRouteItemId,
    tag,
  ]);

  useEffect(() => {
    const shouldLoadSkillTags =
      Boolean(skillRouteItemId) ||
      reviewRouteTab === "skills" ||
      routeMemoryTab === "skills";

    if (!shouldLoadSkillTags) {
      return undefined;
    }

    let ignore = false;
    setSkillTagsLoading(true);

    void listSkillTags()
      .then((tags) => {
        if (ignore) {
          return;
        }
        setSkillTags(tags);
        setSkillTagsLoaded(true);
      })
      .catch((error) => {
        if (ignore) {
          return;
        }
        console.error("Load skill tags failed:", error);
        setSkillTagsLoaded(false);
      })
      .finally(() => {
        if (!ignore) {
          setSkillTagsLoading(false);
        }
      });

    return () => {
      ignore = true;
    };
  }, [reviewRouteTab, routeMemoryTab, skillRouteItemId]);

  useEffect(() => {
    const shouldLoadSkillCategories =
      Boolean(skillRouteItemId) ||
      reviewRouteTab === "skills" ||
      routeMemoryTab === "skills";

    if (!shouldLoadSkillCategories) {
      return undefined;
    }

    let ignore = false;
    setSkillCategoriesLoading(true);

    void listSkillCategories()
      .then((categories) => {
        if (ignore) {
          return;
        }
        setSkillCategories(categories);
        setSkillCategoriesLoaded(true);
      })
      .catch((error) => {
        if (ignore) {
          return;
        }
        console.error("Load skill categories failed:", error);
        setSkillCategoriesLoaded(false);
      })
      .finally(() => {
        if (!ignore) {
          setSkillCategoriesLoading(false);
        }
      });

    return () => {
      ignore = true;
    };
  }, [reviewRouteTab, routeMemoryTab, skillRouteItemId]);

  useEffect(() => {
    const shouldRefreshExperience =
      Boolean(experienceRouteItemId) ||
      reviewRouteTab === "experience" ||
      routeMemoryTab === "experience";

    if (!shouldRefreshExperience) {
      return;
    }

    const refreshKey = [
      location.key,
      location.pathname,
      location.search,
      routeMemoryTab,
    ].join("|");

    if (experienceSectionRefreshKeyRef.current === refreshKey) {
      return;
    }
    experienceSectionRefreshKeyRef.current = refreshKey;

    void refreshExperienceSection();
  }, [
    experienceRouteItemId,
    location.key,
    location.pathname,
    location.search,
    refreshExperienceSection,
    reviewRouteTab,
    routeMemoryTab,
  ]);

  useEffect(() => {
    if (activeTab !== "skills") {
      return;
    }

    void refreshSkillShareCenter({ silent: true });
  }, [activeTab, refreshSkillShareCenter]);

  useEffect(() => {
    if (routeMemoryTab !== "glossary") {
      return;
    }

    const filterKey = [query, glossarySource || ""].join("|");
    const shouldResetGlossaryPage =
      glossaryAssetsRouteLocationKeyRef.current !== location.key ||
      glossaryAssetsFilterKeyRef.current !== filterKey;
    const requestPage = shouldResetGlossaryPage ? 1 : glossaryListPage;
    const refreshKey = [
      location.key,
      location.pathname,
      location.search,
      filterKey,
      requestPage,
      glossaryListPageSize,
    ].join("|");

    if (glossaryAssetsRefreshKeyRef.current === refreshKey) {
      return;
    }
    glossaryAssetsRouteLocationKeyRef.current = location.key;
    glossaryAssetsFilterKeyRef.current = filterKey;
    glossaryAssetsRefreshKeyRef.current = refreshKey;

    void refreshGlossaryAssets({
      keyword: query,
      page: requestPage,
      pageSize: glossaryListPageSize,
      source: glossarySource,
    });
  }, [
    glossaryListPage,
    glossaryListPageSize,
    glossarySource,
    location.key,
    location.pathname,
    location.search,
    query,
    refreshGlossaryAssets,
    routeMemoryTab,
  ]);

  useEffect(() => {
    if (routeMemoryTab !== "glossary") {
      return;
    }

    const refreshKey = [
      location.key,
      location.pathname,
      location.search,
      routeMemoryTab,
    ].join("|");

    if (glossaryConflictsRefreshKeyRef.current === refreshKey) {
      return;
    }
    glossaryConflictsRefreshKeyRef.current = refreshKey;

    void refreshGlossaryConflicts({ silent: true });
  }, [
    location.key,
    location.pathname,
    location.search,
    refreshGlossaryConflicts,
    routeMemoryTab,
  ]);

  useEffect(() => {
    if (!glossaryInboxOpen) {
      return;
    }

    void refreshGlossaryConflicts({ showErrorToast: true });
  }, [glossaryInboxOpen, refreshGlossaryConflicts]);

  useEffect(() => {
    const queryTab = parseMemoryTab(searchParams.get("tab"));
    const nextTab = skillRouteItemId
      ? "skills"
      : experienceRouteItemId
      ? "experience"
      : glossaryRouteItemId
      ? "glossary"
      : reviewRouteTab || routeListTab || queryTab || "skills";

    setActiveTab((previous) => (previous === nextTab ? previous : nextTab));
  }, [
    experienceRouteItemId,
    glossaryRouteItemId,
    reviewRouteTab,
    routeListTab,
    searchParams,
    skillRouteItemId,
  ]);

  useEffect(() => {
    let ignore = false;

    if (!glossaryRouteItemId) {
      setGlossaryDetailTarget((previous) => (previous ? null : previous));
      return () => {
        ignore = true;
      };
    }

    const matchedGlossary = glossaryAssets.find((item) => item.id === glossaryRouteItemId);
    if (matchedGlossary) {
      setGlossaryDetailTarget(cloneGlossaryAsset(matchedGlossary));
      return () => {
        ignore = true;
      };
    }

    if (!glossaryInitialized) {
      return () => {
        ignore = true;
      };
    }

    setGlossaryDetailTarget(null);
    void (async () => {
      try {
        const detail = await getGlossaryAssetDetail(glossaryRouteItemId);
        if (ignore) {
          return;
        }
        if (detail) {
          setGlossaryDetailTarget(cloneGlossaryAsset(detail));
          return;
        }
        message.warning(t("admin.memoryDiffTargetMissing"));
        navigateToMemoryList("glossary", { replace: true });
      } catch (error) {
        if (ignore) {
          return;
        }
        console.error("Load glossary detail failed:", error);
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryGlossaryLoadFailed")) ||
            t("admin.memoryGlossaryLoadFailed"),
        );
        navigateToMemoryList("glossary", { replace: true });
      }
    })();

    return () => {
      ignore = true;
    };
  }, [
    glossaryAssets,
    glossaryInitialized,
    glossaryRouteItemId,
    navigateToMemoryList,
    t,
  ]);

  useEffect(() => {
    if (!reviewRouteTab || !reviewRouteItemId) {
      setActiveProposalId(undefined);
      reviewRouteReloadKeyRef.current = "";
      return;
    }

    if (reviewRouteTab === "skills" && !skillsInitialized) {
      return;
    }

    if (reviewRouteTab === "experience" && !experienceInitialized) {
      return;
    }

    const reviewRouteReloadKey = `${reviewRouteTab}:${reviewRouteItemId}`;
    if (reviewRouteReloadKeyRef.current === reviewRouteReloadKey && activeProposal) {
      return;
    }
    reviewRouteReloadKeyRef.current = reviewRouteReloadKey;

    void (async () => {
      const opened = await openChangeReview(reviewRouteTab, reviewRouteItemId, undefined, {
        forceReload: true,
        syncRoute: false,
      });

      if (!opened) {
        reviewRouteReloadKeyRef.current = "";
        navigateToMemoryList(reviewRouteTab, { replace: true });
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    reviewRouteTab,
    reviewRouteItemId,
    skillsInitialized,
    experienceInitialized,
    skillAssets,
    experienceAssets,
    changeProposals,
  ]);

  const proposalKey = useCallback(
    (tab: ChangeProposalTab, itemId: string) => `${tab}:${itemId}`,
    [],
  );
  const proposalMap = useMemo(() => {
    const map = new Map<string, ChangeProposal>();
    changeProposals.forEach((item) => {
      map.set(proposalKey(item.tab, item.targetId), item);
    });
    return map;
  }, [changeProposals, proposalKey]);
  const getPendingProposal = useCallback(
    (tab: ChangeProposalTab, itemId: string) => proposalMap.get(proposalKey(tab, itemId)),
    [proposalKey, proposalMap],
  );
  const activeProposal = useMemo(
    () =>
      activeProposalId
        ? changeProposals.find((item) => item.id === activeProposalId) || null
        : null,
    [activeProposalId, changeProposals],
  );
  const activeBackendSuggestions = useMemo(
    () => {
      const suggestions = activeProposal?.backendSuggestions || [];
      if (activeProposal?.tab !== "skills") {
        return suggestions;
      }

      return [...suggestions].sort((left, right) => {
        const leftIsRemove = isSkillRemoveSuggestion(left);
        const rightIsRemove = isSkillRemoveSuggestion(right);
        if (leftIsRemove === rightIsRemove) {
          return 0;
        }
        return leftIsRemove ? -1 : 1;
      });
    },
    [activeProposal],
  );
  const activeSkillRemoveSuggestions = useMemo(
    () =>
      activeProposal?.tab === "skills"
        ? activeBackendSuggestions.filter((item) => isSkillRemoveSuggestion(item))
        : [],
    [activeBackendSuggestions, activeProposal?.tab],
  );
  const hasPendingSkillRemoveSuggestion = activeSkillRemoveSuggestions.length > 0;
  const isBackendSuggestionSelectable = useCallback(
    (suggestion: EvolutionSuggestionRecord) =>
      activeProposal?.tab !== "skills" ||
      !hasPendingSkillRemoveSuggestion ||
      isSkillRemoveSuggestion(suggestion),
    [activeProposal?.tab, hasPendingSkillRemoveSuggestion],
  );
  const selectableBackendSuggestionIds = useMemo(
    () =>
      activeBackendSuggestions
        .filter((item) => isBackendSuggestionSelectable(item))
        .map((item) => item.id),
    [activeBackendSuggestions, isBackendSuggestionSelectable],
  );
  const activeBackendSuggestionPage =
    activeProposal
      ? activeProposal.backendSuggestionPage || 1
      : 1;
  const activeBackendSuggestionPageSize =
    activeProposal
      ? activeProposal.backendSuggestionPageSize || backendSuggestionPageSize
      : backendSuggestionPageSize;
  const activeBackendSuggestionTotal =
    activeProposal
      ? Math.max(
          activeBackendSuggestions.length,
          activeProposal.backendSuggestionTotal || activeBackendSuggestions.length,
        )
      : activeBackendSuggestions.length;
  const backendSuggestionHasMore =
    Boolean(activeProposal) &&
    activeBackendSuggestionPage * activeBackendSuggestionPageSize < activeBackendSuggestionTotal;
  const isBackendSuggestionReviewMode =
    Boolean(activeProposal?.backendSuggestions) &&
    (activeProposal?.tab === "skills" ||
      activeProposal?.tab === "experience" ||
      activeBackendSuggestions.length > 0 ||
      approvedBackendSuggestionIds.length > 0 ||
      rejectedBackendSuggestionIds.length > 0);
  const activeBackendSuggestionSourceText = useMemo(() => {
    if (!activeProposal) {
      return "";
    }

    if (activeProposal.tab === "skills") {
      return getSkillBodyContentForDisplay(activeProposal.before.content);
    }

    return activeProposal.before.content;
  }, [activeProposal]);
  const backendDraftDiffLines = useMemo(
    () => buildUnifiedDiffLines(backendDraftPreview?.diff || ""),
    [backendDraftPreview?.diff],
  );
  const activeProposalFieldChanges = useMemo<ProposalFieldChange[]>(() => {
    if (!activeProposal) {
      return [];
    }
    if (activeProposal.backendSuggestions) {
      return [];
    }

    const yesText = t("admin.memoryDiffBoolYes");
    const noText = t("admin.memoryDiffBoolNo");
    const toBoolText = (value: boolean) => (value ? yesText : noText);

    if (activeProposal.tab === "skills") {
      const beforeTags = activeProposal.before.tags.join(", ");
      const afterTags = activeProposal.after.tags.join(", ");
      const fieldSuggestionIds = activeProposal.backendSuggestionIdsByField || {};
      const fieldChanges: Array<ProposalFieldChange | null> = [
        activeProposal.before.name !== activeProposal.after.name
          ? {
              key: "name",
              label: t("admin.memoryName"),
              before: activeProposal.before.name,
              after: activeProposal.after.name,
              backendSuggestionId:
                fieldSuggestionIds.name || activeProposal.backendSuggestionId,
            }
          : null,
        activeProposal.before.description !== activeProposal.after.description
          ? {
              key: "description",
              label: t("admin.memoryDescription"),
              before: activeProposal.before.description,
              after: activeProposal.after.description,
              backendSuggestionId:
                fieldSuggestionIds.description || activeProposal.backendSuggestionId,
            }
          : null,
        activeProposal.before.category !== activeProposal.after.category
          ? {
              key: "category",
              label: t("admin.memoryCategory"),
              before: activeProposal.before.category,
              after: activeProposal.after.category,
              backendSuggestionId:
                fieldSuggestionIds.category || activeProposal.backendSuggestionId,
            }
          : null,
        activeProposal.before.tags.join(",") !== activeProposal.after.tags.join(",")
          ? {
              key: "tags",
              label: t("admin.memoryTagSet"),
              before: beforeTags,
              after: afterTags,
              backendSuggestionId:
                fieldSuggestionIds.tags || activeProposal.backendSuggestionId,
            }
          : null,
        activeProposal.before.content !== activeProposal.after.content
          ? {
              key: "content",
              label: t("admin.memoryContent"),
              before: activeProposal.before.content,
              after: activeProposal.after.content,
              backendSuggestionId:
                fieldSuggestionIds.content || activeProposal.backendSuggestionId,
            }
          : null,
        Boolean(activeProposal.before.protect) !== Boolean(activeProposal.after.protect)
          ? {
              key: "protect",
              label: t("admin.memoryProtect", { defaultValue: "保护" }),
              before: toBoolText(Boolean(activeProposal.before.protect)),
              after: toBoolText(Boolean(activeProposal.after.protect)),
              backendSuggestionId:
                fieldSuggestionIds.protect || activeProposal.backendSuggestionId,
            }
          : null,
      ];

      return fieldChanges.filter((item): item is ProposalFieldChange => Boolean(item));
    }

    const fieldSuggestionIds = activeProposal.backendSuggestionIdsByField || {};
    const fieldChanges: Array<ProposalFieldChange | null> = [
      activeProposal.before.title !== activeProposal.after.title
        ? {
            key: "title",
            label: t("admin.memoryTitle"),
            before: activeProposal.before.title,
            after: activeProposal.after.title,
            backendSuggestionId:
              fieldSuggestionIds.title || activeProposal.backendSuggestionId,
          }
        : null,
      activeProposal.before.content !== activeProposal.after.content
        ? {
            key: "content",
            label: t("admin.memoryContent"),
            before: activeProposal.before.content,
            after: activeProposal.after.content,
            backendSuggestionId:
              fieldSuggestionIds.content || activeProposal.backendSuggestionId,
          }
        : null,
      Boolean(activeProposal.before.protect) !== Boolean(activeProposal.after.protect)
        ? {
            key: "protect",
            label: t("admin.memoryProtect", { defaultValue: "保护" }),
            before: toBoolText(Boolean(activeProposal.before.protect)),
            after: toBoolText(Boolean(activeProposal.after.protect)),
            backendSuggestionId:
              fieldSuggestionIds.protect || activeProposal.backendSuggestionId,
        }
      : null,
    ];
    return fieldChanges.filter((item): item is ProposalFieldChange => Boolean(item));
  }, [activeProposal, t]);

  activeProposalFieldChangesRef.current = activeProposalFieldChanges;

  useEffect(() => {
    let ignore = false;

    if (!activeProposal) {
      setProposalFieldDecisions({});
      setSelectedFieldKeys([]);
      setActiveReviewStep(0);
      setManualMergedDraft(null);
      setIsPreviewContentEditing(false);
      setManualPreviewContentDraft("");
      setQaQuestionDraft("");
      setSelectedBackendSuggestionIds([]);
      setBackendSuggestionBatchSubmitting("");
      setBackendSuggestionLoadingMore(false);
      setBackendSuggestionLoadMoreError("");
      setApprovedBackendSuggestionIds([]);
      setRejectedBackendSuggestionIds([]);
      setBackendDraftPreview(null);
      setBackendDraftLoading(false);
      setBackendDraftSubmitting("");
      return () => {
        ignore = true;
      };
    }

    const fieldChanges = activeProposal.backendSuggestions
      ? []
      : activeProposalFieldChangesRef.current;
    const defaults = fieldChanges.reduce<Record<string, ProposalFieldDecision>>(
      (result, field) => {
        result[field.key] = "pending";
        return result;
      },
      {},
    );

    setProposalFieldDecisions(defaults);
    setSelectedFieldKeys([]);
    setActiveReviewStep(0);
    setManualMergedDraft(null);
    setIsPreviewContentEditing(false);
    setManualPreviewContentDraft("");
    setQaQuestionDraft("");
    setSelectedBackendSuggestionIds([]);
    setBackendSuggestionBatchSubmitting("");
    setBackendSuggestionLoadingMore(false);
    setBackendSuggestionLoadMoreError("");
    setApprovedBackendSuggestionIds([]);
    setRejectedBackendSuggestionIds([]);
    setBackendDraftPreview(null);
    setBackendDraftLoading(false);
    setBackendDraftSubmitting("");

    if (
      (activeProposal.tab === "skills" || activeProposal.tab === "experience") &&
      activeProposal.backendSuggestions
    ) {
      if (confirmedDraftProposalIdsRef.current.has(activeProposal.id)) {
        return () => {
          ignore = true;
        };
      }

      const isSkillProposal = activeProposal.tab === "skills";
      setActiveReviewStep(1);
      if (activeProposal.backendDraftPreview) {
        setBackendDraftPreview(activeProposal.backendDraftPreview);
        if (!isSkillProposal) {
          setBackendDraftKind(resolveManagedPreferenceDraftKind(activeProposal.before.resourceType));
        }
        return () => {
          ignore = true;
        };
      }
      setBackendDraftLoading(true);
      void (async () => {
        try {
          const preview = isSkillProposal
            ? await previewSkillDraft(activeProposal.targetId)
            : await previewManagedPreferenceDraft(
                resolveManagedPreferenceDraftKind(activeProposal.before.resourceType),
              );
          if (!ignore) {
            if (!isSkillProposal) {
              setBackendDraftKind(
                resolveManagedPreferenceDraftKind(activeProposal.before.resourceType),
              );
            }
            setBackendDraftPreview(preview);
          }
        } catch (error) {
          if (!ignore) {
            console.error("Load draft preview failed:", error);
            const errorKey = isSkillProposal
              ? "admin.memorySkillDraftPreviewFailed"
              : "admin.memoryPreferenceDraftPreviewFailed";
            message.error(
              getLocalizedErrorMessage(error, t(errorKey)) || t(errorKey),
            );
          }
        } finally {
          if (!ignore) {
            setBackendDraftLoading(false);
          }
        }
      })();
    }

    return () => {
      ignore = true;
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeProposal?.id]);

  useEffect(() => {
    backendSuggestionLoadMoreRequestIdRef.current += 1;
  }, [activeProposal?.id]);

  const currentProposalFieldKeys = useMemo(
    () => activeProposalFieldChanges.map((field) => field.key),
    [activeProposalFieldChanges],
  );
  const allSelectableFieldsSelected = useMemo(
    () =>
      currentProposalFieldKeys.length > 0 &&
      selectedFieldKeys.length === currentProposalFieldKeys.length,
    [currentProposalFieldKeys, selectedFieldKeys],
  );
  const hasPartialFieldSelection = useMemo(
    () => selectedFieldKeys.length > 0 && !allSelectableFieldsSelected,
    [allSelectableFieldsSelected, selectedFieldKeys],
  );
  const selectedBackendSuggestionCount = selectedBackendSuggestionIds.length;
  const allBackendSuggestionsSelected = useMemo(
    () =>
      selectableBackendSuggestionIds.length > 0 &&
      selectedBackendSuggestionCount === selectableBackendSuggestionIds.length,
    [selectableBackendSuggestionIds.length, selectedBackendSuggestionCount],
  );
  const hasPartialBackendSuggestionSelection =
    selectedBackendSuggestionCount > 0 && !allBackendSuggestionsSelected;
  const backendRejectedSuggestionCount = rejectedBackendSuggestionIds.length;
  const isBackendSuggestionBatchBusy = Boolean(backendSuggestionBatchSubmitting);
  const isAnyBackendSuggestionMutating =
    isBackendSuggestionBatchBusy || Object.keys(backendSuggestionSubmitting).length > 0;

  useEffect(() => {
    setSelectedFieldKeys((previous) =>
      previous.filter((key) => currentProposalFieldKeys.includes(key)),
    );
  }, [currentProposalFieldKeys]);

  useEffect(() => {
    setSelectedBackendSuggestionIds((previous) =>
      previous.filter((item) => selectableBackendSuggestionIds.includes(item)),
    );
  }, [selectableBackendSuggestionIds]);

  const activeProposalMerged = useMemo<StructuredAsset | ExperienceAsset | null>(() => {
    if (!activeProposal) {
      return null;
    }

    const useAfterValue = (fieldKey: ProposalFieldKey) =>
      activeProposalFieldChanges.some((field) => field.key === fieldKey) &&
      (proposalFieldDecisions[fieldKey] ?? "pending") === "accept";

    if (activeProposal.tab === "skills") {
      const merged = cloneStructuredAsset(activeProposal.before);

      if (useAfterValue("name")) {
        merged.name = activeProposal.after.name;
      }
      if (useAfterValue("description")) {
        merged.description = activeProposal.after.description;
      }
      if (useAfterValue("category")) {
        merged.category = activeProposal.after.category;
      }
      if (useAfterValue("tags")) {
        merged.tags = [...activeProposal.after.tags];
      }
      if (useAfterValue("content")) {
        merged.content = activeProposal.after.content;
      }
      if (useAfterValue("protect")) {
        merged.protect = Boolean(activeProposal.after.protect);
      }

      return merged;
    }

    const merged = cloneExperienceAsset(activeProposal.before);
    if (useAfterValue("title")) {
      merged.title = activeProposal.after.title;
    }
    if (useAfterValue("content")) {
      merged.content = activeProposal.after.content;
    }
    if (useAfterValue("protect")) {
      merged.protect = Boolean(activeProposal.after.protect);
    }
    return merged;
  }, [activeProposal, activeProposalFieldChanges, proposalFieldDecisions]);

  const effectiveProposalMerged = useMemo<StructuredAsset | ExperienceAsset | null>(
    () => manualMergedDraft ?? activeProposalMerged,
    [activeProposalMerged, manualMergedDraft],
  );

  const hasEffectiveChange = useMemo(() => {
    if (!activeProposal || !effectiveProposalMerged) {
      return false;
    }

    if (activeProposal.tab === "skills") {
      const merged = effectiveProposalMerged as StructuredAsset;
      return (
        activeProposal.before.name !== merged.name ||
        activeProposal.before.description !== merged.description ||
        activeProposal.before.category !== merged.category ||
        activeProposal.before.tags.join(",") !== merged.tags.join(",") ||
        activeProposal.before.content !== merged.content ||
        Boolean(activeProposal.before.protect) !== Boolean(merged.protect)
      );
    }

    const merged = effectiveProposalMerged as ExperienceAsset;
    return (
      activeProposal.before.title !== merged.title ||
      activeProposal.before.content !== merged.content ||
      Boolean(activeProposal.before.protect) !== Boolean(merged.protect)
    );
  }, [activeProposal, effectiveProposalMerged]);

  const activeProposalDiff = useMemo(() => {
    if (!activeProposal || !effectiveProposalMerged) {
      return null;
    }

    const commonLabels = {
      protect: t("admin.memoryProtect", { defaultValue: "保护" }),
      content: t("admin.memoryContent"),
      yes: t("admin.memoryDiffBoolYes"),
      no: t("admin.memoryDiffBoolNo"),
    };
    const beforeText =
      activeProposal.tab === "skills"
        ? serializeStructuredAsset(activeProposal.before, {
            name: t("admin.memoryName"),
            description: t("admin.memoryDescription"),
            category: t("admin.memoryCategory"),
            tags: t("admin.memoryTagSet"),
            ...commonLabels,
          })
        : serializeExperienceAsset(activeProposal.before, {
            title: t("admin.memoryTitle"),
            ...commonLabels,
          });
    const afterText =
      activeProposal.tab === "skills"
        ? serializeStructuredAsset(effectiveProposalMerged as StructuredAsset, {
            name: t("admin.memoryName"),
            description: t("admin.memoryDescription"),
            category: t("admin.memoryCategory"),
            tags: t("admin.memoryTagSet"),
            ...commonLabels,
          })
        : serializeExperienceAsset(effectiveProposalMerged as ExperienceAsset, {
            title: t("admin.memoryTitle"),
            ...commonLabels,
          });

    const changedFields = activeProposalFieldChanges
      .filter((field) => (proposalFieldDecisions[field.key] ?? "pending") === "accept")
      .map((field) => field.label);

    const isPreference =
      activeProposal.tab === "experience" &&
      isExperienceProfileAsset(activeProposal.before as ExperienceAsset);

    let prefYamlDiffLines: import("./shared").DiffLine[] = [];
    let prefBodyDiffLines: import("./shared").DiffLine[] = [];
    if (isPreference) {
      const beforeExp = activeProposal.before as ExperienceAsset;
      const afterExp = effectiveProposalMerged as ExperienceAsset;
      const beforeYaml = serializePreferenceYaml(beforeExp);
      const afterYaml = serializePreferenceYaml(afterExp);
      prefYamlDiffLines = buildDiffLinesWithInline(beforeYaml, afterYaml);
      const beforeBody = parsePreferenceYamlAndBody(beforeExp.content).bodyText;
      const afterBody = parsePreferenceYamlAndBody(afterExp.content).bodyText;
      prefBodyDiffLines = buildDiffLinesWithInline(beforeBody, afterBody);
    }

    return {
      beforeText,
      afterText,
      lines: buildDiffLinesWithInline(beforeText, afterText),
      changedFields,
      isPreference,
      prefYamlDiffLines,
      prefBodyDiffLines,
    };
  }, [
    activeProposal,
    activeProposalFieldChanges,
    effectiveProposalMerged,
    proposalFieldDecisions,
    t,
  ]);

  const acceptedFieldCount = useMemo(
    () =>
      activeProposalFieldChanges.filter(
        (field) => (proposalFieldDecisions[field.key] ?? "pending") === "accept",
      ).length,
    [activeProposalFieldChanges, proposalFieldDecisions],
  );
  const rejectedFieldCount = useMemo(
    () =>
      activeProposalFieldChanges.filter(
        (field) => (proposalFieldDecisions[field.key] ?? "pending") === "reject",
      ).length,
    [activeProposalFieldChanges, proposalFieldDecisions],
  );
  const pendingFieldCount = useMemo(
    () =>
      activeProposalFieldChanges.filter(
        (field) => (proposalFieldDecisions[field.key] ?? "pending") === "pending",
      ).length,
    [activeProposalFieldChanges, proposalFieldDecisions],
  );

  useEffect(() => {
    if (activeProposalId && !activeProposal) {
      if (isReviewRouteRequested) {
        return;
      }
      setActiveProposalId(undefined);
      if (reviewRouteTab) {
        navigateToMemoryList(reviewRouteTab);
      }
    }
  }, [
    activeProposal,
    activeProposalId,
    isReviewRouteRequested,
    navigateToMemoryList,
    reviewRouteTab,
  ]);

  const keyword = query.trim().toLowerCase();
  const hasStructuredFilter = Boolean(keyword || category || tag);
  const shouldFilterStructuredItemsLocally = activeTab !== "skills";
  const matchesStructuredFilter = useCallback(
    (item: StructuredAsset) => {
      if (!shouldFilterStructuredItemsLocally) {
        return true;
      }

      const matchesKeyword =
        !keyword ||
        item.name.toLowerCase().includes(keyword) ||
        item.description.toLowerCase().includes(keyword) ||
        item.content.toLowerCase().includes(keyword);
      const matchesCategory = !category || item.category === category;
      const matchesTag = !tag || item.tags.includes(tag);
      return matchesKeyword && matchesCategory && matchesTag;
    },
    [category, keyword, shouldFilterStructuredItemsLocally, tag],
  );

  const filteredExperienceItems = experienceAssets;
  useEffect(() => {
    const profileIds = experienceAssets
      .filter(isExperienceProfileAsset)
      .map((item) => item.id);
    const validIdSet = new Set(experienceAssets.map((item) => item.id));

    setExpandedExperienceProfileIds((previous) => {
      const next = previous.filter((id) => profileIds.includes(id));
      profileIds.forEach((id) => {
        if (!next.includes(id)) {
          next.push(id);
        }
      });
      return next.length === previous.length && next.every((id, index) => id === previous[index])
        ? previous
        : next;
    });
    setExperienceProfileDrafts((previous) => {
      const nextEntries = Object.entries(previous).filter(([id]) => validIdSet.has(id));
      if (nextEntries.length === Object.keys(previous).length) {
        return previous;
      }
      return Object.fromEntries(nextEntries);
    });
    setExperienceProfileEditTarget((previous) =>
      previous && validIdSet.has(previous.recordId) ? previous : null,
    );
  }, [experienceAssets]);

  const updateExperienceProfileDraft = useCallback(
    (record: ExperienceAsset, key: ExperienceProfileFieldKey, value: string) => {
      setExperienceProfileDrafts((previous) => ({
        ...previous,
        [record.id]: {
          ...(previous[record.id] || getExperienceProfileDraft(record)),
          [key]: value,
        },
      }));
    },
    [],
  );

  const resetExperienceProfileDraft = useCallback((record: ExperienceAsset) => {
    setExperienceProfileDrafts((previous) => {
      const next = { ...previous };
      delete next[record.id];
      return next;
    });
  }, []);

  const saveExperienceProfileDraft = useCallback(
    async (record: ExperienceAsset) => {
      const draft = experienceProfileDrafts[record.id] || getExperienceProfileDraft(record);

      setExperienceProfileSaving((previous) => new Set(previous).add(record.id));
      try {
        await upsertPreferenceAsset({
          title: record.title,
          content: record.content,
          protect: Boolean(record.protect),
          autoEvo: Boolean(record.autoEvo),
          agentPersona: draft.agentPersona.trim(),
          responseStyle: draft.responseStyle.trim(),
          resourceType: record.resourceType,
          preferredName: draft.preferredName.trim(),
        });
        resetExperienceProfileDraft(record);
        await refreshExperienceSection({ silent: true });
        message.success(
          t("admin.memoryProfileSaveSuccess", { defaultValue: "用户画像配置已保存" }),
        );
        return true;
      } catch (error) {
        console.error("Save user profile preference failed:", error);
        message.error(
          getLocalizedErrorMessage(
            error,
            t("admin.memoryProfileSaveFailed", {
              defaultValue: "保存用户画像配置失败",
            }),
          ) ||
            t("admin.memoryProfileSaveFailed", {
              defaultValue: "保存用户画像配置失败",
            }),
        );
        return false;
      } finally {
        setExperienceProfileSaving((previous) => {
          const next = new Set(previous);
          next.delete(record.id);
          return next;
        });
      }
    },
    [
      experienceProfileDrafts,
      refreshExperienceSection,
      resetExperienceProfileDraft,
      t,
    ],
  );

  const filteredGlossaryItems = glossaryAssets.filter((item) => {
    const matchesSource = !glossarySource || item.source === glossarySource;
    if (!matchesSource) {
      return false;
    }

    if (!keyword) {
      return true;
    }

    return (
      item.term.toLowerCase().includes(keyword) ||
      item.aliases.some((alias) => alias.toLowerCase().includes(keyword)) ||
      item.content.toLowerCase().includes(keyword)
    );
  });
  const glossaryAssetMap = useMemo(
    () => new Map(glossaryAssets.map((item) => [item.id, item])),
    [glossaryAssets],
  );
  const selectedGlossaryAssets = useMemo(
    () =>
      selectedGlossaryAssetIds
        .map((id) => glossaryAssetMap.get(id))
        .filter((item): item is GlossaryAsset => Boolean(item)),
    [glossaryAssetMap, selectedGlossaryAssetIds],
  );
  const availableGlossarySourceOptions: Array<{
    value: GlossarySource;
    label: string;
  }> = [
    { value: "user", label: t("admin.memoryGlossarySourceUser") },
    { value: "ai", label: t("admin.memoryGlossarySourceAI") },
  ];

  const filteredStructuredItems = currentStructuredItems.filter((item) =>
    matchesStructuredFilter(item),
  );

  const filteredSkillTree = useMemo<SkillTreeNode[]>(() => {
    const skillMap = new Map(skillAssets.map((item) => [item.id, item]));
    const rootSkills = skillAssets.filter(
      (item) => !item.parentId || !skillMap.has(item.parentId),
    );
    const matchedIds = new Set(
      skillAssets.filter((item) => matchesStructuredFilter(item)).map((item) => item.id),
    );

    return rootSkills
      .map((parent): SkillTreeNode | null => {
        const childItems = skillAssets.filter((item) => item.parentId === parent.id);
        const parentMatched = matchedIds.has(parent.id);
        const visibleChildren = childItems.filter(
          (item) => !hasStructuredFilter || parentMatched || matchedIds.has(item.id),
        );
        const visibleParent =
          !hasStructuredFilter || parentMatched || visibleChildren.length > 0;

        if (!visibleParent) {
          return null;
        }

        return {
          ...parent,
          children: visibleChildren.length ? visibleChildren : undefined,
        };
      })
      .filter((item): item is SkillTreeNode => Boolean(item));
  }, [hasStructuredFilter, matchesStructuredFilter, skillAssets]);

  const resetFilters = () => {
    setQuery("");
    setSearchInput("");
    setCategory(undefined);
    setTag(undefined);
    setGlossarySource(undefined);
  };

  const addChildSkillDraft = () => {
    setDraft((previous) => ({
      ...previous,
      childSkills: [...previous.childSkills, createChildSkillDraft()],
    }));
  };

  const updateChildSkillDraft = (
    tempId: string,
    patch: Partial<Omit<ChildSkillDraft, "tempId">>,
  ) => {
    setDraft((previous) => ({
      ...previous,
      childSkills: previous.childSkills.map((item) =>
        item.tempId === tempId ? { ...item, ...patch } : item,
      ),
    }));
  };

  const removeChildSkillDraft = (tempId: string) => {
    setDraft((previous) => ({
      ...previous,
      childSkills: previous.childSkills.filter((item) => item.tempId !== tempId),
    }));
  };

  const readFileAsText = (file: File) =>
    new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ""));
      reader.onerror = () => reject(reader.error);
      reader.readAsText(file);
    });

  const appendImportedSkillContent = (existingContent: string, importedContent: string) => {
    if (!existingContent.trim()) {
      return importedContent;
    }
    if (!importedContent.trim()) {
      return existingContent;
    }
    return `${existingContent.replace(/\s+$/, "")}\n\n${importedContent.replace(/^\s+/, "")}`;
  };

  const confirmSkillContentImportMode = (existingContent?: string) => {
    if (!existingContent?.trim()) {
      return Promise.resolve<"replace" | "append">("replace");
    }

    return new Promise<"replace" | "append">((resolve) => {
      Modal.confirm({
        title: t("admin.memoryUploadSkillContentMergeTitle"),
        content: t("admin.memoryUploadSkillContentMergeContent"),
        okText: t("admin.memoryUploadSkillContentMergeReplace"),
        cancelText: t("admin.memoryUploadSkillContentMergeAppend"),
        closable: false,
        maskClosable: false,
        keyboard: false,
        onOk: () => resolve("replace"),
        onCancel: () => resolve("append"),
      });
    });
  };

  const handleUploadSkillFile = async (
    file: File,
    options?: {
      childTempId?: string;
      parentOnlyMarkdown?: boolean;
    },
  ) => {
    const { childTempId, parentOnlyMarkdown = false } = options || {};

    if (!canUploadSkillFile(file.name, parentOnlyMarkdown)) {
      message.warning(
        t(
          parentOnlyMarkdown
            ? "admin.memoryUploadSkillTypeInvalidParent"
            : "admin.memoryUploadSkillTypeInvalid",
        ),
      );
      return;
    }

    try {
      const content = await readFileAsText(file);
      const inferredName = getBaseName(file.name);
      const frontMatter = isMarkdownSkillFile(file.name)
        ? parseMarkdownFrontMatter(content)
        : null;
      const hasFrontMatterMetadata = Boolean(
        frontMatter && (frontMatter.name || frontMatter.description),
      );
      const importedContent = frontMatter?.content ?? content;
      const existingContent = childTempId
        ? draft.childSkills.find((item) => item.tempId === childTempId)?.content
        : draft.content;
      const contentImportMode = await confirmSkillContentImportMode(existingContent);
      const resolveImportedContent = (currentContent: string) =>
        contentImportMode === "append"
          ? appendImportedSkillContent(currentContent, importedContent)
          : importedContent;

      const applyMainDraftFromUpload = (replaceFromFrontMatter: boolean) => {
        setDraft((previous) => {
          if (!hasFrontMatterMetadata) {
            return {
              ...previous,
              name: previous.name || inferredName,
              content: resolveImportedContent(previous.content),
            };
          }

          const nextName = replaceFromFrontMatter
            ? frontMatter?.name || previous.name || inferredName
            : previous.name || inferredName;
          const nextDescription = replaceFromFrontMatter
            ? frontMatter?.description || previous.description
            : previous.description;

          return {
            ...previous,
            name: nextName,
            description: nextDescription,
            content: resolveImportedContent(previous.content),
          };
        });
      };
      const fillMainDraftMissingMetadata = () => {
        setDraft((previous) => ({
          ...previous,
          name: previous.name || frontMatter?.name || inferredName,
          description: previous.description || frontMatter?.description || "",
          content: resolveImportedContent(previous.content),
        }));
      };

      if (childTempId) {
        setDraft((previous) => ({
          ...previous,
          childSkills: previous.childSkills.map((item) =>
            item.tempId === childTempId
              ? {
                  ...item,
                  name: item.name || inferredName,
                  description: item.description || frontMatter?.description || "",
                  content: resolveImportedContent(item.content),
                }
              : item,
          ),
        }));
      } else if (hasFrontMatterMetadata) {
        const hasExistingName = Boolean(draft.name.trim());
        const hasExistingDescription = Boolean(draft.description.trim());

        if (hasExistingName && hasExistingDescription) {
          Modal.confirm({
            title: t("admin.memoryUploadSkillMetadataReplaceTitle"),
            content: t("admin.memoryUploadSkillMetadataReplaceContent"),
            okText: t("admin.memoryUploadSkillMetadataReplaceConfirm"),
            cancelText: t("admin.memoryUploadSkillMetadataReplaceKeep"),
            onOk: () => applyMainDraftFromUpload(true),
            onCancel: () => applyMainDraftFromUpload(false),
          });
        } else {
          fillMainDraftMissingMetadata();
        }
      } else {
        applyMainDraftFromUpload(false);
      }

      message.success(t("admin.memoryUploadSkillSuccess"));
    } catch (error) {
      console.error("Read skill file failed:", error);
      message.error(t("admin.memoryUploadSkillFailed"));
    }
  };

  const createSkillUploadProps = (childTempId?: string): UploadProps => {
    const isParentSkillUpload = activeTab === "skills" && !childTempId && !draft.parentId;

    return {
      accept: isParentSkillUpload ? parentSkillUploadAccept : skillUploadAccept,
      maxCount: 1,
      showUploadList: false,
      beforeUpload: (file) => {
        void handleUploadSkillFile(file as File, {
          childTempId,
          parentOnlyMarkdown: isParentSkillUpload,
        });
        return Upload.LIST_IGNORE;
      },
    };
  };

  const syncShareParams = (nextTab?: MemoryTab, nextItemId?: string) => {
    const nextSearchParams = new URLSearchParams(searchParams);

    if (!routeListTab && !glossaryRouteItemId && !reviewRouteTab && nextTab) {
      nextSearchParams.set("tab", nextTab);
    } else {
      nextSearchParams.delete("tab");
    }

    if (nextItemId) {
      nextSearchParams.set("item", nextItemId);
    } else {
      nextSearchParams.delete("item");
    }

    if (nextSearchParams.toString() === searchParams.toString()) {
      return;
    }

    setSearchParams(nextSearchParams, { replace: true });
  };

  const openModal = (
    mode: ModalMode,
    item?: StructuredAsset | ExperienceAsset | GlossaryAsset,
    options?: { skipSkillDetailLoad?: boolean },
  ) => {
    setPendingGlossaryMergeSourceIds([]);
    setModalMode(mode);

    if (activeTab === "skills" && (mode === "add" || mode === "edit")) {
      void refreshParentSkillAssets();
    }

    if (!item) {
      setDraft(createDraft());
      setModalOpen(true);
      return;
    }

    if ("title" in item) {
      setDraft({
        id: item.id,
        title: item.title,
        agentPersona: item.agentPersona || "",
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
        content: item.content,
        protect: Boolean(item.protect),
        responseStyle: item.responseStyle || "",
        preferredName: item.preferredName || "",
      });
    } else if ("term" in item) {
      setDraft({
        id: item.id,
        title: "",
        agentPersona: "",
        name: "",
        description: "",
        category: "",
        tags: [],
        parentId: "",
        childSkills: [],
        term: item.term,
        group: item.group,
        aliases: [...item.aliases],
        source: item.source,
        content: item.content,
        protect: Boolean(item.protect),
        responseStyle: "",
        preferredName: "",
      });
    } else {
      setDraft(
        createStructuredDraft(item, {
          stripFrontMatter: activeTab === "skills" && mode !== "add",
        }),
      );

      if (activeTab === "skills" && mode !== "add" && !options?.skipSkillDetailLoad) {
        void (async () => {
          try {
            const detail = await getSkillAssetDetail(item.id);
            if (!detail) {
              return;
            }

            setDraft((previous) => {
              if (previous.id !== item.id) {
                return previous;
              }

              return createStructuredDraft(
                {
                  id: detail.id,
                  name: detail.name,
                  description: detail.description,
                  category: detail.category,
                  tags: detail.tags,
                  content: detail.content,
                  parentId: detail.parentId || previous.parentId,
                  protect: detail.protect,
                },
                { stripFrontMatter: true },
              );
            });
          } catch (error) {
            console.error("Load skill detail failed:", error);
          }
        })();
      }
    }

    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    setPendingGlossaryMergeSourceIds([]);
    syncShareParams(activeTab);
  };

  const openShareModal = (tab: ShareableTab, item: StructuredAsset | ExperienceAsset) => {
    setShareTarget({ tab, item });
    setShareDraft({ groupIds: [], userIds: [], message: "" });
    setShareModalOpen(true);
  };

  const closeShareModal = () => {
    shareStatusRequestIdRef.current += 1;
    setShareModalOpen(false);
    setShareTarget(null);
    setShareDraft({ groupIds: [], userIds: [], message: "" });
    setShareStatusLoading(false);
    setShareStatusError("");
    setShareStatusRecords([]);
  };

  const openSkillShareCenter = (nextTab: SkillShareCenterTab = "incoming") => {
    setSkillShareCenterTab(nextTab);
    setSkillShareCenterOpen(true);
    void refreshSkillShareCenter({ showErrorToast: true });
  };

  const closeSkillShareCenter = () => {
    setSkillShareCenterOpen(false);
  };

  const buildStructuredAssetFromSkillShare = (
    share: SkillShareRecord,
  ): StructuredAsset => ({
    id: share.sourceSkillId || share.skillId || share.id,
    name: share.skillName || t("admin.memorySkillShareUnknownSkill"),
    description: share.skillDescription,
    category: share.category,
    tags: share.tags,
    content: share.skillContent || share.message || "",
    protect: false,
  });

  const previewSkillShare = async (share: SkillShareRecord) => {
    setSkillShareAction(share.id, "preview");

    try {
      const detail = await getSkillAssetDetail(share.sourceSkillId || share.skillId || share.id);
      openModal(
        "view",
        detail || buildStructuredAssetFromSkillShare(share),
        { skipSkillDetailLoad: true },
      );
    } catch (error) {
      console.error("Load skill detail failed:", error);
      openModal("view", buildStructuredAssetFromSkillShare(share), {
        skipSkillDetailLoad: true,
      });
    } finally {
      setSkillShareAction(share.id);
    }
  };

  const acceptIncomingSkillShare = async (share: SkillShareRecord) => {
    setSkillShareAction(share.id, "accept");

    try {
      await acceptSkillShare(share.id);
      message.success(t("admin.memorySkillShareAcceptSuccess"));
      await Promise.all([
        refreshSkillAssets(),
        refreshSkillShareCenter({ silent: true }),
      ]);
    } catch (error) {
      console.error("Accept skill share failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillShareAcceptFailed")) ||
          t("admin.memorySkillShareAcceptFailed"),
      );
    } finally {
      setSkillShareAction(share.id);
    }
  };

  const rejectIncomingSkillShare = async (share: SkillShareRecord) => {
    setSkillShareAction(share.id, "reject");

    try {
      await rejectSkillShare(share.id);
      message.success(t("admin.memorySkillShareRejectSuccess"));
      await refreshSkillShareCenter({ silent: true });
    } catch (error) {
      console.error("Reject skill share failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillShareRejectFailed")) ||
          t("admin.memorySkillShareRejectFailed"),
      );
    } finally {
      setSkillShareAction(share.id);
    }
  };

  const handleExperienceFeatureToggle = async (checked: boolean) => {
    const previousValue = experienceFeatureEnabled;
    setExperienceFeatureEnabled(checked);
    setExperienceSettingSaving(true);

    try {
      const enabled = await updatePersonalizationSetting(checked);
      setExperienceFeatureEnabled(enabled);
      await refreshExperienceSection({ silent: true });
      message.success(t("admin.memoryExperienceSettingSaveSuccess"));
    } catch (error) {
      console.error("Update preference setting failed:", error);
      setExperienceFeatureEnabled(previousValue);
      await refreshExperienceSection({ silent: true });
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryExperienceSettingSaveFailed")) ||
          t("admin.memoryExperienceSettingSaveFailed"),
      );
    } finally {
      setExperienceSettingSaving(false);
    }
  };

  const loadExperienceChangeProposal = async (
    item: ExperienceAsset,
  ): Promise<ExperienceChangeProposal | null> => {
    return {
      id: `experience-draft-${item.id}`,
      tab: "experience",
      targetId: item.id,
      before: cloneExperienceAsset(item),
      after: cloneExperienceAsset(item),
      backendSuggestions: [],
      backendSuggestionPage: 1,
      backendSuggestionPageSize: backendSuggestionPageSize,
      backendSuggestionTotal: 0,
    };
  };

  const loadSkillChangeProposal = async (
    item: StructuredAsset,
  ): Promise<ChangeProposal | null> => {
    const detail = await getSkillAssetDetail(item.id).catch((error) => {
      console.error("Load skill detail for review failed:", error);
      return null;
    });

    const reviewItem: StructuredAsset = detail
      ? {
          ...item,
          id: detail.id,
          name: detail.name,
          description: detail.description,
          category: detail.category,
          tags: detail.tags,
          content: detail.content,
          parentId: detail.parentId,
          protect: detail.protect,
          fileExt: detail.fileExt,
          isEnabled: detail.isEnabled,
          hasPendingReviewSuggestions:
            detail.hasPendingReviewSuggestions ?? item.hasPendingReviewSuggestions,
          hasPendingReviewResult:
            detail.hasPendingReviewResult ?? item.hasPendingReviewResult,
          reviewStatus: detail.reviewStatus || item.reviewStatus,
          suggestionStatus: detail.suggestionStatus || item.suggestionStatus,
          nodeType: detail.nodeType || item.nodeType,
          updateStatus: detail.updateStatus || item.updateStatus,
        }
      : item;

    return {
      id: `skill-draft-${reviewItem.id}`,
      tab: "skills",
      targetId: reviewItem.id,
      before: cloneStructuredAsset(reviewItem),
      after: cloneStructuredAsset(reviewItem),
      backendSuggestions: [],
      backendSuggestionPage: 1,
      backendSuggestionPageSize: backendSuggestionPageSize,
      backendSuggestionTotal: 0,
    };
  };

  const openChangeReview = async (
    tab: ChangeProposalTab,
    itemId: string,
    skillUpdateStatus?: string,
    options?: { forceReload?: boolean; syncRoute?: boolean },
  ): Promise<boolean> => {
    if (options?.syncRoute !== false) {
      reviewRouteReloadKeyRef.current = `${tab}:${itemId}`;
    }
    const proposal = getPendingProposal(tab, itemId);
    const shouldReloadProposal = options?.forceReload ?? true;
    if (!proposal || shouldReloadProposal) {
      if (tab === "skills") {
        const matchedSkill = skillAssets.find((item) => item.id === itemId);
        const hasReviewableDraft = matchedSkill
          ? hasSkillDraftPreviewStatus(matchedSkill)
          : false;

        if (
          shouldReloadProposal ||
          isSkillUpdatePending(skillUpdateStatus) ||
          hasReviewableDraft
        ) {
          if (!matchedSkill) {
            message.warning(t("admin.memoryDiffTargetMissing"));
            return false;
          }

          setReviewSuggestionLoadingId(itemId);
          try {
            const backendProposal = await loadSkillChangeProposal(matchedSkill);
            if (!backendProposal) {
              setChangeProposals((previous) =>
                previous.filter(
                  (item) => !(item.tab === "skills" && item.targetId === itemId),
                ),
              );
              message.info(t("admin.memoryDiffNoPending"));
              return false;
            }

            setChangeProposals((previous) => {
              const next = previous.filter(
                (item) =>
                  !(item.tab === "skills" && item.targetId === backendProposal.targetId),
              );
              return [...next, backendProposal];
            });
            setActiveProposalId(backendProposal.id);
            if (options?.syncRoute !== false) {
              reviewRouteReloadKeyRef.current = `${tab}:${itemId}`;
              navigateToChangeReview(tab, itemId);
            }
          } catch (error) {
            console.error("Load skill draft preview failed:", error);
            message.error(
              getLocalizedErrorMessage(error, t("admin.memorySkillDraftPreviewFailed")) ||
                t("admin.memorySkillDraftPreviewFailed"),
            );
            return false;
          } finally {
            setReviewSuggestionLoadingId("");
          }
          return true;
        }
      }

      if (
        tab === "experience" &&
        (shouldReloadProposal ||
          experienceAssets.some((item) => item.id === itemId && hasDraftPreviewStatus(item)))
      ) {
        const matchedExperience = experienceAssets.find((item) => item.id === itemId);
        if (!matchedExperience) {
          message.warning(t("admin.memoryDiffTargetMissing"));
          return false;
        }

        setReviewSuggestionLoadingId(itemId);
        try {
          const backendProposal = await loadExperienceChangeProposal(matchedExperience);
          if (!backendProposal) {
            setChangeProposals((previous) =>
              previous.filter(
                (item) => !(item.tab === "experience" && item.targetId === itemId),
              ),
            );
            message.info(t("admin.memoryPreferenceDraftPreviewFailed"));
            return false;
          }

          setChangeProposals((previous) => {
            const next = previous.filter(
              (item) =>
                !(
                  item.tab === "experience" &&
                  item.targetId === backendProposal.targetId
                ),
            );
            return [...next, backendProposal];
          });
          setActiveProposalId(backendProposal.id);
          if (options?.syncRoute !== false) {
            reviewRouteReloadKeyRef.current = `${tab}:${itemId}`;
            navigateToChangeReview(tab, itemId);
          }
          } catch (error) {
            console.error("Load preference draft preview failed:", error);
            message.error(
              getLocalizedErrorMessage(error, t("admin.memoryPreferenceDraftPreviewFailed")) ||
                t("admin.memoryPreferenceDraftPreviewFailed"),
          );
          return false;
        } finally {
          setReviewSuggestionLoadingId("");
        }
        return true;
      }

      message.info(t("admin.memoryDiffNoPending"));
      return false;
    }

    const itemExists =
      tab === "skills"
        ? skillAssets.some((item) => item.id === itemId)
        : experienceAssets.some((item) => item.id === itemId);

    if (!itemExists) {
      setChangeProposals((previous) =>
        previous.filter((item) => item.id !== proposal.id),
      );
      message.warning(t("admin.memoryDiffTargetMissing"));
      return false;
    }

    setActiveProposalId(proposal.id);
    if (options?.syncRoute !== false) {
      reviewRouteReloadKeyRef.current = `${tab}:${itemId}`;
      navigateToChangeReview(tab, itemId);
    }
    return true;
  };

  const setFieldDecision = (
    fieldKey: ProposalFieldKey,
    decision: ProposalFieldDecision,
  ) => {
    setProposalFieldDecisions((previous) => ({ ...previous, [fieldKey]: decision }));
  };
  const markBackendSuggestionReviewed = (suggestionId: string) => {
    setReviewedBackendSuggestionIds((previous) =>
      previous.includes(suggestionId) ? previous : [...previous, suggestionId],
    );
  };
  const markBackendSuggestionsReviewed = (suggestionIds: string[]) => {
    setReviewedBackendSuggestionIds((previous) => [
      ...previous,
      ...suggestionIds.filter((item) => !previous.includes(item)),
    ]);
  };
  const markBackendSuggestionApproved = (suggestionId: string) => {
    setApprovedBackendSuggestionIds((previous) =>
      previous.includes(suggestionId) ? previous : [...previous, suggestionId],
    );
  };
  const markBackendSuggestionRejected = (suggestionId: string) => {
    setRejectedBackendSuggestionIds((previous) =>
      previous.includes(suggestionId) ? previous : [...previous, suggestionId],
    );
  };
  const markBackendSuggestionsApproved = (suggestionIds: string[]) => {
    setApprovedBackendSuggestionIds((previous) => [
      ...previous,
      ...suggestionIds.filter((item) => !previous.includes(item)),
    ]);
  };
  const markBackendSuggestionsRejected = (suggestionIds: string[]) => {
    setRejectedBackendSuggestionIds((previous) => [
      ...previous,
      ...suggestionIds.filter((item) => !previous.includes(item)),
    ]);
  };
  const removeBackendSuggestionsFromProposal = (
    proposalId: string,
    handledSuggestionIds: string[],
  ) => {
    const handledIdSet = new Set(handledSuggestionIds);

    setChangeProposals((previous) =>
      previous.map((proposal) => {
        if (proposal.id !== proposalId) {
          return proposal;
        }

        const remainingSuggestions =
          proposal.backendSuggestions?.filter((item) => !handledIdSet.has(item.id)) || [];

        return {
          ...proposal,
          backendSuggestionId: remainingSuggestions[0]?.id,
          backendSuggestions: remainingSuggestions,
          backendSuggestionTotal: Math.max(
            remainingSuggestions.length,
            (proposal.backendSuggestionTotal || remainingSuggestions.length) -
              handledSuggestionIds.length,
          ),
        };
      }),
    );
  };
  const appendBackendSuggestionPageToProposal = (
    proposalId: string,
    suggestionPage: EvolutionSuggestionListResult,
  ) => {
    setChangeProposals((previous) =>
      previous.map((proposal) => {
        if (proposal.id !== proposalId) {
          return proposal;
        }

        const mergedSuggestions = mergeEvolutionSuggestionRecords(
          proposal.backendSuggestions || [],
          suggestionPage.items,
        );

        return {
          ...proposal,
          backendSuggestionId: mergedSuggestions[0]?.id,
          backendSuggestions: mergedSuggestions,
          backendSuggestionPage: suggestionPage.page,
          backendSuggestionPageSize: suggestionPage.pageSize,
          backendSuggestionTotal: Math.max(mergedSuggestions.length, suggestionPage.total),
        };
      }),
    );
  };
  const replaceBackendSuggestionPageInProposal = (
    proposalId: string,
    suggestionPage: EvolutionSuggestionListResult,
  ) => {
    setChangeProposals((previous) =>
      previous.map((proposal) => {
        if (proposal.id !== proposalId) {
          return proposal;
        }

        return {
          ...proposal,
          backendSuggestionId: suggestionPage.items[0]?.id,
          backendSuggestions: suggestionPage.items,
          backendSuggestionPage: suggestionPage.page,
          backendSuggestionPageSize: suggestionPage.pageSize,
          backendSuggestionTotal: Math.max(suggestionPage.items.length, suggestionPage.total),
        };
      }),
    );
  };
  const clearBackendSuggestionSubmitting = (suggestionIds: string[]) => {
    setBackendSuggestionSubmitting((previous) => {
      const next = { ...previous };
      suggestionIds.forEach((item) => {
        delete next[item];
      });
      return next;
    });
  };
  const setBackendSuggestionSelected = (suggestionId: string, checked: boolean) => {
    const suggestion = activeBackendSuggestions.find((item) => item.id === suggestionId);
    if (suggestion && !isBackendSuggestionSelectable(suggestion)) {
      return;
    }
    setSelectedBackendSuggestionIds((previous) => {
      if (checked) {
        return previous.includes(suggestionId) ? previous : [...previous, suggestionId];
      }
      return previous.filter((item) => item !== suggestionId);
    });
  };
  const setAllBackendSuggestionsSelected = (checked: boolean) => {
    setSelectedBackendSuggestionIds(checked ? [...selectableBackendSuggestionIds] : []);
  };
  const clearSelectedBackendSuggestions = () => {
    if (!selectedBackendSuggestionIds.length) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return;
    }
    setSelectedBackendSuggestionIds([]);
  };
  const getFieldDecisionActionKey = (field: ProposalFieldChange) =>
    `${activeProposal?.id || "proposal"}:${field.key}`;
  const submitFieldDecision = async (
    field: ProposalFieldChange,
    decision: Extract<ProposalFieldDecision, "accept" | "reject">,
  ) => {
    const actionKey = getFieldDecisionActionKey(field);
    const suggestionId = field.backendSuggestionId;

    if (!suggestionId || reviewedBackendSuggestionIds.includes(suggestionId)) {
      setFieldDecision(field.key, decision);
      if (decision === "accept") {
        goToReviewPreview();
      }
      return;
    }

    setFieldDecisionSubmitting((previous) => ({
      ...previous,
      [actionKey]: decision,
    }));

    try {
      if (decision === "accept") {
        await approveEvolutionSuggestion(suggestionId);
        message.success(t("admin.memoryDiffApproveSuccess"));
        markBackendSuggestionApproved(suggestionId);
      } else {
        await rejectEvolutionSuggestion(suggestionId);
        message.success(t("admin.memoryDiffRejectSuccess"));
        markBackendSuggestionRejected(suggestionId);
      }

      markBackendSuggestionReviewed(suggestionId);
      setFieldDecision(field.key, decision);
      if (decision === "accept") {
        goToReviewPreview();
      }
    } catch (error) {
      console.error("Submit evolution suggestion field decision failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryExperienceSaveFailed")) ||
          t("admin.memoryExperienceSaveFailed"),
      );
    } finally {
      setFieldDecisionSubmitting((previous) => {
        const next = { ...previous };
        delete next[actionKey];
        return next;
      });
    }
  };
  const submitBackendSuggestionDecision = async (
    suggestion: EvolutionSuggestionRecord,
    decision: Extract<ProposalFieldDecision, "accept" | "reject">,
  ) => {
    if (!activeProposal) {
      return;
    }
    if (backendSuggestionMutationLockRef.current || isAnyBackendSuggestionMutating) {
      return;
    }

    const suggestionId = suggestion.id;
    const shouldDirectDeleteSkill =
      activeProposal.tab === "skills" && decision === "accept" && isSkillRemoveSuggestion(suggestion);
    backendSuggestionMutationLockRef.current = true;
    setBackendSuggestionSubmitting((previous) => ({
      ...previous,
      [suggestionId]: decision,
    }));

    try {
      if (shouldDirectDeleteSkill) {
        await removeSkillAsset(activeProposal.targetId);
        setChangeProposals((previous) =>
          previous.filter((item) => item.id !== activeProposal.id),
        );
        setActiveProposalId(undefined);
        setSelectedBackendSuggestionIds([]);
        navigateToMemoryList("skills");
        await refreshSkillAssets();
        message.success(t("admin.memorySkillDeleteSuccess"));
        return;
      }

      const nextApprovedSuggestionIds =
        decision === "accept"
          ? approvedBackendSuggestionIds.includes(suggestionId)
            ? approvedBackendSuggestionIds
            : [...approvedBackendSuggestionIds, suggestionId]
          : approvedBackendSuggestionIds;

      if (decision === "accept") {
        setActiveReviewStep(1);
        setBackendDraftLoading(true);
        await approveEvolutionSuggestion(suggestionId);
        message.success(t("admin.memoryDiffBatchApproveSuccess", { count: 1 }));
        markBackendSuggestionApproved(suggestionId);
      } else {
        await rejectEvolutionSuggestion(suggestionId);
        message.success(t("admin.memoryDiffRejectSuccess"));
        markBackendSuggestionRejected(suggestionId);
      }

      markBackendSuggestionReviewed(suggestionId);
      removeBackendSuggestionsFromProposal(activeProposal.id, [suggestionId]);
      setSelectedBackendSuggestionIds((previous) =>
        previous.filter((item) => item !== suggestionId),
      );
      if (decision === "accept") {
        await loadBackendDraftPreview(nextApprovedSuggestionIds);
        if (activeProposal.tab === "experience") {
          await refreshExperienceAssets({ silent: true });
        } else {
          await refreshSkillAssets({ preserveChangeProposals: true });
        }
      } else {
        if (activeProposal.tab === "experience") {
          await refreshExperienceAssets({ silent: true });
        } else {
          await refreshSkillAssets({ preserveChangeProposals: true });
        }
      }
    } catch (error) {
      console.error("Submit backend suggestion decision failed:", error);
      if (decision === "accept") {
        setActiveReviewStep(0);
        setBackendDraftLoading(false);
      }
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryExperienceSaveFailed")) ||
          t("admin.memoryExperienceSaveFailed"),
      );
    } finally {
      clearBackendSuggestionSubmitting([suggestionId]);
      backendSuggestionMutationLockRef.current = false;
    }
  };
  const submitBackendSuggestionBatchDecision = async (
    decision: Extract<ProposalFieldDecision, "accept" | "reject">,
  ) => {
    if (!activeProposal) {
      return;
    }
    if (backendSuggestionMutationLockRef.current || isAnyBackendSuggestionMutating) {
      return;
    }

    const suggestionIds = selectedBackendSuggestionIds.filter((item) =>
      selectableBackendSuggestionIds.includes(item),
    );
    if (!suggestionIds.length) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return;
    }
    const selectedSuggestions = activeBackendSuggestions.filter((item) =>
      suggestionIds.includes(item.id),
    );
    const shouldDirectDeleteSkill =
      activeProposal.tab === "skills" &&
      decision === "accept" &&
      selectedSuggestions.some((item) => isSkillRemoveSuggestion(item));

    backendSuggestionMutationLockRef.current = true;
    setBackendSuggestionBatchSubmitting(decision);
    setBackendSuggestionSubmitting((previous) => ({
      ...previous,
      ...suggestionIds.reduce<Record<string, ProposalFieldDecision>>((result, item) => {
        result[item] = decision;
        return result;
      }, {}),
    }));

    try {
      if (shouldDirectDeleteSkill) {
        await removeSkillAsset(activeProposal.targetId);
        setChangeProposals((previous) =>
          previous.filter((item) => item.id !== activeProposal.id),
        );
        setActiveProposalId(undefined);
        setSelectedBackendSuggestionIds([]);
        navigateToMemoryList("skills");
        await refreshSkillAssets();
        message.success(t("admin.memorySkillDeleteSuccess"));
        return;
      }

      const nextApprovedSuggestionIds =
        decision === "accept"
          ? [
              ...approvedBackendSuggestionIds,
              ...suggestionIds.filter((item) => !approvedBackendSuggestionIds.includes(item)),
            ]
          : approvedBackendSuggestionIds;

      if (decision === "accept") {
        setActiveReviewStep(1);
        setBackendDraftLoading(true);
        await batchApproveEvolutionSuggestions(suggestionIds);
        message.success(
          t("admin.memoryDiffBatchApproveSuccess", { count: suggestionIds.length }),
        );
        markBackendSuggestionsApproved(suggestionIds);
      } else {
        await batchRejectEvolutionSuggestions(suggestionIds);
        message.success(
          t("admin.memoryDiffBatchRejectSuccess", { count: suggestionIds.length }),
        );
        markBackendSuggestionsRejected(suggestionIds);
      }

      markBackendSuggestionsReviewed(suggestionIds);
      removeBackendSuggestionsFromProposal(activeProposal.id, suggestionIds);
      setSelectedBackendSuggestionIds((previous) =>
        previous.filter((item) => !suggestionIds.includes(item)),
      );

      if (decision === "accept") {
        await loadBackendDraftPreview(nextApprovedSuggestionIds);
        if (activeProposal.tab === "experience") {
          await refreshExperienceAssets({ silent: true });
        } else {
          await refreshSkillAssets({ preserveChangeProposals: true });
        }
      } else {
        if (activeProposal.tab === "experience") {
          await refreshExperienceAssets({ silent: true });
        } else {
          await refreshSkillAssets({ preserveChangeProposals: true });
        }
      }
    } catch (error) {
      console.error("Submit backend suggestion batch decision failed:", error);
      if (decision === "accept") {
        setActiveReviewStep(0);
        setBackendDraftLoading(false);
      }
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryExperienceSaveFailed")) ||
          t("admin.memoryExperienceSaveFailed"),
      );
    } finally {
      clearBackendSuggestionSubmitting(suggestionIds);
      setBackendSuggestionBatchSubmitting("");
      backendSuggestionMutationLockRef.current = false;
    }
  };
  const buildBackendDraftUserInstruct = (extraInstruction = "") => {
    const instructions = [
      activeProposal?.tab === "skills"
        ? t("admin.memorySkillDraftDefaultInstruction")
        : "",
      extraInstruction.trim(),
    ].filter(Boolean);
    return instructions.join("\n");
  };
  const getActiveManagedDraftKind = () =>
    activeProposal?.tab === "experience"
      ? resolveManagedPreferenceDraftKind(activeProposal.before.resourceType)
      : backendDraftKind;
  const startBackendDraftPreviewLoading = () => {
    setActiveReviewStep(1);
    setBackendDraftPreview(null);
    setIsPreviewContentEditing(false);
    setManualPreviewContentDraft("");
    setBackendDraftLoading(true);
  };
  const loadCurrentDraftPreview = async () => {
    if (!activeProposal) {
      return false;
    }

    startBackendDraftPreviewLoading();
    try {
      const preview =
        activeProposal.tab === "skills"
          ? await previewSkillDraft(activeProposal.targetId)
          : await previewManagedPreferenceDraft(
              resolveManagedPreferenceDraftKind(activeProposal.before.resourceType),
            );
      if (activeProposal.tab === "experience") {
        setBackendDraftKind(resolveManagedPreferenceDraftKind(activeProposal.before.resourceType));
      }
      setBackendDraftPreview(preview);
      return true;
    } catch (error) {
      console.error("Load draft preview failed:", error);
      const errorKey =
        activeProposal.tab === "skills"
          ? "admin.memorySkillDraftPreviewFailed"
          : "admin.memoryPreferenceDraftPreviewFailed";
      message.error(
        getLocalizedErrorMessage(error, t(errorKey)) || t(errorKey),
      );
      return false;
    } finally {
      setBackendDraftLoading(false);
    }
  };
  const loadBackendDraftPreview = async (
    suggestionIds: string[],
    extraInstruction = "",
    options?: { omitSuggestionIds?: boolean },
  ) => {
    const shouldOmitSuggestionIds = Boolean(options?.omitSuggestionIds);

    if (!suggestionIds.length && !shouldOmitSuggestionIds) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return false;
    }

    startBackendDraftPreviewLoading();
    try {
      const userInstruct = shouldOmitSuggestionIds
        ? extraInstruction.trim()
        : buildBackendDraftUserInstruct(extraInstruction);
      const preview =
        activeProposal?.tab === "skills"
          ? await (async () => {
              await generateSkillDraft(activeProposal.targetId, {
                suggestionIds: shouldOmitSuggestionIds ? undefined : suggestionIds,
                userInstruct,
              });
              return previewSkillDraft(activeProposal.targetId);
            })()
          : await (async () => {
              const draftKind = getActiveManagedDraftKind();
              await generateManagedPreferenceDraft(draftKind, {
                suggestionIds: shouldOmitSuggestionIds ? undefined : suggestionIds,
                userInstruct,
              });
              return previewManagedPreferenceDraft(draftKind);
            })();
      setBackendDraftPreview(preview);
      return true;
    } catch (error) {
      console.error("Load managed draft preview failed:", error);
      const errorKey =
        activeProposal?.tab === "skills"
          ? "admin.memorySkillDraftPreviewFailed"
          : "admin.memoryPreferenceDraftPreviewFailed";
      message.error(
        getLocalizedErrorMessage(error, t(errorKey)) || t(errorKey),
      );
      return false;
    } finally {
      setBackendDraftLoading(false);
    }
  };
  const confirmBackendDraft = async () => {
    if (!activeProposal) {
      return;
    }

    setBackendDraftSubmitting("confirm");
    try {
      if (activeProposal.tab === "skills") {
        await confirmSkillDraft(activeProposal.targetId);
      } else {
        await confirmManagedPreferenceDraft(getActiveManagedDraftKind());
      }
      message.success(
        activeProposal.tab === "skills"
          ? t("admin.memorySkillDraftConfirmSuccess")
          : t("admin.memoryPreferenceDraftConfirmSuccess"),
      );
      confirmedDraftProposalIdsRef.current.add(activeProposal.id);
      setChangeProposals((previous) =>
        previous.filter((item) => item.id !== activeProposal.id),
      );
      setActiveProposalId(undefined);
      if (activeProposal.tab === "skills") {
        await refreshSkillAssets({ preserveChangeProposals: true });
      } else {
        await refreshExperienceAssets({ silent: true });
      }
      navigateToMemoryList(activeProposal.tab);
    } catch (error) {
      console.error("Confirm managed draft failed:", error);
      message.error(
        getLocalizedErrorMessage(
          error,
          activeProposal.tab === "skills"
            ? t("admin.memorySkillDraftConfirmFailed")
            : t("admin.memoryPreferenceDraftConfirmFailed"),
        ) ||
          (activeProposal.tab === "skills"
            ? t("admin.memorySkillDraftConfirmFailed")
            : t("admin.memoryPreferenceDraftConfirmFailed")),
      );
    } finally {
      setBackendDraftSubmitting("");
    }
  };
  const discardBackendDraft = async () => {
    setBackendDraftSubmitting("discard");
    try {
      if (activeProposal?.tab === "skills") {
        await discardSkillDraft(activeProposal.targetId);
      } else {
        await discardManagedPreferenceDraft(getActiveManagedDraftKind());
      }
      message.success(
        activeProposal?.tab === "skills"
          ? t("admin.memorySkillDraftDiscardSuccess")
          : t("admin.memoryPreferenceDraftDiscardSuccess"),
      );
      setBackendDraftPreview(null);
      setApprovedBackendSuggestionIds([]);
      setRejectedBackendSuggestionIds([]);
      setSelectedBackendSuggestionIds([]);
      if (activeProposal?.tab === "skills" || activeProposal?.tab === "experience") {
        setChangeProposals((previous) =>
          previous.filter((item) => item.id !== activeProposal.id),
        );
        setActiveProposalId(undefined);
        navigateToMemoryList(activeProposal.tab);
        if (activeProposal.tab === "skills") {
          await refreshSkillAssets();
        } else {
          await refreshExperienceSection({ silent: true });
        }
        return;
      }
      setActiveReviewStep(0);
      await refreshExperienceSection({ silent: true });
    } catch (error) {
      console.error("Discard managed draft failed:", error);
      message.error(
        getLocalizedErrorMessage(
          error,
          activeProposal?.tab === "skills"
            ? t("admin.memorySkillDraftDiscardFailed")
            : t("admin.memoryPreferenceDraftDiscardFailed"),
        ) ||
          (activeProposal?.tab === "skills"
            ? t("admin.memorySkillDraftDiscardFailed")
            : t("admin.memoryPreferenceDraftDiscardFailed")),
      );
    } finally {
      setBackendDraftSubmitting("");
    }
  };
  const discardBackendDraftAndReturn = () => {
    Modal.confirm({
      title: t("admin.memoryDiffDiscardDraftAndBackConfirmTitle"),
      content: t("admin.memoryDiffDiscardDraftAndBackConfirmContent"),
      okText: t("admin.memoryDiffDiscardDraftAndBackConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => discardBackendDraft(),
    });
  };
  const setFieldSelected = (fieldKey: ProposalFieldKey, checked: boolean) => {
    setSelectedFieldKeys((previous) => {
      if (checked) {
        return previous.includes(fieldKey) ? previous : [...previous, fieldKey];
      }
      return previous.filter((key) => key !== fieldKey);
    });
  };
  const setAllFieldsSelected = (checked: boolean) => {
    setSelectedFieldKeys(checked ? [...currentProposalFieldKeys] : []);
  };
  const setAllFieldDecision = (decision: ProposalFieldDecision): boolean => {
    if (!selectedFieldKeys.length) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return false;
    }

    setProposalFieldDecisions((previous) => {
      const next = { ...previous };
      selectedFieldKeys.forEach((fieldKey) => {
        next[fieldKey] = decision;
      });
      return next;
    });
    return true;
  };
  const handleBatchAcceptAndGoPreview = () => {
    if (setAllFieldDecision("accept")) {
      goToReviewPreview();
    }
  };
  const handleBatchRejectWithConfirm = () => {
    if (!selectedFieldKeys.length) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return;
    }

    Modal.confirm({
      title: t("admin.memoryDiffBatchRejectConfirmTitle"),
      content: t("admin.memoryDiffBatchRejectConfirmContent"),
      okText: t("admin.memoryDiffBatchRejectConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => {
        setAllFieldDecision("reject");
      },
    });
  };
  const clearSelectedFields = () => {
    if (!selectedFieldKeys.length) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return;
    }
    setSelectedFieldKeys([]);
  };
  const handleBackendBatchAccept = () => {
    void submitBackendSuggestionBatchDecision("accept");
  };
  const handleBackendBatchRejectWithConfirm = () => {
    if (!selectedBackendSuggestionIds.length) {
      message.info(t("admin.memoryDiffSelectFieldFirst"));
      return;
    }

    Modal.confirm({
      title: t("admin.memoryDiffBatchRejectConfirmTitle"),
      content: t("admin.memoryDiffBatchRejectConfirmContent"),
      okText: t("admin.memoryDiffBatchRejectConfirmOk"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: () => submitBackendSuggestionBatchDecision("reject"),
    });
  };
  const loadMoreBackendSuggestions = useCallback(async () => {
    if (
      !activeProposal ||
      activeProposal.backendDraftPreview ||
      activeProposal.backendSuggestions ||
      activeProposal.tab !== "experience" ||
      backendSuggestionLoadingMore ||
      !backendSuggestionHasMore
    ) {
      return;
    }

    const requestId = backendSuggestionLoadMoreRequestIdRef.current + 1;
    backendSuggestionLoadMoreRequestIdRef.current = requestId;
    setBackendSuggestionLoadingMore(true);
    setBackendSuggestionLoadMoreError("");

    try {
      const suggestionPage = await listEvolutionSuggestions({
        page: activeBackendSuggestionPage + 1,
        pageSize: activeBackendSuggestionPageSize,
        ...getPreferenceSuggestionResourceParam(activeProposal.before),
      });

      if (backendSuggestionLoadMoreRequestIdRef.current !== requestId) {
        return;
      }

      appendBackendSuggestionPageToProposal(activeProposal.id, suggestionPage);
    } catch (error) {
      if (backendSuggestionLoadMoreRequestIdRef.current !== requestId) {
        return;
      }

      setBackendSuggestionLoadMoreError(
        getLocalizedErrorMessage(error, t("admin.memoryPreferenceDraftPreviewFailed")) ||
          t("admin.memoryPreferenceDraftPreviewFailed"),
      );
    } finally {
      if (backendSuggestionLoadMoreRequestIdRef.current === requestId) {
        setBackendSuggestionLoadingMore(false);
      }
    }
  }, [
    activeBackendSuggestionPage,
    activeBackendSuggestionPageSize,
    activeProposal,
    backendSuggestionHasMore,
    backendSuggestionLoadingMore,
    t,
  ]);

  const sendReviewQuestion = async () => {
    const text = qaQuestionDraft.trim();
    if (!text) {
      return;
    }

    setQaQuestionDraft("");

    if (
      activeProposal?.backendSuggestions &&
      activeReviewStep === 1
    ) {
      const updated = await loadBackendDraftPreview(approvedBackendSuggestionIds, text, {
        omitSuggestionIds: true,
      });
      if (updated) {
        message.success(t("admin.memoryDiffQaSendSuccess"));
      }
      return;
    }

    message.success(t("admin.memoryDiffQaSendSuccess"));
  };

  const handleReviewQuestionKeyDown = (
    event: React.KeyboardEvent<HTMLTextAreaElement>,
  ) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void sendReviewQuestion();
    }
  };

  const goToReviewPreview = () => {
    if (
      activeProposal?.backendSuggestions &&
      (activeProposal.tab === "skills" || activeProposal.tab === "experience")
    ) {
      void loadCurrentDraftPreview();
      return;
    }

    if (
      activeProposal?.backendSuggestions &&
      (activeProposal.backendSuggestions?.length || approvedBackendSuggestionIds.length)
    ) {
      void loadBackendDraftPreview(approvedBackendSuggestionIds);
      return;
    }
    setActiveReviewStep(1);
  };

  const goToReviewChoose = () => {
    setIsPreviewContentEditing(false);
    if (
      activeProposal?.backendSuggestions &&
      (activeProposal.tab === "skills" || activeProposal.tab === "experience")
    ) {
      void loadCurrentDraftPreview();
      return;
    }

    if (!activeProposal?.backendSuggestions) {
      setActiveReviewStep(0);
      return;
    }
    if (activeProposal.backendDraftPreview) {
      setActiveReviewStep(1);
      return;
    }
    if (activeProposal.tab !== "experience") {
      setActiveReviewStep(0);
      return;
    }

    void (async () => {
      const suggestionPage = await listEvolutionSuggestions({
        page: 1,
        pageSize: backendSuggestionPageSize,
        ...getPreferenceSuggestionResourceParam(activeProposal.before),
      });

      replaceBackendSuggestionPageInProposal(activeProposal.id, suggestionPage);
      setSelectedBackendSuggestionIds((previous) => {
        const latestIds = new Set(suggestionPage.items.map((item) => item.id));
        return previous.filter((item) => latestIds.has(item));
      });
      setActiveReviewStep(0);
    })();
  };

  const finishCloseChangeReview = () => {
    setIsPreviewContentEditing(false);
    setActiveProposalId(undefined);
    reviewRouteReloadKeyRef.current = "";
    navigateToMemoryList(activeProposal?.tab || activeTab);
  };
  const closeChangeReview = () => {
    if (
      (activeProposal?.tab === "skills" || activeProposal?.tab === "experience") &&
      activeProposal.backendSuggestions &&
      activeReviewStep === 1
    ) {
      finishCloseChangeReview();
      return;
    }

    if (
      activeProposal?.backendSuggestions &&
      activeReviewStep === 1 &&
      backendDraftPreview
    ) {
      Modal.confirm({
        title: t("admin.memoryDiffClosePreviewConfirmTitle"),
        content: t("admin.memoryDiffClosePreviewConfirmContent"),
        okText: t("admin.memoryDiffClosePreviewConfirmOk"),
        cancelText: t("common.cancel"),
        onOk: async () => {
          try {
            await discardManagedPreferenceDraft(getActiveManagedDraftKind());
          } catch (error) {
            console.error("Discard managed draft on close failed:", error);
          } finally {
            finishCloseChangeReview();
          }
        },
      });
      return;
    }

    if (activeReviewStep !== 1) {
      finishCloseChangeReview();
      return;
    }

    Modal.confirm({
      title: t("admin.memoryDiffClosePreviewConfirmTitle"),
      content: t("admin.memoryDiffClosePreviewConfirmContent"),
      okText: t("admin.memoryDiffClosePreviewConfirmOk"),
      cancelText: t("common.cancel"),
      onOk: finishCloseChangeReview,
    });
  };

  const startPreviewContentEdit = () => {
    if (!activeProposal || !effectiveProposalMerged || !activeProposalMerged) {
      return;
    }

    const currentContent =
      activeProposal.tab === "skills"
        ? (manualMergedDraft as StructuredAsset | null)?.content ??
          (activeProposalMerged as StructuredAsset).content
        : (manualMergedDraft as ExperienceAsset | null)?.content ??
          (activeProposalMerged as ExperienceAsset).content;

    setManualPreviewContentDraft(currentContent);
    setIsPreviewContentEditing(true);
  };

  const savePreviewContentEdit = () => {
    if (!activeProposal || !effectiveProposalMerged) {
      return;
    }

    if (activeProposal.tab === "skills") {
      const nextMerged = cloneStructuredAsset(effectiveProposalMerged as StructuredAsset);
      nextMerged.content = manualPreviewContentDraft;
      setManualMergedDraft(nextMerged);
    } else {
      const nextMerged = cloneExperienceAsset(effectiveProposalMerged as ExperienceAsset);
      nextMerged.content = manualPreviewContentDraft;
      setManualMergedDraft(nextMerged);
    }

    setIsPreviewContentEditing(false);
    message.success(t("admin.memoryDiffManualSaveSuccess"));
  };

  const approveChangeProposal = async () => {
    if (!activeProposal || !effectiveProposalMerged) {
      return;
    }

    if (activeProposal.backendSuggestionId) {
      const suggestionId = activeProposal.backendSuggestionId;
      const isSuggestionAlreadyReviewed = reviewedBackendSuggestionIds.includes(suggestionId);
      setReviewSuggestionSubmitting(true);
      try {
        if (hasEffectiveChange) {
          if (!isSuggestionAlreadyReviewed) {
            await approveEvolutionSuggestion(suggestionId);
            markBackendSuggestionReviewed(suggestionId);
          }
          if (activeProposal.tab === "experience") {
            const mergedExperience = effectiveProposalMerged as ExperienceAsset;
            await upsertPreferenceAsset({
              title: mergedExperience.title,
              content: mergedExperience.content,
              protect: Boolean(mergedExperience.protect),
              autoEvo: Boolean(mergedExperience.autoEvo),
              agentPersona: mergedExperience.agentPersona,
              responseStyle: mergedExperience.responseStyle,
              resourceType: mergedExperience.resourceType,
              preferredName: mergedExperience.preferredName,
            });
          }
          message.success(t("admin.memoryDiffApproveSuccess"));
        } else {
          if (!isSuggestionAlreadyReviewed) {
            await rejectEvolutionSuggestion(suggestionId);
            markBackendSuggestionReviewed(suggestionId);
          }
          message.success(t("admin.memoryDiffKeepOriginalSuccess"));
        }

        setChangeProposals((previous) =>
          previous.filter((item) => item.id !== activeProposal.id),
        );
        setActiveProposalId(undefined);
        navigateToMemoryList(activeProposal.tab);
        if (activeProposal.tab === "experience") {
          await refreshExperienceAssets({ silent: true });
        } else {
          await refreshSkillAssets();
        }
      } catch (error) {
        console.error("Submit evolution suggestion failed:", error);
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryExperienceSaveFailed")) ||
            t("admin.memoryExperienceSaveFailed"),
        );
      } finally {
        setReviewSuggestionSubmitting(false);
      }
      return;
    }

    if (!hasEffectiveChange) {
      setChangeProposals((previous) =>
        previous.filter((item) => item.id !== activeProposal.id),
      );
      setActiveProposalId(undefined);
      navigateToMemoryList(activeProposal.tab);
      message.success(t("admin.memoryDiffKeepOriginalSuccess"));
      return;
    }

    if (activeProposal.tab === "skills") {
      const itemExists = skillAssets.some((item) => item.id === activeProposal.targetId);
      if (!itemExists) {
        setChangeProposals((previous) =>
          previous.filter((item) => item.id !== activeProposal.id),
        );
        setActiveProposalId(undefined);
        navigateToMemoryList("skills");
        message.warning(t("admin.memoryDiffTargetMissing"));
        return;
      }

      setSkillAssets((previous) =>
        previous.map((item) =>
          item.id === activeProposal.targetId
            ? cloneStructuredAsset(effectiveProposalMerged as StructuredAsset)
            : item,
        ),
      );
    } else {
      const itemExists = experienceAssets.some((item) => item.id === activeProposal.targetId);
      if (!itemExists) {
        setChangeProposals((previous) =>
          previous.filter((item) => item.id !== activeProposal.id),
        );
        setActiveProposalId(undefined);
        navigateToMemoryList("experience");
        message.warning(t("admin.memoryDiffTargetMissing"));
        return;
      }

      setExperienceAssets((previous) =>
        previous.map((item) =>
          item.id === activeProposal.targetId
            ? cloneExperienceAsset(effectiveProposalMerged as ExperienceAsset)
            : item,
        ),
      );
    }

    setChangeProposals((previous) =>
      previous.filter((item) => item.id !== activeProposal.id),
    );
    setActiveProposalId(undefined);
    navigateToMemoryList(activeProposal.tab);
    message.success(t("admin.memoryDiffApproveSuccess"));
  };

  const clearGlossaryProposalsByAssetIds = useCallback(
    (assetIds: string[]) => {
      if (!assetIds.length) {
        return;
      }
      const removedIdSet = new Set(assetIds);
      const relatedProposalIds = glossaryChangeProposals
        .filter(
          (proposal) =>
            removedIdSet.has(proposal.targetId) ||
            (proposal.before ? removedIdSet.has(proposal.before.id) : false) ||
            Boolean(
              proposal.mergeFrom?.some((mergeItem) => removedIdSet.has(mergeItem.id)),
            ),
        )
        .map((proposal) => proposal.id);

      if (!relatedProposalIds.length) {
        return;
      }

      const relatedProposalSet = new Set(relatedProposalIds);
      setGlossaryChangeProposals((previous) =>
        previous.filter((proposal) => !relatedProposalSet.has(proposal.id)),
      );
    },
    [glossaryChangeProposals],
  );

  const handleDelete = (item: StructuredAsset | ExperienceAsset | GlossaryAsset) => {
    if (activeTab === "experience") {
      return;
    }

    const itemName = "title" in item ? item.title : "term" in item ? item.term : item.name;

    Modal.confirm({
      title: t("common.delete"),
      content: t("admin.memoryDeleteConfirm", { name: itemName }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        if (activeTab === "skills") {
          try {
            await removeSkillAsset(item.id);
            await refreshSkillAssets();
            message.success(t("admin.memorySkillDeleteSuccess"));
          } catch (error) {
            console.error("Delete skill asset failed:", error);
            message.error(
              getLocalizedErrorMessage(error, t("admin.memorySkillDeleteFailed")) ||
                t("admin.memorySkillDeleteFailed"),
            );
          }
          return;
        }

        if (activeTab === "glossary") {
          const removedIds = [item.id];
          const removedIdSet = new Set(removedIds);
          try {
            await removeGlossaryAsset(item.id);
            await refreshGlossaryAssets({
              keyword: query,
              page: glossaryListPage,
              pageSize: glossaryListPageSize,
              source: glossarySource,
              silent: true,
            });
            setSelectedGlossaryAssetIds((previous) =>
              previous.filter((id) => !removedIdSet.has(id)),
            );
            setGlossaryDetailTarget((previous) =>
              previous && removedIdSet.has(previous.id) ? null : previous,
            );
            clearGlossaryProposalsByAssetIds(removedIds);
          } catch (error) {
            console.error("Delete glossary asset failed:", error);
            message.error(
              getLocalizedErrorMessage(error, t("admin.memoryGlossaryDeleteFailed")) ||
                t("admin.memoryGlossaryDeleteFailed"),
            );
            return;
          }

          message.success(t("admin.memoryGlossaryDeleteSuccess"));
          return;
        }

        message.success(t("admin.memoryDeleteSuccess"));
      },
    });
  };

  const handleEnableBuiltinSkill = useCallback(
    async (item: StructuredAsset) => {
      const builtinSkillUid = item.builtinSkillUid?.trim();
      if (!builtinSkillUid) {
        message.warning(t("admin.memoryBuiltinSkillMissing"));
        return;
      }

      setBuiltinSkillEnableLoading((previous) => new Set(previous).add(builtinSkillUid));
      try {
        await enableBuiltinSkill(builtinSkillUid);
        setSkillListPage(1);
        await refreshSkillAssets({ page: 1 });
        message.success(t("admin.memoryBuiltinSkillEnableSuccess"));
      } catch (error) {
        console.error("Enable builtin skill failed:", error);
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryBuiltinSkillEnableFailed")) ||
            t("admin.memoryBuiltinSkillEnableFailed"),
        );
      } finally {
        setBuiltinSkillEnableLoading((previous) => {
          const next = new Set(previous);
          next.delete(builtinSkillUid);
          return next;
        });
      }
    },
    [refreshSkillAssets, t],
  );

  const handleBatchDeleteGlossary = () => {
    if (!selectedGlossaryAssets.length) {
      message.info(t("admin.memoryGlossaryBatchSelectFirst"));
      return;
    }

    Modal.confirm({
      title: t("common.delete"),
      content: t("admin.memoryGlossaryBatchDeleteConfirm", {
        count: selectedGlossaryAssets.length,
      }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        const removedIds = selectedGlossaryAssets.map((item) => item.id);
        const removedIdSet = new Set(removedIds);

        try {
          await batchRemoveGlossaryAssets(removedIds);
          await refreshGlossaryAssets({
            keyword: query,
            page: glossaryListPage,
            pageSize: glossaryListPageSize,
            source: glossarySource,
            silent: true,
          });
          setSelectedGlossaryAssetIds([]);
          setGlossaryDetailTarget((previous) =>
            previous && removedIdSet.has(previous.id) ? null : previous,
          );
          clearGlossaryProposalsByAssetIds(removedIds);

          message.success(t("admin.memoryGlossaryBatchDeleteSuccess"));
        } catch (error) {
          console.error("Batch delete glossary assets failed:", error);
          message.error(
            getLocalizedErrorMessage(error, t("admin.memoryGlossaryBatchDeleteFailed")) ||
              t("admin.memoryGlossaryBatchDeleteFailed"),
          );
        }
      },
    });
  };
  const handleBatchMergeGlossary = () => {
    if (!selectedGlossaryAssets.length) {
      message.info(t("admin.memoryGlossaryBatchSelectFirst"));
      return;
    }
    if (selectedGlossaryAssets.length < 2) {
      message.info(t("admin.memoryGlossaryBatchMergeSelectAtLeastTwo"));
      return;
    }

    const [target, ...mergeSources] = selectedGlossaryAssets;
    Modal.confirm({
      title: t("admin.memoryGlossaryBatchMergeConfirmTitle"),
      content: t("admin.memoryGlossaryBatchMergeConfirmContent", {
        target: target.term,
        count: mergeSources.length,
      }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      onOk: () => {
        const mergedAliases = normalizeTextValues([
          ...target.aliases,
          ...mergeSources.map((item) => item.term),
          ...mergeSources.flatMap((item) => item.aliases),
        ]).filter((alias) => alias !== target.term.trim());
        const mergedGroup = (
          [target.group, ...mergeSources.map((item) => item.group)]
            .map((item) => item.trim())
            .find(Boolean) || ""
        ).trim();
        const mergedContent = normalizeTextValues([
          target.content,
          ...mergeSources.map((item) => item.content),
        ]).join("\n\n");

        openModal("edit", {
          ...cloneGlossaryAsset(target),
          group: mergedGroup,
          aliases: mergedAliases,
          content: mergedContent,
        });
        setPendingGlossaryMergeSourceIds(mergeSources.map((item) => item.id));
      },
    });
  };

  const saveDraft = async () => {
    let saveSuccessMessageKey = "admin.memorySaveSuccess";

    if (activeTab === "glossary") {
      const normalizedTerm = draft.term.trim();
      const normalizedAliases = normalizeTextValues(draft.aliases);
      const normalizedContent = draft.content.trim();

      if (!normalizedTerm || !normalizedContent) {
        message.warning(
          `${t("common.pleaseInput")}${
            !normalizedTerm
              ? t("admin.memoryGlossaryTerm")
              : t("admin.memoryContent")
          }`,
        );
        return;
      }

      if (normalizedTerm.length > GLOSSARY_TERM_MAX_LENGTH) {
        message.warning(
          t("admin.memoryGlossaryTermMaxLength", {
            count: GLOSSARY_TERM_MAX_LENGTH,
          }),
        );
        return;
      }

      if (
        normalizedAliases.some((item) => item.length > GLOSSARY_ALIAS_MAX_LENGTH)
      ) {
        message.warning(
          t("admin.memoryGlossaryAliasMaxLength", {
            count: GLOSSARY_ALIAS_MAX_LENGTH,
          }),
        );
        return;
      }

      if (normalizedContent.length > GLOSSARY_CONTENT_MAX_LENGTH) {
        message.warning(
          t("admin.memoryGlossaryContentMaxLength", {
            count: GLOSSARY_CONTENT_MAX_LENGTH,
          }),
        );
        return;
      }

      if (normalizedAliases.includes(normalizedTerm)) {
        message.warning(
          t("admin.memoryGlossaryTermAliasExactDuplicate", {
            word: normalizedTerm,
          }),
        );
        return;
      }

      const payload: GlossaryAsset = {
        id: draft.id || createId("glossary"),
        term: normalizedTerm,
        group: draft.group.trim(),
        aliases: normalizedAliases,
        source: draft.source,
        content: normalizedContent,
        protect: draft.protect,
      };
      const mergeSourceIdSet = new Set(pendingGlossaryMergeSourceIds);
      const hasPendingMerge = mergeSourceIdSet.size > 0;

      setGlossarySaving(true);
      let mergeApplied = false;

      try {
        let savedGlossary: GlossaryAsset | null = null;
        const shouldCheckExistingWords = !hasPendingMerge;

        if (shouldCheckExistingWords) {
          const existingWords = await checkGlossaryWordsExist(
            payload.term,
            payload.aliases,
          );
          if (existingWords.existing.length) {
            message.warning(
              t("admin.memoryGlossaryWordsAlreadyExist", {
                words: existingWords.existing.join("、"),
              }),
            );
          }
        }

        if (hasPendingMerge) {
          const merged = await mergeGlossaryAssets({
            group_ids: [payload.id, ...pendingGlossaryMergeSourceIds],
            term: payload.term,
            aliases: payload.aliases,
            description: payload.content,
          });
          mergeApplied = true;
          savedGlossary = await updateGlossaryAsset({
            ...payload,
            id: merged?.id || payload.id,
            source: merged?.source || payload.source,
            group: merged?.group || payload.group,
          });
          clearGlossaryProposalsByAssetIds([
            payload.id,
            ...pendingGlossaryMergeSourceIds,
          ]);
          setSelectedGlossaryAssetIds([]);
          setGlossaryDetailTarget((previous) =>
            previous && mergeSourceIdSet.has(previous.id) ? null : previous,
          );
          saveSuccessMessageKey = "admin.memoryGlossaryBatchMergeSuccess";
        } else if (modalMode === "edit") {
          savedGlossary = await updateGlossaryAsset(payload);
          setGlossaryChangeProposals((previous) =>
            previous.filter((proposal) => proposal.targetId !== payload.id),
          );
        } else {
          savedGlossary = await createGlossaryAsset(payload);
        }

        await refreshGlossaryAssets({
          keyword: query,
          page: glossaryListPage,
          pageSize: glossaryListPageSize,
          source: glossarySource,
          silent: true,
        });
        if (savedGlossary) {
          setGlossaryDetailTarget((previous) =>
            previous && previous.id === savedGlossary.id
              ? cloneGlossaryAsset(savedGlossary)
              : previous,
          );
        }
        setModalOpen(false);
        setPendingGlossaryMergeSourceIds([]);
        message.success(t(saveSuccessMessageKey));
      } catch (error) {
        console.error("Save glossary asset failed:", error);
        if (mergeApplied) {
          setPendingGlossaryMergeSourceIds([]);
          setSelectedGlossaryAssetIds([]);
          await refreshGlossaryAssets({
            keyword: query,
            page: glossaryListPage,
            pageSize: glossaryListPageSize,
            source: glossarySource,
            silent: true,
          });
        }
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryGlossarySaveFailed")) ||
            t("admin.memoryGlossarySaveFailed"),
        );
      } finally {
        setGlossarySaving(false);
      }

      return;
    } else if (activeTab === "experience") {
      if (!draft.title.trim()) {
        message.warning(`${t("common.pleaseInput")}${t("admin.memoryTitle")}`);
        return;
      }

      setExperienceSaving(true);

      try {
        const currentExperienceItem =
          modalMode === "edit" && draft.id
            ? experienceAssets.find((item) => item.id === draft.id)
            : undefined;

        await upsertPreferenceAsset({
          title: draft.title.trim(),
          content: draft.content.trim(),
          protect: draft.protect,
          autoEvo: currentExperienceItem?.autoEvo,
          agentPersona: draft.agentPersona,
          responseStyle: draft.responseStyle,
          resourceType: currentExperienceItem?.resourceType,
          preferredName: draft.preferredName,
        });
        if (modalMode === "edit" && draft.id) {
          setChangeProposals((previous) =>
            previous.filter(
              (item) => !(item.tab === "experience" && item.targetId === draft.id),
            ),
          );
        }

        await refreshExperienceAssets({ silent: true });
        setModalOpen(false);
        message.success(t(saveSuccessMessageKey));
      } catch (error) {
        console.error("Save preference asset failed:", error);
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryExperienceSaveFailed")) ||
            t("admin.memoryExperienceSaveFailed"),
        );
      } finally {
        setExperienceSaving(false);
      }

      return;
    } else {
      const isChildSkill = activeTab === "skills" && Boolean(draft.parentId);
      if (!draft.name.trim()) {
        message.warning(`${t("common.pleaseInput")}${t("admin.memoryName")}`);
        return;
      }
      if (!draft.description.trim()) {
        message.warning(`${t("common.pleaseInput")}${t("admin.memoryDescription")}`);
        return;
      }
      if (!draft.content.trim()) {
        message.warning(`${t("common.pleaseInput")}${t("admin.memoryMarkdown")}`);
        return;
      }

      const normalizedSkillTags = normalizeTagValues(draft.tags);
      if (activeTab === "skills" && normalizedSkillTags.length > SKILL_TAG_MAX_COUNT) {
        message.warning(
          t("admin.memorySkillTagMaxCount", {
            count: SKILL_TAG_MAX_COUNT,
          }),
        );
        return;
      }

      const payload: StructuredAsset = {
        id: draft.id || createId("skill"),
        name: draft.name.trim(),
        description: draft.description.trim(),
        category: isChildSkill ? "" : draft.category.trim(),
        tags: normalizedSkillTags,
        parentId: activeTab === "skills" ? draft.parentId || undefined : undefined,
        content: draft.content.trim(),
        protect: draft.protect,
      };

      if (activeTab === "skills") {
        const parentSkill = payload.parentId
          ? parentSkillCandidateAssets.find((item) => item.id === payload.parentId)
          : undefined;
        if (payload.parentId && payload.parentId === payload.id) {
          message.warning(t("admin.memoryParentSkillSelf"));
          return;
        }

        if (parentSkill?.parentId) {
          message.warning(t("admin.memoryParentSkillSecondLevelOnly"));
          return;
        }

        const hasChildren = parentSkillCandidateAssets.some(
          (item) => item.parentId === payload.id,
        );
        if (payload.parentId && hasChildren) {
          message.warning(t("admin.memoryParentSkillHasChildren"));
          return;
        }

        if (payload.parentId && parentSkill) {
          payload.protect = Boolean(parentSkill.protect);
        }

        try {
          if (modalMode === "edit") {
            if (!payload.id) {
              message.warning(t("admin.memoryDiffTargetMissing"));
              return;
            }

            const existingSkill = skillAssets.find((item) => item.id === payload.id);
            const patchFileExt = payload.parentId
              ? payload.fileExt || existingSkill?.fileExt || inferSkillFileExt(undefined, payload.content)
              : "md";
            const patchCategory = payload.parentId
              ? existingSkill?.category || payload.category
              : payload.category;
            const patchParentSkillName = payload.parentId
              ? parentSkill?.name || existingSkill?.parentSkillName || ""
              : "";
            const patchSource: StructuredAsset = {
              ...(existingSkill || payload),
              ...payload,
              category: patchCategory,
              parentSkillName: patchParentSkillName,
              autoEvo: existingSkill?.autoEvo ?? payload.autoEvo,
              isEnabled: existingSkill?.isEnabled ?? payload.isEnabled ?? true,
              fileExt: patchFileExt,
            };
            const patchPayload = buildSkillPatchPayload(patchSource, {
              name: payload.name,
              description: payload.description,
              tags: payload.tags,
              content: payload.content,
              category: patchCategory,
              parent_skill_id: payload.parentId || "",
              parent_skill_name: patchParentSkillName,
              file_ext: patchFileExt,
              is_locked: Boolean(payload.protect),
            });

            await patchSkillAsset(payload.id, patchPayload);
            setChangeProposals((previous) =>
              previous.filter(
                (item) => !(item.tab === "skills" && item.targetId === payload.id),
              ),
            );
          } else if (payload.parentId) {
            if (!parentSkill) {
              message.warning(t("admin.memoryDiffTargetMissing"));
              return;
            }

            await createSkillAsset({
              name: payload.name,
              description: payload.description,
              category: parentSkill.category || draft.category.trim(),
              tags: payload.tags,
              parent_skill_name: parentSkill.name,
              content: payload.content,
              file_ext: payload.fileExt || inferSkillFileExt(undefined, payload.content),
              is_locked: Boolean(payload.protect),
              is_enabled: true,
            });
          } else {
            const canCreateChildSkills = draft.childSkills.length > 0;
            let childPayloads: Array<Record<string, unknown>> = [];

            if (canCreateChildSkills) {
              const hasInvalidChild = draft.childSkills.some(
                (child) =>
                  !child.name.trim() ||
                  !child.description.trim() ||
                  !child.content.trim(),
              );
              if (hasInvalidChild) {
                message.warning(t("admin.memoryChildSkillRequired"));
                return;
              }

              childPayloads = draft.childSkills.map((child) => ({
                name: child.name.trim(),
                description: child.description.trim(),
                content: child.content.trim(),
                file_ext: inferSkillFileExt(undefined, child.content),
                is_locked: Boolean(payload.protect),
              }));
            }

            await createSkillAsset({
              name: payload.name,
              description: payload.description,
              category: payload.category,
              tags: payload.tags,
              content: payload.content,
              file_ext: "md",
              is_locked: Boolean(payload.protect),
              is_enabled: true,
              children: childPayloads,
            });
          }

          await refreshSkillAssets();
        } catch (error) {
          console.error("Save skill draft failed:", error);
          return;
        }

        setModalOpen(false);
        message.success(t(saveSuccessMessageKey));
        return;
      }
    }

    setModalOpen(false);
    message.success(t(saveSuccessMessageKey));
  };

  const handleConfirmShare = async () => {
    if (!shareTarget) {
      return;
    }

    if (!shareDraft.groupIds.length && !shareDraft.userIds.length) {
      message.warning(t("admin.memoryShareRequireRecipient"));
      return;
    }

    if (shareTarget.tab === "skills") {
      try {
        await shareSkillAsset(shareTarget.item.id, {
          targetUserIds: shareDraft.userIds,
          targetGroupIds: shareDraft.groupIds,
          message: shareDraft.message || t("admin.memoryShareSkillHint"),
        });
      } catch (error) {
        console.error("Share skill failed:", error);
        return;
      }
    }

    message.success(t("admin.memoryShareSuccess"));
    if (shareTarget.tab === "skills") {
      void refreshSkillShareCenter({ silent: true });
    }
    closeShareModal();
  };

  useEffect(() => {
    if (!shareModalOpen) {
      return;
    }

    const fetchShareOptions = async () => {
      setShareLoading(true);

      try {
        const [userResponse, groupResponse] = await Promise.all([
          createUserApi().listUsersApiAuthserviceUserGet({
            page: 1,
            pageSize: 200,
            activeOnly: true,
          }),
          createGroupApi().listGroupsApiAuthserviceGroupGet({
            page: 1,
            pageSize: 200,
          }),
        ]);

        const userPayload = (userResponse.data as any)?.data || userResponse.data || {};
        const groupPayload = (groupResponse.data as any)?.data || groupResponse.data || {};

        setShareUsers(Array.isArray(userPayload.users) ? userPayload.users : []);
        setShareGroups(Array.isArray(groupPayload.groups) ? groupPayload.groups : []);
      } catch (error) {
        console.error("Fetch share targets failed:", error);
        message.error(t("admin.memoryShareLoadFailed"));
      } finally {
        setShareLoading(false);
      }
    };

    fetchShareOptions();
  }, [shareModalOpen, t]);

  useEffect(() => {
    if (!shareModalOpen || !shareTarget || shareTarget.tab !== "skills") {
      setShareStatusError("");
      setShareStatusRecords([]);
      setShareStatusLoading(false);
      return;
    }

    void refreshShareStatus(shareTarget.item.id, { showErrorToast: false });
  }, [shareModalOpen, shareTarget, refreshShareStatus]);

  useEffect(() => {
    const sharedTab = routeListTab || parseMemoryTab(searchParams.get("tab"));
    const sharedItemId = searchParams.get("item");

    if (!sharedTab || !sharedItemId) {
      handledShareKeyRef.current = "";
      return;
    }

    if (sharedTab !== "skills" && sharedTab !== "experience") {
      return;
    }
    if (sharedTab === "skills" && !skillsInitialized) {
      return;
    }

    const shareKey = `${sharedTab}:${sharedItemId}`;
    if (handledShareKeyRef.current === shareKey) {
      return;
    }

    const matchedItem = shareableItems[sharedTab].find((item) => item.id === sharedItemId);
    if (!matchedItem) {
      message.warning(t("admin.memoryShareTargetMissing"));
      handledShareKeyRef.current = shareKey;
      return;
    }

    handledShareKeyRef.current = shareKey;
    setActiveTab(sharedTab);
    openModal("view", matchedItem);
  }, [routeListTab, searchParams, shareableItems, skillsInitialized, t]);
  const glossarySourceLabelMap: Record<GlossarySource, string> = {
    user: t("admin.memoryGlossarySourceUser"),
    ai: t("admin.memoryGlossarySourceAI"),
  };
  const glossarySourceColorMap: Record<GlossarySource, string> = {
    user: "blue",
    ai: "purple",
  };
  const openGlossaryDetail = (item: GlossaryAsset) => {
    setGlossaryDetailTarget(cloneGlossaryAsset(item));
    navigateToGlossaryDetail(item.id);
  };
  const closeGlossaryDetail = () => {
    setGlossaryDetailTarget(null);
    navigateToMemoryList("glossary");
  };
  const applyGlossaryProposals = async (
    proposals: GlossaryChangeProposal[],
    resolutions: Record<string, GlossaryConflictResolution> = {},
  ) => {
    if (!proposals.length) {
      message.info(t("admin.memoryGlossaryInboxSelectFirst"));
      return;
    }

    setGlossaryInboxSubmitting("accept");
    try {
      const backendProposals = proposals.filter((proposal) => proposal.backendConflictId);
      if (backendProposals.length) {
        await Promise.all(
          backendProposals.map((proposal) => {
            const conflictId = proposal.backendConflictId || proposal.id;
            const conflictWord = proposal.backendConflictWord || proposal.after.term;
            const resolution = resolutions[proposal.id];
            const mode =
              resolution?.mode || (proposal.backendConflictGroupIds?.length ? "separate" : "create");
            const selectedGroupIds = resolution?.mergeGroupIds?.length
              ? resolution.mergeGroupIds
              : resolution?.selectedGroupIds?.length
              ? resolution.selectedGroupIds
              : proposal.backendConflictGroupIds || [];

            if (mode === "merge") {
              if (selectedGroupIds.length < 2) {
                throw new Error(t("admin.memoryGlossaryInboxMergeSelectAtLeastTwo"));
              }

              const targetGroups = proposal.backendConflictGroups || [];
              const mergeGroupsFromResolution =
                resolution?.mergeGroups?.filter((item) => item.length >= 2) || [];
              const mergeGroups = mergeGroupsFromResolution.length
                ? mergeGroupsFromResolution
                : [selectedGroupIds];
              const fallbackMergedTerm =
                targetGroups.find((group) => mergeGroups[0]?.includes(group.id))?.term ||
                proposal.after.term;
              const fallbackMergedAliases = Array.from(
                new Set(
                  targetGroups
                    .filter((group) => selectedGroupIds.includes(group.id))
                    .flatMap((group) => [group.term, ...group.aliases]),
                ),
              );
              const fallbackMergedContent = targetGroups
                .filter((group) => selectedGroupIds.includes(group.id))
                .map((group) => group.content)
                .filter(Boolean)
                .join("\n\n");
              const mergePayloads = mergeGroups.map((groupIds) => {
                const draft = resolution?.mergeDrafts?.find(
                  (item) =>
                    item.groupIds.length === groupIds.length &&
                    item.groupIds.every((id) => groupIds.includes(id)),
                );
                const term = (
                  draft?.term ||
                  resolution?.mergedGroupTerm ||
                  fallbackMergedTerm
                ).trim();
                const aliasesSource = draft?.aliases?.length
                  ? draft.aliases
                  : resolution?.mergedGroupAliases?.length
                    ? resolution.mergedGroupAliases
                    : fallbackMergedAliases;
                const description = (
                  draft?.content ??
                  resolution?.mergedGroupContent ??
                  fallbackMergedContent ??
                  proposal.after.content
                ).trim();
                return {
                  group_ids: groupIds,
                  term,
                  aliases: Array.from(
                    new Set(
                      aliasesSource
                        .map((item) => item.trim())
                        .filter(Boolean),
                    ),
                  ),
                  description,
                };
              });
              const firstMergedGroupIds = mergeGroups
                .map((groupIds) => groupIds[0])
                .filter(Boolean);
              if (!firstMergedGroupIds.length) {
                throw new Error(t("admin.memoryGlossaryInboxSelectTargetFirst"));
              }
              const writeGroupIds = resolution?.writeGroupIds || [];
              const shouldWriteToMergedGroup =
                !writeGroupIds.length ||
                writeGroupIds.some((groupId) => groupId.startsWith(MERGED_GLOSSARY_GROUP_OPTION_ID));
              const extraWriteGroupIds = writeGroupIds.filter(
                (groupId) =>
                  !groupId.startsWith(MERGED_GLOSSARY_GROUP_OPTION_ID_PREFIX) &&
                  groupId !== MERGED_GLOSSARY_GROUP_OPTION_ID &&
                  !selectedGroupIds.includes(groupId),
              );
              const targetGroupIds = Array.from(
                new Set([
                  ...(shouldWriteToMergedGroup ? firstMergedGroupIds : []),
                  ...extraWriteGroupIds,
                ]),
              );
              if (!targetGroupIds.length) {
                throw new Error(t("admin.memoryGlossaryInboxSelectTargetFirst"));
              }

              return mergeGlossaryConflictAndAddWord({
                id: conflictId,
                word: conflictWord,
                merges: mergePayloads,
                group_ids: targetGroupIds,
              });
            }

            if (mode === "separate") {
              if (!selectedGroupIds.length) {
                throw new Error(t("admin.memoryGlossaryInboxSelectTargetFirst"));
              }

              return addGlossaryConflictToGroups({
                id: conflictId,
                word: conflictWord,
                groupIds: selectedGroupIds,
              });
            }

            const newGroupTerm = (resolution?.newGroupTerm || "").trim();
            if (!newGroupTerm) {
              throw new Error(t("admin.memoryGlossaryInboxNewGroupRequired"));
            }
            const normalizedNewAliases = (resolution?.newGroupAliases?.length
              ? resolution.newGroupAliases
              : proposal.after.aliases
            )
              .map((item) => item.trim())
              .filter(Boolean);
            if (normalizedNewAliases.some((alias) => alias === newGroupTerm)) {
              throw new Error(t("admin.memoryGlossaryGroupAliasDuplicate"));
            }
            const newGroupContent = (resolution?.newGroupContent ?? proposal.after.content).trim();
            if (newGroupTerm && newGroupContent && newGroupTerm === newGroupContent) {
              throw new Error(t("admin.memoryGlossaryContentSameAsTerm"));
            }

            const writeGroupIds = resolution?.writeGroupIds || [];
            const shouldWriteConflictWordToNewGroup =
              !writeGroupIds.length ||
              writeGroupIds.includes(NEW_GLOSSARY_GROUP_OPTION_ID);
            const aliases = [
              ...(shouldWriteConflictWordToNewGroup ? [conflictWord] : []),
              ...(resolution?.newGroupAliases?.length
                ? resolution.newGroupAliases
                : proposal.after.aliases),
            ]
              .map((item) => item.trim())
              .filter((item) => Boolean(item) && item !== newGroupTerm);
            const extraWriteGroupIds = writeGroupIds.filter(
              (groupId) => groupId !== NEW_GLOSSARY_GROUP_OPTION_ID,
            );

            return createGlossaryGroupFromConflict({
              id: conflictId,
              word: conflictWord,
              term: newGroupTerm,
              aliases: [...new Set(aliases)],
              description: newGroupContent,
              group_ids: extraWriteGroupIds.length ? extraWriteGroupIds : undefined,
            });
          }),
        );
        await Promise.all([
          refreshGlossaryAssets({
            keyword: query,
            page: glossaryListPage,
            pageSize: glossaryListPageSize,
            source: glossarySource,
            silent: true,
          }),
          refreshGlossaryConflicts({ silent: true }),
        ]);
        message.success(t("admin.memoryGlossaryInboxAcceptSuccess"));
        return;
      }

      setGlossaryAssets((previous) => {
        const next = [...previous];
        proposals.forEach((proposal) => {
          const mergeSourceIds = proposal.mergeFrom?.map((item) => item.id) ?? [];
          if (mergeSourceIds.length) {
            for (let index = next.length - 1; index >= 0; index -= 1) {
              if (mergeSourceIds.includes(next[index].id)) {
                next.splice(index, 1);
              }
            }
          }

          const existingIndex = next.findIndex(
            (item) =>
              item.id === proposal.targetId ||
              (proposal.before ? item.id === proposal.before.id : false),
          );
          if (existingIndex >= 0) {
            next[existingIndex] = cloneGlossaryAsset(proposal.after);
            return;
          }
          next.unshift(cloneGlossaryAsset(proposal.after));
        });
        return next;
      });

      setGlossaryChangeProposals((previous) =>
        previous.filter(
          (proposal) => !proposals.some((selected) => selected.id === proposal.id),
        ),
      );
      message.success(t("admin.memoryGlossaryInboxAcceptSuccess"));
    } catch (error) {
      console.error("Accept glossary conflicts failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryGlossaryInboxAcceptFailed")) ||
          t("admin.memoryGlossaryInboxAcceptFailed"),
      );
    } finally {
      setGlossaryInboxSubmitting("");
    }
  };
  const rejectGlossaryProposals = async (proposals: GlossaryChangeProposal[]) => {
    if (!proposals.length) {
      message.info(t("admin.memoryGlossaryInboxSelectFirst"));
      return;
    }

    setGlossaryInboxSubmitting("reject");
    try {
      const backendConflictIds = proposals
        .map((proposal) => proposal.backendConflictId)
        .filter((item): item is string => Boolean(item));
      if (backendConflictIds.length) {
        await Promise.all(backendConflictIds.map((id) => removeGlossaryConflict(id)));
        await refreshGlossaryConflicts({ silent: true });
        message.success(t("admin.memoryGlossaryInboxRejectSuccess"));
        return;
      }

      setGlossaryChangeProposals((previous) =>
        previous.filter(
          (proposal) => !proposals.some((selected) => selected.id === proposal.id),
        ),
      );
      message.success(t("admin.memoryGlossaryInboxRejectSuccess"));
    } catch (error) {
      console.error("Reject glossary conflicts failed:", error);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memoryGlossaryInboxRejectFailed")) ||
          t("admin.memoryGlossaryInboxRejectFailed"),
      );
    } finally {
      setGlossaryInboxSubmitting("");
    }
  };
  const structuredInfoColumns: ColumnsType<StructuredAsset> = [
    {
      title: t("admin.memoryNameDesc"),
      dataIndex: "name",
      key: "name",
      width: 380,
      render: (_value, record) => {
        const pendingProposal =
          activeTab === "skills" ? getPendingProposal("skills", record.id) : undefined;
        const hasReviewableDraft =
          activeTab === "skills" && hasSkillDraftPreviewStatus(record);
        const showPendingTag =
          !record.autoEvo && (Boolean(pendingProposal) || hasReviewableDraft);
        const autoEvoStatusMeta =
          activeTab === "skills" && record.autoEvo
            ? getAutoEvoStatusMeta(record.autoEvoApplyStatus)
            : null;

        return (
          <div
            className={`memory-table-main ${
              record.isBuiltinTemplate ? "is-builtin-template" : ""
            }`}
          >
            <div className="memory-table-main-title">
              {activeTab === "skills" && !record.isBuiltinTemplate ? (
                <button
                  type="button"
                  className="memory-term-link"
                  onClick={() => navigateToSkillDetail(record.id)}
                >
                  {record.name}
                </button>
              ) : activeTab === "skills" ? (
                <span className="memory-term-link is-disabled">{record.name}</span>
              ) : (
                <span>{record.name}</span>
              )}
              {activeTab === "skills" && record.isBuiltinTemplate ? (
                <Tag color="default">{t("admin.memoryBuiltinSkillTemplateTag")}</Tag>
              ) : record.originBuiltinSkillUid ? (
                <Tag color="blue">{t("admin.memoryBuiltinSkillEnabledTag")}</Tag>
              ) : null}
              {autoEvoStatusMeta ? (
                <Tag color={autoEvoStatusMeta.color}>{t(autoEvoStatusMeta.textKey)}</Tag>
              ) : null}
              {showPendingTag ? (
                <Tag color="orange">{t("admin.memoryDiffPendingTag")}</Tag>
              ) : null}
              {activeTab === "skills" && record.hasPendingRemoveSuggestion ? (
                <Tag color="red">{t("admin.memorySkillPendingRemoveTag")}</Tag>
              ) : null}
              {record.protect ? (
                <Tag className="memory-protect-tag" bordered={false}>
                  <LockOutlined />
                  <span>{t("admin.memoryProtect", { defaultValue: "保护" })}</span>
                </Tag>
              ) : null}
            </div>
            {!record.parentId && record.description ? (
              <Tooltip
                title={
                  <div className="memory-text-popover-content">{record.description}</div>
                }
                overlayClassName="memory-text-popover"
                placement="topLeft"
                trigger="hover"
              >
                <div className="memory-table-main-desc">{record.description}</div>
              </Tooltip>
            ) : !record.parentId ? (
              <div className="memory-table-main-desc">{record.description}</div>
            ) : null}
          </div>
        );
      },
    },
    {
      title: t("admin.memoryCategory"),
      dataIndex: "category",
      key: "category",
      width: 180,
      render: (value: string, record) =>
        !record.parentId && value ? (
          <Tag className="memory-category-tag" bordered={false}>
            {value}
          </Tag>
        ) : (
          "-"
        ),
    },
    {
      title: t("admin.memoryTagSet"),
      dataIndex: "tags",
      key: "tags",
      width: 260,
      render: (tags: string[], record) =>
        !record.parentId && tags.length ? (
          <div className="memory-tag-group">
            {tags.map((item) => (
              <Tag key={item}>{item}</Tag>
            ))}
          </div>
        ) : (
          "-"
        ),
    },
  ];

  const genericColumns: ColumnsType<StructuredAsset> = [
    ...structuredInfoColumns,
    {
      title: t("admin.memoryAutoUpdate"),
      key: "autoEvo",
      width: 90,
      render: (_value, record) => {
        if (activeTab === "skills" && record.parentId) {
          return "-";
        }
        if (activeTab === "skills" && record.isBuiltinTemplate) {
          return "-";
        }

        const disabledByRemoveSuggestion =
          activeTab === "skills" && Boolean(record.hasPendingRemoveSuggestion);
        const switchNode = (
          <Switch
            checked={Boolean(record.autoEvo) && !disabledByRemoveSuggestion}
            disabled={disabledByRemoveSuggestion}
            loading={skillAutoEvoLoading.has(record.id)}
            onChange={(checked) => {
              if (checked && record.hasPendingRemoveSuggestion) {
                message.warning(t("admin.memorySkillAutoEvoDisabledByRemove"));
                void refreshSkillAssets({ preserveChangeProposals: true });
                return;
              }
              void (async () => {
                setSkillAutoEvoLoading((prev) => new Set(prev).add(record.id));
                try {
                  await patchSkillAsset(record.id, buildSkillPatchPayload(record, { auto_evo: checked }));
                  await refreshSkillAssets({ preserveChangeProposals: true });
                } catch (error) {
                  console.error("Toggle auto_evo failed:", error);
                  await refreshSkillAssets({ preserveChangeProposals: true });
                  message.error(
                    getLocalizedErrorMessage(error, t("admin.memoryAutoEvoToggleFailed")) ||
                      t("admin.memoryAutoEvoToggleFailed"),
                  );
                } finally {
                  setSkillAutoEvoLoading((prev) => {
                    const next = new Set(prev);
                    next.delete(record.id);
                    return next;
                  });
                }
              })();
            }}
          />
        );
        return disabledByRemoveSuggestion ? (
          <Tooltip title={t("admin.memorySkillAutoEvoDisabledByRemove")}>{switchNode}</Tooltip>
        ) : (
          switchNode
        );
      },
    },
    {
      title: t("admin.memoryOperations"),
      key: "actions",
      width: 250,
      fixed: "right",
      render: (_value, record) => {
        const isChildSkill = activeTab === "skills" && Boolean(record.parentId);
        const isBuiltinTemplate = activeTab === "skills" && Boolean(record.isBuiltinTemplate);
        const pendingProposal =
          activeTab === "skills" ? getPendingProposal("skills", record.id) : undefined;
        const hasReviewableDraft =
          activeTab === "skills" && hasSkillDraftPreviewStatus(record);
        const canReviewChange = Boolean(pendingProposal) || hasReviewableDraft;
        const reviewTooltip = pendingProposal
          ? t("admin.memoryDiffReviewAction")
          : hasReviewableDraft
            ? t("admin.memorySkillUpdateReviewAction")
            : t("admin.memoryDiffNoPending");

        return (
          <Space size={4}>
            {isBuiltinTemplate && !record.parentId ? (
              <Button
                type="primary"
                size="small"
                loading={
                  record.builtinSkillUid
                    ? builtinSkillEnableLoading.has(record.builtinSkillUid)
                    : false
                }
                onClick={() => void handleEnableBuiltinSkill(record)}
              >
                {t("admin.memoryBuiltinSkillEnable")}
              </Button>
            ) : null}
            {activeTab !== "skills" ? (
              <Tooltip title={t("admin.memoryViewItem")}>
                <Button
                  type="text"
                  icon={<EyeOutlined />}
                  onClick={() => openModal("view", record)}
                />
              </Tooltip>
            ) : null}
            {!isBuiltinTemplate ? (
              <>
                {!isChildSkill ? (
                  <Tooltip title={reviewTooltip}>
                    <Button
                      type="text"
                      icon={<HistoryOutlined />}
                      loading={reviewSuggestionLoadingId === record.id}
                      disabled={!canReviewChange}
                      onClick={() =>
                        void openChangeReview("skills", record.id, record.updateStatus)
                      }
                    />
                  </Tooltip>
                ) : null}
                <Tooltip title={t("admin.memoryEditItem")}>
                  <Button
                    type="text"
                    icon={<EditOutlined />}
                    onClick={() => openModal("edit", record)}
                  />
                </Tooltip>
                {!record.parentId ? (
                  <Tooltip title={t("admin.memoryShareItem")}>
                    <Button
                      type="text"
                      icon={<LinkOutlined />}
                      onClick={() => openShareModal("skills", record)}
                    />
                  </Tooltip>
                ) : null}
                <Tooltip title={t("admin.memoryDeleteItem")}>
                  <Button
                    type="text"
                    danger
                    icon={<DeleteOutlined />}
                    onClick={() => handleDelete(record)}
                  />
                </Tooltip>
              </>
            ) : null}
          </Space>
        );
      },
    },
  ];

  const experienceProfileFields = useMemo<ExperienceProfileFieldConfig[]>(
    () => [
      {
        key: "agentPersona",
        label: t("admin.memoryProfileAgentPersona", { defaultValue: "角色" }),
        description: t("admin.memoryProfileAgentPersonaDesc", {
          defaultValue: "描述智能体在回复时应保持的身份、职责和边界。",
        }),
        placeholder: t("admin.memoryProfileAgentPersonaPlaceholder", {
          defaultValue: "例如：专业、审慎、主动澄清上下文的智能体",
        }),
      },
      {
        key: "preferredName",
        label: t("admin.memoryProfilePreferredName", { defaultValue: "用户称谓" }),
        description: t("admin.memoryProfilePreferredNameDesc", {
          defaultValue: "设置回复中对用户的称呼方式。",
        }),
        placeholder: t("admin.memoryProfilePreferredNamePlaceholder", {
          defaultValue: "例如：称呼用户为“您”，或使用指定昵称",
        }),
      },
      {
        key: "responseStyle",
        label: t("admin.memoryProfileResponseStyle", { defaultValue: "回复风格" }),
        description: t("admin.memoryProfileResponseStyleDesc", {
          defaultValue: "定义默认表达习惯、篇幅和结构偏好。",
        }),
        placeholder: t("admin.memoryProfileResponseStylePlaceholder", {
          defaultValue: "例如：简洁、结构化，先结论后解释",
        }),
      },
    ],
    [t],
  );

  const activeExperienceProfileRecord = useMemo(
    () =>
      experienceProfileEditTarget
        ? experienceAssets.find((item) => item.id === experienceProfileEditTarget.recordId) ||
          null
        : null,
    [experienceAssets, experienceProfileEditTarget],
  );

  const activeExperienceProfileField = useMemo(
    () =>
      experienceProfileEditTarget
        ? experienceProfileFields.find(
            (field) => field.key === experienceProfileEditTarget.fieldKey,
          ) || null
        : null,
    [experienceProfileEditTarget, experienceProfileFields],
  );

  const renderExperienceProfileEditor = useCallback(
    (record: ExperienceAsset): ReactNode => {
      const draft = experienceProfileDrafts[record.id] || getExperienceProfileDraft(record);
      const isSaving = experienceProfileSaving.has(record.id);
      const emptyText = t("admin.memoryProfileEmpty", { defaultValue: "未配置" });

      return (
        <div className="memory-profile-editor">
          <div className="memory-profile-editor-head">
            <div>
              <strong>
                {t("admin.memoryProfileEditorTitle", { defaultValue: "用户画像配置" })}
              </strong>
              <span>
                {t("admin.memoryProfileEditorDesc", {
                  defaultValue: "作为用户画像的二级信息参与对话偏好，不影响主内容结构。",
                })}
              </span>
            </div>
            <Tag bordered={false}>
              {t("admin.memoryProfileEditorTag", { defaultValue: "二级结构" })}
            </Tag>
          </div>
          <div className="memory-profile-field-grid">
            {experienceProfileFields.map((field) => (
              <div className="memory-profile-field" key={field.key}>
                <div className="memory-profile-field-copy">
                  <span className="memory-profile-field-label">{field.label}</span>
                  <span className="memory-profile-field-desc">{field.description}</span>
                </div>
                <div className="memory-profile-field-value">
                  <span className={draft[field.key] ? "" : "is-empty"}>
                    {draft[field.key] || emptyText}
                  </span>
                </div>
                <Button
                  disabled={isSaving}
                  icon={<EditOutlined />}
                  size="small"
                  onClick={() =>
                    setExperienceProfileEditTarget({
                      recordId: record.id,
                      fieldKey: field.key,
                    })
                  }
                >
                  {t("common.edit")}
                </Button>
              </div>
            ))}
          </div>
        </div>
      );
    },
    [
      experienceProfileFields,
      experienceProfileDrafts,
      experienceProfileSaving,
      t,
    ],
  );

  const experienceProfileExpandable = useMemo(
    () => ({
      expandedRowClassName: () => "memory-profile-expanded-row",
      expandedRowKeys: expandedExperienceProfileIds,
      expandedRowRender: renderExperienceProfileEditor,
      rowExpandable: isExperienceProfileAsset,
      onExpandedRowsChange: (keys: readonly unknown[]) =>
        setExpandedExperienceProfileIds(keys.map(String)),
    }),
    [expandedExperienceProfileIds, renderExperienceProfileEditor],
  );

  const experienceColumns: ColumnsType<ExperienceAsset> = [
    {
      title: t("admin.memoryTitleCol"),
      dataIndex: "title",
      key: "title",
      width: 320,
      render: (_value, record) => {
        const showPendingTag = hasDraftPreviewStatus(record);
        const autoEvoStatusMeta = record.autoEvo
          ? getAutoEvoStatusMeta(record.autoEvoApplyStatus)
          : null;

        return (
          <div className="memory-table-main">
            <div className="memory-table-main-title">
              <button
                type="button"
                className="memory-term-link"
                onClick={() => navigateToExperienceDetail(record.id)}
              >
                {record.title}
              </button>
              {autoEvoStatusMeta ? (
                <Tag color={autoEvoStatusMeta.color}>{t(autoEvoStatusMeta.textKey)}</Tag>
              ) : null}
              {showPendingTag ? (
                <Tag color="orange">{t("admin.memoryDiffPendingTag")}</Tag>
              ) : null}
              {record.protect ? (
                <Tag className="memory-protect-tag" bordered={false}>
                  <LockOutlined />
                  <span>{t("admin.memoryProtect", { defaultValue: "保护" })}</span>
                </Tag>
              ) : null}
            </div>
          </div>
        );
      },
    },
    {
      title: t("admin.memoryContentSummary"),
      dataIndex: "content",
      key: "content",
      width: 520,
      className: "memory-content-summary-column",
      render: (value: string) =>
        value ? (
          <Tooltip
            title={<div className="memory-text-popover-content">{value}</div>}
            overlayClassName="memory-text-popover"
            placement="topLeft"
            trigger="hover"
          >
            <div className="memory-content-preview memory-content-preview-single-line">
              {value}
            </div>
          </Tooltip>
        ) : (
          <div className="memory-content-preview memory-content-preview-single-line">
            {value}
          </div>
        ),
    },
    {
      title: t("admin.memoryAutoUpdate"),
      key: "autoEvo",
      width: 90,
      render: (_value, record) => (
        <Switch
          checked={Boolean(record.autoEvo)}
          loading={experienceAutoEvoLoading.has(record.id)}
          onChange={(checked) => {
            void (async () => {
              setExperienceAutoEvoLoading((prev) => new Set(prev).add(record.id));
              try {
                await upsertPreferenceAsset({
                  title: record.title,
                  content: record.content,
                  protect: Boolean(record.protect),
                  autoEvo: checked,
                  agentPersona: record.agentPersona,
                  responseStyle: record.responseStyle,
                  resourceType: record.resourceType,
                  preferredName: record.preferredName,
                });
                await refreshExperienceSection({ silent: true });
              } catch (error) {
                console.error("Toggle auto_evo failed:", error);
                await refreshExperienceSection({ silent: true });
                message.error(
                  getLocalizedErrorMessage(error, t("admin.memoryAutoEvoToggleFailed")) ||
                    t("admin.memoryAutoEvoToggleFailed"),
                );
              } finally {
                setExperienceAutoEvoLoading((prev) => {
                  const next = new Set(prev);
                  next.delete(record.id);
                  return next;
                });
              }
            })();
          }}
        />
      ),
    },
    {
      title: t("admin.memoryOperations"),
      key: "actions",
      width: 210,
      render: (_value, record) => {
        const canReviewChange = isPendingReviewStatus(record.reviewStatus);
        const reviewTooltip = canReviewChange
          ? t("admin.memoryDiffReviewAction")
          : t("admin.memoryDiffNoPending");

        return (
          <Space size={4}>
            <Tooltip title={reviewTooltip}>
              <Button
                type="text"
                icon={<HistoryOutlined />}
                loading={reviewSuggestionLoadingId === record.id}
                disabled={!canReviewChange}
                onClick={() => void openChangeReview("experience", record.id)}
              />
            </Tooltip>
            <Tooltip title={t("admin.memoryEditItem")}>
              <Button
                type="text"
                icon={<EditOutlined />}
                onClick={() => openModal("edit", record)}
              />
            </Tooltip>
          </Space>
        );
      },
    },
  ];
  const glossaryColumns: ColumnsType<GlossaryAsset> = [
    {
      title: t("admin.memoryGlossaryTerm"),
      dataIndex: "term",
      key: "term",
      width: 380,
      render: (_value, record) => (
        <div className="memory-table-main">
          <div className="memory-table-main-title">
            <button
              type="button"
              className="memory-term-link"
              onClick={() => openGlossaryDetail(record)}
            >
              {record.term}
            </button>
            {record.protect ? (
              <Tag className="memory-protect-tag" bordered={false}>
                <LockOutlined />
                <span>{t("admin.memoryProtect", { defaultValue: "保护" })}</span>
              </Tag>
            ) : null}
          </div>
          <div className="memory-tag-group memory-tag-group-scroll">
            {record.aliases.length ? (
              record.aliases.map((alias) => <Tag key={alias}>{alias}</Tag>)
            ) : (
              <span className="memory-content-preview">-</span>
            )}
          </div>
        </div>
      ),
    },
    {
      title: t("admin.memoryGlossarySource"),
      dataIndex: "source",
      key: "source",
      width: 150,
      render: (source: GlossarySource) => (
        <Tag color={glossarySourceColorMap[source]}>
          {glossarySourceLabelMap[source]}
        </Tag>
      ),
    },
    {
      title: t("admin.memoryContentSummary"),
      dataIndex: "content",
      key: "content",
      width: 420,
      render: (value: string) => (
        <div className="memory-content-preview memory-content-preview-glossary">
          {value}
        </div>
      ),
    },
    {
      title: t("admin.memoryOperations"),
      key: "actions",
      width: 170,
      render: (_value, record) => (
        <Space size={4}>
          <Tooltip title={t("admin.memoryViewItem")}>
            <Button
              type="text"
              icon={<EyeOutlined />}
              onClick={() => openGlossaryDetail(record)}
            />
          </Tooltip>
          <Tooltip title={t("admin.memoryEditItem")}>
            <Button
              type="text"
              icon={<EditOutlined />}
              onClick={() => openModal("edit", record)}
            />
          </Tooltip>
          <Tooltip title={t("admin.memoryDeleteItem")}>
            <Button
              type="text"
              danger
              icon={<DeleteOutlined />}
              onClick={() => handleDelete(record)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  const modalTitle = `${t(
    modalMode === "add"
      ? "admin.memoryModalCreate"
      : modalMode === "edit"
        ? "admin.memoryModalEdit"
        : "admin.memoryModalView",
  )}${currentTabMeta.unit}`;
  const isReadOnly = modalMode === "view";
  const isChildSkillDraft = activeTab === "skills" && Boolean(draft.parentId);
  const tagOptions = [...new Set([...availableTags, ...draft.tags])].map((item) => ({
    label: item,
    value: item,
  }));
  const isGlossaryRouteRequested = Boolean(glossaryRouteItemId);
  const isReviewMode = Boolean(activeProposal && (activeProposalDiff || isBackendSuggestionReviewMode));
  const glossaryDetailExists = useMemo(
    () =>
      glossaryDetailTarget
        ? glossaryAssets.some((item) => item.id === glossaryDetailTarget.id)
        : false,
    [glossaryAssets, glossaryDetailTarget],
  );
  const getSkillShareStatusMeta = (status: SkillShareStatus) => {
    if (status === "accepted") {
      return { color: "success", text: t("admin.memorySkillShareStatusAccepted") };
    }
    if (status === "rejected") {
      return { color: "error", text: t("admin.memorySkillShareStatusRejected") };
    }
    if (status === "failed") {
      return { color: "warning", text: t("admin.memorySkillShareStatusFailed") };
    }
    if (status === "unknown") {
      return { color: "default", text: t("admin.memorySkillShareStatusUnknown") };
    }
    return { color: "processing", text: t("admin.memorySkillShareStatusPending") };
  };
  const activeExperienceProfileDraft = activeExperienceProfileRecord
    ? experienceProfileDrafts[activeExperienceProfileRecord.id] ||
      getExperienceProfileDraft(activeExperienceProfileRecord)
    : null;
  const activeExperienceProfileOriginal = activeExperienceProfileRecord
    ? getExperienceProfileDraft(activeExperienceProfileRecord)
    : null;
  const activeExperienceProfileSaving = activeExperienceProfileRecord
    ? experienceProfileSaving.has(activeExperienceProfileRecord.id)
    : false;
  const activeExperienceProfileValue =
    activeExperienceProfileDraft && activeExperienceProfileField
      ? activeExperienceProfileDraft[activeExperienceProfileField.key]
      : "";
  const activeExperienceProfileHasChanges =
    Boolean(activeExperienceProfileDraft && activeExperienceProfileOriginal) &&
    experienceProfileFields.some(
      (field) =>
        activeExperienceProfileDraft?.[field.key] !==
        activeExperienceProfileOriginal?.[field.key],
    );

  const outletContext = {
    t,
    activeTab,
    setActiveTab,
    currentTabMeta,
    tabMeta,
    memoryTabOrder,
    openSkillShareCenter,
    incomingPendingCount,
    glossaryChangeProposals,
    glossaryAssets,
    glossaryLoading,
    glossaryListPage,
    glossaryListPageSize,
    glossaryListTotal,
    glossaryLoadError,
    refreshGlossaryAssets,
    glossaryRouteItemId,
    skillRouteItemId,
    experienceRouteItemId,
    glossaryDetailTarget,
    glossaryDetailExists,
    closeGlossaryDetail,
    openModal,
    glossarySourceColorMap,
    glossarySourceLabelMap,
    resetFilters,
    navigateToMemoryList,
    navigateToSkillDetail,
    navigateToExperienceDetail,
    setGlossaryDetailTarget,
    setGlossaryInboxOpen,
    experienceFeatureEnabled,
    experienceSettingSaving,
    handleExperienceFeatureToggle,
    refreshSkillAssets,
    refreshExperienceSection,
    searchInput,
    setSearchInput,
    query,
    setQuery,
    category,
    setCategory,
    tag,
    setTag,
    glossarySource,
    setGlossarySource,
    availableGlossarySourceOptions,
    availableCategories,
    availableTags,
    skillCategoriesLoading,
    skillTagsLoading,
    selectedGlossaryAssets,
    handleBatchMergeGlossary,
    handleBatchDeleteGlossary,
    filteredExperienceItems,
    experienceAssets,
    experienceLoading,
    experienceInitialized,
    experienceColumns,
    experienceProfileExpandable,
    filteredGlossaryItems,
    glossaryColumns,
    selectedGlossaryAssetIds,
    setGlossaryListPage,
    setGlossaryListPageSize,
    setSelectedGlossaryAssetIds,
    skillLoading,
    skillsInitialized,
    skillListPage,
    skillListPageSize,
    skillListTotal,
    setSkillListPage,
    setSkillListPageSize,
    skillAssets,
    filteredSkillTree,
    filteredStructuredItems,
    genericColumns,
    isReviewRouteRequested,
    isGlossaryRouteRequested,
    reviewRouteTab,
    reviewRouteItemId,
    activeProposal,
    isBackendSuggestionReviewMode,
    activeReviewStep,
    goToReviewChoose,
    goToReviewPreview,
    closeChangeReview,
    backendDraftSubmitting,
    discardBackendDraftAndReturn,
    backendDraftLoading,
    approvedBackendSuggestionIds,
    isAnyBackendSuggestionMutating,
    confirmBackendDraft,
    allBackendSuggestionsSelected,
    hasPartialBackendSuggestionSelection,
    setAllBackendSuggestionsSelected,
    backendRejectedSuggestionCount,
    activeBackendSuggestions,
    activeBackendSuggestionSourceText,
    selectedBackendSuggestionCount,
    backendSuggestionBatchSubmitting,
    handleBackendBatchAccept,
    handleBackendBatchRejectWithConfirm,
    backendSuggestionHasMore,
    backendSuggestionLoadingMore,
    backendSuggestionLoadMoreError,
    loadMoreBackendSuggestions,
    clearSelectedBackendSuggestions,
    backendSuggestionSubmitting,
    selectedBackendSuggestionIds,
    isBackendSuggestionSelectable,
    setBackendSuggestionSelected,
    submitBackendSuggestionDecision,
    backendDraftDiffLines,
    backendDraftReady: Boolean(backendDraftPreview),
    qaQuestionDraft,
    setQaQuestionDraft,
    handleReviewQuestionKeyDown,
    sendReviewQuestion,
    activeProposalDiff,
    reviewSuggestionSubmitting,
    approveChangeProposal,
    hasEffectiveChange,
    allSelectableFieldsSelected,
    hasPartialFieldSelection,
    setAllFieldsSelected,
    acceptedFieldCount,
    rejectedFieldCount,
    pendingFieldCount,
    handleBatchAcceptAndGoPreview,
    handleBatchRejectWithConfirm,
    clearSelectedFields,
    activeProposalFieldChanges,
    proposalFieldDecisions,
    getFieldDecisionActionKey,
    fieldDecisionSubmitting,
    selectedFieldKeys,
    setFieldSelected,
    submitFieldDecision,
    normalizeSuggestionValue,
    isPreviewContentEditing,
    startPreviewContentEdit,
    savePreviewContentEdit,
    manualPreviewContentDraft,
    setManualPreviewContentDraft,
  };

  return (
    <div className={`admin-page memory-page ${isReviewMode ? "is-review-mode" : ""}`}>
      <Outlet context={outletContext} />

      {showGlossaryInboxUi ? (
        <GlossaryInboxModal
          t={t}
          glossaryInboxOpen={glossaryInboxOpen}
          setGlossaryInboxOpen={setGlossaryInboxOpen}
          glossaryChangeProposals={glossaryChangeProposals}
          glossaryInboxLoading={glossaryInboxLoading}
          glossaryInboxError={glossaryInboxError}
          glossaryInboxSubmitting={glossaryInboxSubmitting}
          refreshGlossaryConflicts={refreshGlossaryConflicts}
          glossarySourceColorMap={glossarySourceColorMap}
          glossarySourceLabelMap={glossarySourceLabelMap}
          rejectGlossaryProposals={rejectGlossaryProposals}
          applyGlossaryProposals={applyGlossaryProposals}
        />
      ) : null}

      <MemoryDraftModal
        t={t}
        modalOpen={modalOpen}
        modalTitle={modalTitle}
        closeModal={closeModal}
        saveDraft={saveDraft}
        activeTab={activeTab}
        experienceSaving={experienceSaving}
        glossarySaving={glossarySaving}
        isReadOnly={isReadOnly}
        draft={draft}
        setDraft={setDraft}
        pendingGlossaryMergeSourceIds={pendingGlossaryMergeSourceIds}
        modalMode={modalMode}
        isChildSkillDraft={isChildSkillDraft}
        parentSkillOptions={parentSkillOptions}
        parentSkillOptionsLoading={parentSkillOptionsLoading}
        tagOptions={tagOptions}
        normalizeTagValues={normalizeTagValues}
        createSkillUploadProps={createSkillUploadProps}
        addChildSkillDraft={addChildSkillDraft}
        removeChildSkillDraft={removeChildSkillDraft}
        updateChildSkillDraft={updateChildSkillDraft}
      />

      <SkillShareCenterModal
        t={t}
        skillShareCenterOpen={skillShareCenterOpen}
        closeSkillShareCenter={closeSkillShareCenter}
        skillShareCenterTab={skillShareCenterTab}
        setSkillShareCenterTab={setSkillShareCenterTab}
        incomingPendingCount={incomingPendingCount}
        outgoingSkillShares={outgoingSkillShares}
        skillShareCenterLoading={skillShareCenterLoading}
        refreshSkillShareCenter={refreshSkillShareCenter}
        skillShareCenterError={skillShareCenterError}
        currentSkillShareList={currentSkillShareList}
        skillShareActionState={skillShareActionState}
        getSkillShareStatusMeta={getSkillShareStatusMeta}
        formatDateTime={formatDateTime}
        previewSkillShare={previewSkillShare}
        rejectIncomingSkillShare={rejectIncomingSkillShare}
        acceptIncomingSkillShare={acceptIncomingSkillShare}
        isSkillShareActionable={isSkillShareActionable}
      />

      <ShareModal
        t={t}
        shareModalOpen={shareModalOpen}
        closeShareModal={closeShareModal}
        handleConfirmShare={handleConfirmShare}
        shareTarget={shareTarget}
        shareDraft={shareDraft}
        setShareDraft={setShareDraft}
        shareLoading={shareLoading}
        shareGroups={shareGroups}
        shareUsers={shareUsers}
        shareStatusLoading={shareStatusLoading}
        shareStatusError={shareStatusError}
        shareStatusRecords={shareStatusRecords}
        getSkillShareStatusMeta={getSkillShareStatusMeta}
        formatDateTime={formatDateTime}
      />

      <Modal
        cancelText={t("common.cancel")}
        destroyOnHidden
        okButtonProps={{
          disabled: !activeExperienceProfileHasChanges,
          loading: activeExperienceProfileSaving,
        }}
        okText={t("common.save")}
        open={Boolean(activeExperienceProfileRecord && activeExperienceProfileField)}
        title={
          activeExperienceProfileField
            ? t("admin.memoryProfileEditTitle", {
                defaultValue: "编辑{{field}}",
                field: activeExperienceProfileField.label,
              })
            : t("admin.memoryProfileEditorTitle", { defaultValue: "用户画像配置" })
        }
        width={560}
        onCancel={() => {
          if (activeExperienceProfileRecord) {
            resetExperienceProfileDraft(activeExperienceProfileRecord);
          }
          setExperienceProfileEditTarget(null);
        }}
        onOk={async () => {
          if (!activeExperienceProfileRecord) {
            return;
          }
          const saved = await saveExperienceProfileDraft(activeExperienceProfileRecord);
          if (saved) {
            setExperienceProfileEditTarget(null);
          }
        }}
      >
        {activeExperienceProfileRecord && activeExperienceProfileField ? (
          <label className="memory-profile-edit-modal">
            <span className="memory-profile-edit-modal-label">
              {activeExperienceProfileField.label}
            </span>
            <span className="memory-profile-edit-modal-desc">
              {activeExperienceProfileField.description}
            </span>
            <Input.TextArea
              autoFocus
              autoSize={{ minRows: 5, maxRows: 8 }}
              disabled={activeExperienceProfileSaving}
              maxLength={USER_PROFILE_FIELD_MAX_LENGTH}
              placeholder={activeExperienceProfileField.placeholder}
              showCount
              value={activeExperienceProfileValue}
              onChange={(event) =>
                updateExperienceProfileDraft(
                  activeExperienceProfileRecord,
                  activeExperienceProfileField.key,
                  event.target.value,
                )
              }
            />
          </label>
        ) : null}
      </Modal>

    </div>
  );
}
