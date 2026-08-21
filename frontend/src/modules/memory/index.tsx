import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent, ReactNode } from "react";
import {
  Button,
  Input,
  Modal,
  Space,
  Switch,
  Tag,
  Tooltip,
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
import {
  getLocalizedErrorMessage,
  localizeErrorCode,
} from "@/components/request";
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
import { runtimeFeatures } from "@/runtime/features";
import GlossaryInboxModal from "./components/GlossaryInboxModal";
import { MemoryManagementContext } from "./context";
import MemoryDraftModal, {
  type SkillCreateSource,
} from "./components/MemoryDraftModal";
import ShareModal from "./components/ShareModal";
import SkillShareCenterModal from "./components/SkillShareCenterModal";
import { renderSkillCategoryIcon } from "./components/SkillManagementSection/skillCategoryIcon";
import {
  acceptSkillShare,
  buildSkillUpdatePayload,
  confirmSkillDraft,
  createSkillAsset,
  discardSkillDraft,
  enableBuiltinSkill,
  generateSkillDraft,
  getSkillAssetDetail,
  getSkillReviewSummary,
  listIncomingSkillShares,
  listOutgoingSkillShares,
  listSkillReviewTasks,
  listSkillShareTargets,
  listSkillAssetsPage,
  listSkillCategories,
  listSkillTags,
  patchSkillAsset,
  previewSkillDraft,
  rejectSkillShare,
  removeSkillAsset,
  shareSkillAsset,
  runSkillReview,
  type SkillAssetRecord,
  type SkillReviewResultRecord,
  type SkillReviewSummaryRecord,
  type SkillShareRecord,
  type SkillShareStatus,
  type CreateSkillPayload,
  type SkillDraftPreviewRecord,
} from "./skillApi";
import { buildSkillZipBlob } from "./skillPackage";
import { uploadSkillTempFile } from "./skillUpload";
import {
  approveEvolutionSuggestion,
  batchApproveEvolutionSuggestions,
  batchRejectEvolutionSuggestions,
  rejectEvolutionSuggestion,
  type EvolutionSuggestionRecord,
} from "./evolutionApi";
import MemoryManagementListPage from "./pages/list";
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
  canUploadSkillFile,
  cloneGlossaryAsset,
  cloneStructuredAsset,
  createDraft,
  createId,
  createStructuredDraft,
  formatDateTime,
  getBaseName,
  getSkillBodyContentForDisplay,
  initialChangeProposals,
  initialSkills,
  isMarkdownSkillFile,
  isSkillShareActionable,
  isSkillUpdatePending,
  memoryTabOrder,
  normalizeSuggestionValue,
  normalizeTagValues,
  normalizeTextValues,
  parseChangeProposalTab,
  parseMarkdownFrontMatter,
  parseMemoryTab,
  resolveSkillSourceType,
  serializeStructuredAsset,
  SKILL_TAG_MAX_COUNT,
} from "./shared";
import "./index.scss";

const backendSuggestionPageSize = 20;
const defaultSkillListPageSize = 6;
const defaultGlossaryListPageSize = 4;
const showGlossaryInboxUi = true;
const MERGED_GLOSSARY_GROUP_OPTION_ID = "__merged_glossary_group__";
const MERGED_GLOSSARY_GROUP_OPTION_ID_PREFIX = `${MERGED_GLOSSARY_GROUP_OPTION_ID}:`;
const NEW_GLOSSARY_GROUP_OPTION_ID = "__new_glossary_group__";
const isReviewableSuggestionStatus = (status?: string) => {
  const normalized = String(status || "")
    .trim()
    .toLowerCase();
  return normalized === "pending";
};
const isSkillRemoveSuggestion = (suggestion: EvolutionSuggestionRecord) =>
  String(suggestion.action || "")
    .trim()
    .toLowerCase() === "remove";
const mapSkillAssetRecordToStructuredAsset = (
  item: SkillAssetRecord,
): StructuredAsset => ({
  id: item.id,
  name: item.name,
  description: item.description,
  category: item.category,
  tags: item.tags,
  content: item.content,
  headRevisionId: item.headRevisionId,
  draft: item.draft,
  autoEvo: item.autoEvo,
  isEnabled: item.isEnabled,
});
const hasSkillDraftPreviewStatus = (record: StructuredAsset) =>
  Boolean(record.hasPendingReviewResult) ||
  Boolean(record.hasPendingReviewSuggestions) ||
  isReviewableSuggestionStatus(record.reviewStatus) ||
  isReviewableSuggestionStatus(record.suggestionStatus) ||
  isSkillUpdatePending(record.updateStatus);
const isResourceUpdateTaskRunning = (status?: string) => {
  const normalized = String(status || "")
    .trim()
    .toLowerCase();
  return normalized === "pending" || normalized === "running";
};
const isSkillReviewTaskTerminal = (status?: string) => {
  const normalized = String(status || "")
    .trim()
    .toLowerCase();
  return (
    normalized === "completed" ||
    normalized === "done" ||
    normalized === "failed" ||
    normalized === "skipped"
  );
};
const MANUAL_SKILL_REVIEW_RUNNING_TASK_PAGE_SIZE = 1000;
const waitManualSkillReviewRetry = () =>
  new Promise((resolve) =>
    window.setTimeout(resolve, MANUAL_SKILL_REVIEW_RETRY_DELAY_MS),
  );
const getManualSkillReviewCreatedSkillNames = (
  results: SkillReviewResultRecord[],
) =>
  Array.from(
    new Set(
      results
        .filter((item) => item.type.trim().toLowerCase() === "new")
        .map((item) => item.skillName.trim())
        .filter(Boolean),
    ),
  );
const skillRecordNameMatches = (item: SkillAssetRecord, skillName: string) =>
  item.name.trim().toLowerCase() === skillName.trim().toLowerCase();

interface MemoryManagementProps {
  embeddedTab?: MemoryTab;
}

export default function MemoryManagement({ embeddedTab }: MemoryManagementProps = {}) {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabRouteMatch = useMatch(`${MEMORY_BASE_PATH}/:tab`);
  const skillDetailMatch = useMatch(`${MEMORY_BASE_PATH}/skills/:itemId`);
  const glossaryDetailMatch = useMatch(`${MEMORY_BASE_PATH}/glossary/:itemId`);
  const reviewRouteMatch = useMatch(`${MEMORY_BASE_PATH}/review/:tab/:itemId`);
  const reviewRouteReloadKeyRef = useRef("");
  const skillRouteItemId = skillDetailMatch?.params.itemId;
  const glossaryRouteItemId = glossaryDetailMatch?.params.itemId;
  const reviewRouteTab = parseChangeProposalTab(reviewRouteMatch?.params.tab);
  const reviewRouteItemId = reviewRouteMatch?.params.itemId;
  const isReviewRouteRequested = Boolean(reviewRouteTab && reviewRouteItemId);
  const routeListTab = parseMemoryTab(tabRouteMatch?.params.tab);
  const queryRouteTab = parseMemoryTab(searchParams.get("tab"));
  const routeMemoryTab = (
    embeddedTab ||
    (skillRouteItemId
      ? "skills"
      : glossaryRouteItemId
        ? "glossary"
        : reviewRouteTab || routeListTab || queryRouteTab || "skills")
  ) as MemoryTab;
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
  const [skillAssets, setSkillAssets] =
    useState<StructuredAsset[]>(initialSkills);
  const [pendingSkillPackageFile, setPendingSkillPackageFile] =
    useState<File | null>(null);
  const [pendingSkillSourceUrl, setPendingSkillSourceUrl] = useState("");
  const [skillUrlImportOpen, setSkillUrlImportOpen] = useState(false);
  const [skillUrlImportDraft, setSkillUrlImportDraft] = useState("");
  const [skillLoading, setSkillLoading] = useState(false);
  const [skillCategories, setSkillCategories] = useState<string[]>([]);
  const [skillCategoriesLoaded, setSkillCategoriesLoaded] = useState(false);
  const [skillCategoriesLoading, setSkillCategoriesLoading] = useState(false);
  const [skillTags, setSkillTags] = useState<string[]>([]);
  const [skillTagsLoaded, setSkillTagsLoaded] = useState(false);
  const [skillTagsLoading, setSkillTagsLoading] = useState(false);
  const [skillAutoEvoLoading, setSkillAutoEvoLoading] = useState<Set<string>>(
    new Set(),
  );
  const [skillEnableLoading, setSkillEnableLoading] = useState<Set<string>>(
    new Set(),
  );
  const [builtinSkillEnableLoading, setBuiltinSkillEnableLoading] = useState<
    Set<string>
  >(new Set());
  const [manualSkillReviewSummary, setManualSkillReviewSummary] =
    useState<SkillReviewSummaryRecord | null>(null);
  const [manualSkillReviewLoading, setManualSkillReviewLoading] =
    useState(false);
  const [manualSkillReviewRunning, setManualSkillReviewRunning] =
    useState(false);
  const [manualSkillReviewResults, setManualSkillReviewResults] = useState<
    SkillReviewResultRecord[]
  >([]);
  const [manualSkillReviewResultStatus, setManualSkillReviewResultStatus] =
    useState("");
  const [skillsInitialized, setSkillsInitialized] = useState(false);
  const skillListRequestIdRef = useRef(0);
  const skillZipInputRef = useRef<HTMLInputElement>(null);
  const skillListRouteLocationKeyRef = useRef("");
  const skillListRefreshKeyRef = useRef("");
  const skillListFilterKeyRef = useRef("");
  const manualSkillReviewRequestIdRef = useRef(0);
  const manualSkillReviewPollTimerRef = useRef<number | null>(null);
  const manualSkillReviewPollingKeyRef = useRef("");
  const manualSkillReviewSummaryLoadedRef = useRef(false);
  const glossaryAssetsRefreshKeyRef = useRef("");
  const glossaryAssetsFilterKeyRef = useRef("");
  const glossaryAssetsRouteLocationKeyRef = useRef("");
  const glossaryConflictsRefreshKeyRef = useRef("");
  const [skillListPage, setSkillListPage] = useState(1);
  const [skillListPageSize, setSkillListPageSize] = useState(
    defaultSkillListPageSize,
  );
  const [skillListTotal, setSkillListTotal] = useState(initialSkills.length);
  const [skillView, setSkillView] = useState<
    "installed" | "market" | "workflows" | "trash"
  >(() => {
    const sv = new URLSearchParams(window.location.search).get("skillView");
    if (sv === "workflows" || sv === "market" || sv === "trash") return sv;
    return "installed";
  });
  const [installedSkillSource, setInstalledSkillSource] = useState<
    "all" | "builtin" | "admin" | "personal"
  >("all");
  const [marketSkillSource, setMarketSkillSource] = useState<
    "all" | "builtin" | "admin"
  >("all");
  const [marketCategory, setMarketCategory] = useState("all");
  const [glossaryAssets, setGlossaryAssets] = useState<GlossaryAsset[]>([]);
  const [glossaryLoading, setGlossaryLoading] = useState(false);
  const [glossaryInitialized, setGlossaryInitialized] = useState(false);
  const [glossaryLoadError, setGlossaryLoadError] = useState("");
  const [glossarySaving, setGlossarySaving] = useState(false);
  const [skillSaving, setSkillSaving] = useState(false);
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
  const [selectedGlossaryAssetIds, setSelectedGlossaryAssetIds] = useState<
    string[]
  >([]);
  const [pendingGlossaryMergeSourceIds, setPendingGlossaryMergeSourceIds] =
    useState<string[]>([]);
  const [glossaryDetailTarget, setGlossaryDetailTarget] =
    useState<GlossaryAsset | null>(initialGlossaryDetailTarget);
  const [modalMode, setModalMode] = useState<ModalMode>("view");
  const [draft, setDraft] = useState<AssetDraft>(createDraft());
  const [modalOpen, setModalOpen] = useState(false);
  const [shareModalOpen, setShareModalOpen] = useState(false);
  const hideUserGroupSurfaces = runtimeFeatures.hideUserGroupSurfaces;
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  const [skillShareCenterOpen, setSkillShareCenterOpen] = useState(false);
  const [skillShareCenterTab, setSkillShareCenterTab] =
    useState<SkillShareCenterTab>("incoming");
  const [incomingSkillShares, setIncomingSkillShares] = useState<
    SkillShareRecord[]
  >([]);
  const [outgoingSkillShares, setOutgoingSkillShares] = useState<
    SkillShareRecord[]
  >([]);
  const [skillShareCenterLoading, setSkillShareCenterLoading] = useState(false);
  const [skillShareCenterError, setSkillShareCenterError] = useState("");
  const [skillShareActionState, setSkillShareActionState] = useState<
    Record<string, SkillShareAction | undefined>
  >({});
  const [changeProposals, setChangeProposals] = useState<ChangeProposal[]>(
    initialChangeProposals,
  );
  const [reviewSuggestionSubmitting, setReviewSuggestionSubmitting] =
    useState(false);
  const [fieldDecisionSubmitting, setFieldDecisionSubmitting] = useState<
    Record<string, ProposalFieldDecision | undefined>
  >({});
  const backendSuggestionMutationLockRef = useRef(false);
  const [backendSuggestionSubmitting, setBackendSuggestionSubmitting] =
    useState<Record<string, ProposalFieldDecision | undefined>>({});
  const [
    backendSuggestionBatchSubmitting,
    setBackendSuggestionBatchSubmitting,
  ] = useState<"" | "accept" | "reject">("");
  const [selectedBackendSuggestionIds, setSelectedBackendSuggestionIds] =
    useState<string[]>([]);
  const [reviewedBackendSuggestionIds, setReviewedBackendSuggestionIds] =
    useState<string[]>([]);
  const [approvedBackendSuggestionIds, setApprovedBackendSuggestionIds] =
    useState<string[]>([]);
  const [rejectedBackendSuggestionIds, setRejectedBackendSuggestionIds] =
    useState<string[]>([]);
  const [backendDraftPreview, setBackendDraftPreview] =
    useState<SkillDraftPreviewRecord | null>(null);
  const [backendSkillDiffLines, setBackendSkillDiffLines] = useState<
    import("./shared").DiffLine[]
  >([]);
  const [backendDraftLoading, setBackendDraftLoading] = useState(false);
  const [backendDraftSubmitting, setBackendDraftSubmitting] = useState<
    "confirm" | "discard" | ""
  >("");
  const [glossaryChangeProposals, setGlossaryChangeProposals] = useState<
    GlossaryChangeProposal[]
  >([]);
  const [activeProposalId, setActiveProposalId] = useState<string | undefined>(
    initialReviewProposalId,
  );
  const [activeReviewStep, setActiveReviewStep] = useState<0 | 1>(0);
  const [proposalFieldDecisions, setProposalFieldDecisions] = useState<
    Record<string, ProposalFieldDecision>
  >({});
  const [selectedFieldKeys, setSelectedFieldKeys] = useState<
    ProposalFieldKey[]
  >([]);
  const [manualMergedDraft, setManualMergedDraft] =
    useState<StructuredAsset | null>(null);
  const [isPreviewContentEditing, setIsPreviewContentEditing] = useState(false);
  const [manualPreviewContentDraft, setManualPreviewContentDraft] =
    useState("");
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
  const [shareStatusRecords, setShareStatusRecords] = useState<
    SkillShareRecord[]
  >([]);
  const handledShareKeyRef = useRef("");
  const skillShareRequestIdRef = useRef(0);
  const shareStatusRequestIdRef = useRef(0);
  const glossaryRequestIdRef = useRef(0);
  const glossaryConflictRequestIdRef = useRef(0);
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
  const buildSkillPatchPayload = useCallback(
    (item: StructuredAsset, overrides: Record<string, unknown> = {}) =>
      buildSkillUpdatePayload({
        name: item.name,
        description: item.description,
        category: item.category,
        tags: item.tags,
        autoEvo: item.autoEvo,
        isEnabled: item.isEnabled,
        ...(overrides as Partial<StructuredAsset>),
      }),
    [],
  );

  const localAvailableCategories = [
    ...new Set(currentStructuredItems.map((item) => item.category)),
  ]
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
    activeTab === "skills" && skillTagsLoaded ? skillTags : localAvailableTags;

  const shareableItems = useMemo(() => ({ skills: skillAssets }), [skillAssets]);
  const buildMemoryTabPath = useCallback(
    (tab?: MemoryTab) =>
      tab ? `${MEMORY_BASE_PATH}/${tab}` : MEMORY_BASE_PATH,
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
      if (embeddedTab) {
        setActiveTab(tab || embeddedTab);
        return;
      }
      navigate(
        {
          pathname: buildMemoryTabPath(tab || "skills"),
          search: buildMemorySearch(),
        },
        { replace: options?.replace },
      );
    },
    [buildMemorySearch, buildMemoryTabPath, embeddedTab, navigate],
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
  const navigateToChangeReview = useCallback(
    (
      tab: ChangeProposalTab,
      itemId: string,
      options?: { replace?: boolean },
    ) => {
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
      incomingSkillShares.filter((item) => isSkillShareActionable(item.status)),
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
  const refreshSkillAssets = useCallback(
    async (
      options: {
        page?: number;
        pageSize?: number;
        preserveChangeProposals?: boolean;
      } = {},
    ) => {
      const requestId = skillListRequestIdRef.current + 1;
      skillListRequestIdRef.current = requestId;
      setSkillLoading(true);

      try {
        const requestedPage = options.page ?? skillListPage;
        const requestedPageSize = options.pageSize ?? skillListPageSize;
        const listOptions = {
          keyword: skillKeyword,
          category,
          tags: tag ? [tag] : [],
          pageSize: requestedPageSize,
          excludeBuiltinTemplates: skillView === "installed",
        };

        let result = await listSkillAssetsPage({
          ...listOptions,
          page: requestedPage,
        });
        const maxPage = Math.max(
          1,
          Math.ceil(result.total / Math.max(1, result.pageSize)),
        );
        if (requestedPage > maxPage) {
          result = await listSkillAssetsPage({
            ...listOptions,
            page: maxPage,
          });
        }

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
    },
    [category, skillKeyword, skillListPage, skillListPageSize, skillView, tag],
  );

  const refreshSkillCategories = useCallback(async () => {
    setSkillCategoriesLoading(true);
    try {
      const categories = await listSkillCategories();
      setSkillCategories(categories);
      setSkillCategoriesLoaded(true);
    } catch (error) {
      console.error("Load skill categories failed:", error);
      setSkillCategoriesLoaded(false);
    } finally {
      setSkillCategoriesLoading(false);
    }
  }, []);

  const clearManualSkillReviewPollTimer = useCallback(() => {
    if (manualSkillReviewPollTimerRef.current !== null) {
      window.clearTimeout(manualSkillReviewPollTimerRef.current);
      manualSkillReviewPollTimerRef.current = null;
    }
  }, []);

  const refreshManualSkillReviewSummary = useCallback(
    async (options?: { silent?: boolean }) => {
      const requestId = manualSkillReviewRequestIdRef.current + 1;
      manualSkillReviewRequestIdRef.current = requestId;
      const silent = Boolean(options?.silent);

      if (!silent) {
        setManualSkillReviewLoading(true);
      }

      try {
        const summary = await getSkillReviewSummary();
        if (manualSkillReviewRequestIdRef.current !== requestId) {
          return;
        }
        const runningTask = summary.runningTask;
        setManualSkillReviewSummary(summary);
        setManualSkillReviewRunning(
          Boolean(
            runningTask && isResourceUpdateTaskRunning(runningTask.status),
          ),
        );
      } catch (error) {
        if (manualSkillReviewRequestIdRef.current !== requestId) {
          return;
        }
        console.error("Load manual skill review summary failed:", error);
      } finally {
        if (manualSkillReviewRequestIdRef.current === requestId && !silent) {
          setManualSkillReviewLoading(false);
        }
      }
    },
    [t],
  );

  const pollManualSkillReviewTasks = useCallback(
    (requestId: string) => {
      const normalizedRequestId = requestId.trim();
      if (!normalizedRequestId) {
        return;
      }
      const pollingKey = `manual-skill-review:${normalizedRequestId}`;
      if (manualSkillReviewPollingKeyRef.current === pollingKey) {
        return;
      }

      clearManualSkillReviewPollTimer();
      manualSkillReviewPollingKeyRef.current = pollingKey;
      setManualSkillReviewRunning(true);

      const tick = async () => {
        try {
          const tasks = await listSkillReviewTasks({
            requestId: normalizedRequestId,
            page: 1,
            pageSize: MANUAL_SKILL_REVIEW_RUNNING_TASK_PAGE_SIZE,
          });
          if (manualSkillReviewPollingKeyRef.current !== pollingKey) {
            return;
          }
          const task = tasks.records[0];
          if (task && !isSkillReviewTaskTerminal(task.status)) {
            manualSkillReviewPollTimerRef.current = window.setTimeout(
              tick,
              2000,
            );
            return;
          }

          manualSkillReviewPollingKeyRef.current = "";
          clearManualSkillReviewPollTimer();

          if (task?.status === "failed") {
            message.error(
              localizeErrorCode(
                task.task?.errorCode,
                localizeErrorCode("2000509"),
              ),
            );
            setManualSkillReviewRunning(false);
            await refreshManualSkillReviewSummary({ silent: true });
            return;
          }

          const resultCount = task?.resultCount || 0;
          await Promise.all([
            refreshSkillAssets({ page: 1, preserveChangeProposals: true }),
            refreshManualSkillReviewSummary({ silent: true }),
          ]);
          setManualSkillReviewResults([]);
          setManualSkillReviewResultStatus(
            resultCount > 0 ? "done" : "empty",
          );
          setManualSkillReviewRunning(false);
          if (resultCount > 0) {
            message.success(t("admin.memoryManualSkillReviewDone"));
          } else {
            message.info(t("admin.memoryManualSkillReviewNoResult"));
          }
        } catch (error) {
          if (manualSkillReviewPollingKeyRef.current === pollingKey) {
            manualSkillReviewPollingKeyRef.current = "";
          }
          clearManualSkillReviewPollTimer();
          setManualSkillReviewRunning(false);
          console.error("Poll manual skill review tasks failed:", error);
          await refreshManualSkillReviewSummary({ silent: true });
        }
      };

      void tick();
    },
    [
      clearManualSkillReviewPollTimer,
      refreshManualSkillReviewSummary,
      refreshSkillAssets,
      t,
    ],
  );

  const handleRunManualSkillReview = useCallback(async () => {
    setManualSkillReviewRunning(true);
    setManualSkillReviewResults([]);
    setManualSkillReviewResultStatus("");

    try {
      const result = await runSkillReview();
      setManualSkillReviewSummary(result.summary);
      message.success(t("admin.memoryManualSkillReviewStarted"));
      const requestId = result.requestId || result.summary.runningRequestId;
      if (requestId) {
        pollManualSkillReviewTasks(requestId);
      } else {
        setManualSkillReviewRunning(false);
        await refreshManualSkillReviewSummary({ silent: true });
      }
    } catch (error) {
      if (
        (error as { response?: { status?: number } })?.response?.status === 409
      ) {
        setManualSkillReviewRunning(false);
        await refreshManualSkillReviewSummary({ silent: true });
        return;
      }
      setManualSkillReviewRunning(false);
      console.error("Run manual skill review failed:", error);
      await refreshManualSkillReviewSummary({ silent: true });
    }
  }, [pollManualSkillReviewTasks, refreshManualSkillReviewSummary, t]);

  useEffect(
    () => () => {
      clearManualSkillReviewPollTimer();
    },
    [clearManualSkillReviewPollTimer],
  );

  useEffect(() => {
    if (activeTab !== "skills") {
      return;
    }
    const silent = manualSkillReviewSummaryLoadedRef.current;
    manualSkillReviewSummaryLoadedRef.current = true;
    void refreshManualSkillReviewSummary({ silent });
  }, [activeTab, refreshManualSkillReviewSummary]);

  useEffect(() => {
    if (activeTab !== "skills") {
      return;
    }
    const runningTask = manualSkillReviewSummary?.runningTask;
    const runningRequestId = manualSkillReviewSummary?.runningRequestId || "";
    if (
      !runningTask ||
      !runningRequestId ||
      !isResourceUpdateTaskRunning(runningTask.status)
    ) {
      return;
    }
    pollManualSkillReviewTasks(runningRequestId);
  }, [
    activeTab,
    manualSkillReviewSummary?.runningRequestId,
    manualSkillReviewSummary?.runningTask,
    pollManualSkillReviewTasks,
  ]);

  const handleSkillListPageChange = useCallback(
    (page: number, pageSize: number) => {
      setSkillListPage(page);
      setSkillListPageSize(pageSize);
      skillListRefreshKeyRef.current = [
        location.key,
        location.pathname,
        location.search,
        skillKeyword,
        category || "",
        tag || "",
        skillView,
        installedSkillSource,
        page,
        pageSize,
      ].join("|");
      void refreshSkillAssets({ page, pageSize });
    },
    [
      category,
      installedSkillSource,
      location.key,
      location.pathname,
      location.search,
      refreshSkillAssets,
      skillKeyword,
      skillView,
      tag,
    ],
  );

  const refreshAllSkillAssets = useCallback(async () => {
    const requestId = skillListRequestIdRef.current + 1;
    skillListRequestIdRef.current = requestId;
    setSkillLoading(true);

    try {
      const firstResult = await listSkillAssetsPage({
        keyword: skillKeyword,
        category,
        tags: tag ? [tag] : [],
        page: 1,
        pageSize: 100,
      });
      if (skillListRequestIdRef.current !== requestId) {
        return;
      }

      const records = [...firstResult.records];
      const pageSize = Math.max(1, firstResult.pageSize || 100);
      const totalPages = Math.ceil(firstResult.total / pageSize);

      for (let page = 2; page <= totalPages; page += 1) {
        const pageResult = await listSkillAssetsPage({
          keyword: skillKeyword,
          category,
          tags: tag ? [tag] : [],
          page,
          pageSize,
        });
        if (skillListRequestIdRef.current !== requestId) {
          return;
        }
        records.push(...pageResult.records);
      }

      const deduped = new Map<string, SkillAssetRecord>();
      records.forEach((item) => {
        deduped.set(item.id, item);
      });
      const normalized = Array.from(deduped.values()).map(
        mapSkillAssetRecordToStructuredAsset,
      );
      setSkillAssets(normalized);
      setSkillListTotal(normalized.length);
    } catch (error) {
      if (skillListRequestIdRef.current !== requestId) {
        return;
      }
      console.error("Load all skill assets failed:", error);
    } finally {
      if (skillListRequestIdRef.current === requestId) {
        setSkillLoading(false);
        setSkillsInitialized(true);
      }
    }
  }, [category, skillKeyword, tag]);

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
      const nextPageSize = Math.max(
        1,
        options?.pageSize ?? glossaryListPageSize,
      );
      const totalForToken = Math.max(
        glossaryListTotal,
        (nextPage - 1) * nextPageSize,
      );
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

        const errorMessage = getLocalizedErrorMessage(error);

        setGlossaryLoadError(errorMessage);
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
      reason:
        conflict.reason || t("admin.memoryGlossaryInboxConflictDefaultReason"),
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
            const conflictGroups = await loadGlossaryConflictGroups(
              conflict.groupIds,
            );
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

        const errorMessage = getLocalizedErrorMessage(error);

        setGlossaryInboxError(errorMessage);
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
      if (hideUserGroupSurfaces) {
        return;
      }

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

        const errorMessage = getLocalizedErrorMessage(error);

        setSkillShareCenterError(errorMessage);
      } finally {
        if (skillShareRequestIdRef.current === requestId) {
          setSkillShareCenterLoading(false);
        }
      }
    },
    [hideUserGroupSurfaces, t],
  );

  const refreshShareStatus = useCallback(
    async (
      skillId: string,
      options?: { silent?: boolean; showErrorToast?: boolean },
    ) => {
      if (hideUserGroupSurfaces) {
        return;
      }

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

        const errorMessage = getLocalizedErrorMessage(error);

        setShareStatusError(errorMessage);
      } finally {
        if (shareStatusRequestIdRef.current === requestId) {
          setShareStatusLoading(false);
        }
      }
    },
    [hideUserGroupSurfaces, t],
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
    const filterKey = [
      skillKeyword,
      category || "",
      tag || "",
      installedSkillSource,
    ].join("|");
    const filtersChanged = skillListFilterKeyRef.current !== filterKey;
    if (filtersChanged) {
      skillListFilterKeyRef.current = filterKey;
    }
    const requestPage =
      isNewSkillListEntry || filtersChanged ? 1 : skillListPage;
    if (filtersChanged && skillListPage !== 1) {
      setSkillListPage(1);
    }
    const refreshKey = [
      location.key,
      location.pathname,
      location.search,
      skillKeyword,
      category || "",
      tag || "",
      skillView,
      installedSkillSource,
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
    installedSkillSource,
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
    skillView,
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

    void refreshSkillCategories();
  }, [
    refreshSkillCategories,
    reviewRouteTab,
    routeMemoryTab,
    skillRouteItemId,
  ]);

  useEffect(() => {
    if (hideUserGroupSurfaces || activeTab !== "skills") {
      return;
    }

    void refreshSkillShareCenter({ silent: true });
  }, [activeTab, hideUserGroupSurfaces, refreshSkillShareCenter]);

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
    if (embeddedTab) {
      setActiveTab(embeddedTab);
      return;
    }
    const queryTab = parseMemoryTab(searchParams.get("tab"));
    const nextTab = skillRouteItemId
      ? "skills"
      : glossaryRouteItemId
        ? "glossary"
        : reviewRouteTab || routeListTab || queryTab || "skills";

    setActiveTab((previous) => (previous === nextTab ? previous : nextTab));
  }, [
    glossaryRouteItemId,
    reviewRouteTab,
    routeListTab,
    searchParams,
    skillRouteItemId,
    embeddedTab,
  ]);

  useEffect(() => {
    let ignore = false;

    if (!glossaryRouteItemId) {
      setGlossaryDetailTarget((previous) => (previous ? null : previous));
      return () => {
        ignore = true;
      };
    }

    const matchedGlossary = glossaryAssets.find(
      (item) => item.id === glossaryRouteItemId,
    );
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

    const reviewRouteReloadKey = `${reviewRouteTab}:${reviewRouteItemId}`;
    if (
      reviewRouteReloadKeyRef.current === reviewRouteReloadKey &&
      activeProposal
    ) {
      return;
    }
    reviewRouteReloadKeyRef.current = reviewRouteReloadKey;

    void (async () => {
      const opened = await openChangeReview(
        reviewRouteTab,
        reviewRouteItemId,
        undefined,
        {
          forceReload: true,
          syncRoute: false,
        },
      );

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
    skillAssets,
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
    (tab: ChangeProposalTab, itemId: string) =>
      proposalMap.get(proposalKey(tab, itemId)),
    [proposalKey, proposalMap],
  );
  const activeProposal = useMemo(
    () =>
      activeProposalId
        ? changeProposals.find((item) => item.id === activeProposalId) || null
        : null,
    [activeProposalId, changeProposals],
  );
  const activeBackendSuggestions = useMemo(() => {
    const suggestions = activeProposal?.backendSuggestions || [];
    return [...suggestions].sort((left, right) => {
      const leftIsRemove = isSkillRemoveSuggestion(left);
      const rightIsRemove = isSkillRemoveSuggestion(right);
      if (leftIsRemove === rightIsRemove) {
        return 0;
      }
      return leftIsRemove ? -1 : 1;
    });
  }, [activeProposal]);
  const activeSkillRemoveSuggestions = useMemo(
    () =>
      activeBackendSuggestions.filter((item) =>
        isSkillRemoveSuggestion(item),
      ),
    [activeBackendSuggestions],
  );
  const hasPendingSkillRemoveSuggestion =
    activeSkillRemoveSuggestions.length > 0;
  const isBackendSuggestionSelectable = useCallback(
    (suggestion: EvolutionSuggestionRecord) =>
      !hasPendingSkillRemoveSuggestion ||
      isSkillRemoveSuggestion(suggestion),
    [hasPendingSkillRemoveSuggestion],
  );
  const selectableBackendSuggestionIds = useMemo(
    () =>
      activeBackendSuggestions
        .filter((item) => isBackendSuggestionSelectable(item))
        .map((item) => item.id),
    [activeBackendSuggestions, isBackendSuggestionSelectable],
  );
  const isBackendSuggestionReviewMode =
    Boolean(activeProposal?.backendSuggestions) &&
    (activeProposal?.tab === "skills" ||
      activeBackendSuggestions.length > 0 ||
      approvedBackendSuggestionIds.length > 0 ||
      rejectedBackendSuggestionIds.length > 0);
  const activeBackendSuggestionSourceText = useMemo(() => {
    if (!activeProposal) {
      return "";
    }

    return getSkillBodyContentForDisplay(activeProposal.before.content);
  }, [activeProposal]);
  const backendDraftDiffLines = backendSkillDiffLines;

  const loadSkillDraftPreview = useCallback(async (skillId: string) => {
    const preview = await previewSkillDraft(skillId);
    setBackendDraftPreview(preview);
    setBackendSkillDiffLines(preview.diffLines);
    return preview;
  }, []);
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
              fieldSuggestionIds.description ||
              activeProposal.backendSuggestionId,
          }
        : null,
      activeProposal.before.category !== activeProposal.after.category
        ? {
            key: "category",
            label: t("admin.memoryCategory"),
            before: activeProposal.before.category,
            after: activeProposal.after.category,
            backendSuggestionId:
              fieldSuggestionIds.category ||
              activeProposal.backendSuggestionId,
          }
        : null,
      activeProposal.before.tags.join(",") !==
      activeProposal.after.tags.join(",")
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
      Boolean(activeProposal.before.protect) !==
      Boolean(activeProposal.after.protect)
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
    return fieldChanges.filter((item): item is ProposalFieldChange =>
      Boolean(item),
    );
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
      setApprovedBackendSuggestionIds([]);
      setRejectedBackendSuggestionIds([]);
      setBackendDraftPreview(null);
      setBackendSkillDiffLines([]);
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
    setApprovedBackendSuggestionIds([]);
    setRejectedBackendSuggestionIds([]);
    setBackendDraftPreview(null);
    setBackendSkillDiffLines([]);
    setBackendDraftLoading(false);
    setBackendDraftSubmitting("");

    if (activeProposal.backendSuggestions) {
      if (confirmedDraftProposalIdsRef.current.has(activeProposal.id)) {
        return () => {
          ignore = true;
        };
      }

      setActiveReviewStep(1);
      if (activeProposal.backendDraftPreview) {
        setBackendDraftPreview(activeProposal.backendDraftPreview);
        setBackendDraftLoading(true);
        void loadSkillDraftPreview(activeProposal.targetId).finally(() => {
          if (!ignore) {
            setBackendDraftLoading(false);
          }
        });
        return () => {
          ignore = true;
        };
      }
      setBackendDraftLoading(true);
      void (async () => {
        try {
          await loadSkillDraftPreview(activeProposal.targetId);
        } catch (error) {
          if (!ignore) {
            console.error("Load draft preview failed:", error);
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
  const isBackendSuggestionBatchBusy = Boolean(
    backendSuggestionBatchSubmitting,
  );
  const isAnyBackendSuggestionMutating =
    isBackendSuggestionBatchBusy ||
    Object.keys(backendSuggestionSubmitting).length > 0;

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

  const activeProposalMerged = useMemo<StructuredAsset | null>(() => {
    if (!activeProposal) {
      return null;
    }

    const useAfterValue = (fieldKey: ProposalFieldKey) =>
      activeProposalFieldChanges.some((field) => field.key === fieldKey) &&
      (proposalFieldDecisions[fieldKey] ?? "pending") === "accept";

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
  }, [activeProposal, activeProposalFieldChanges, proposalFieldDecisions]);

  const effectiveProposalMerged = useMemo<StructuredAsset | null>(
    () => manualMergedDraft ?? activeProposalMerged,
    [activeProposalMerged, manualMergedDraft],
  );

  const hasEffectiveChange = useMemo(() => {
    if (!activeProposal || !effectiveProposalMerged) {
      return false;
    }

    const merged = effectiveProposalMerged;
    return (
      activeProposal.before.name !== merged.name ||
      activeProposal.before.description !== merged.description ||
      activeProposal.before.category !== merged.category ||
      activeProposal.before.tags.join(",") !== merged.tags.join(",") ||
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
    const beforeText = serializeStructuredAsset(activeProposal.before, {
      name: t("admin.memoryName"),
      description: t("admin.memoryDescription"),
      category: t("admin.memoryCategory"),
      tags: t("admin.memoryTagSet"),
      ...commonLabels,
    });
    const afterText = serializeStructuredAsset(effectiveProposalMerged, {
      name: t("admin.memoryName"),
      description: t("admin.memoryDescription"),
      category: t("admin.memoryCategory"),
      tags: t("admin.memoryTagSet"),
      ...commonLabels,
    });

    const changedFields = activeProposalFieldChanges
      .filter(
        (field) =>
          (proposalFieldDecisions[field.key] ?? "pending") === "accept",
      )
      .map((field) => field.label);

    return {
      beforeText,
      afterText,
      lines: buildDiffLinesWithInline(beforeText, afterText),
      changedFields,
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
        (field) =>
          (proposalFieldDecisions[field.key] ?? "pending") === "accept",
      ).length,
    [activeProposalFieldChanges, proposalFieldDecisions],
  );
  const rejectedFieldCount = useMemo(
    () =>
      activeProposalFieldChanges.filter(
        (field) =>
          (proposalFieldDecisions[field.key] ?? "pending") === "reject",
      ).length,
    [activeProposalFieldChanges, proposalFieldDecisions],
  );
  const pendingFieldCount = useMemo(
    () =>
      activeProposalFieldChanges.filter(
        (field) =>
          (proposalFieldDecisions[field.key] ?? "pending") === "pending",
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
    return skillAssets.filter((item) => matchesStructuredFilter(item));
  }, [matchesStructuredFilter, skillAssets]);

  const filteredInstalledSkillTree = useMemo<SkillTreeNode[]>(() => {
    return skillAssets.filter((item) => {
      if (installedSkillSource === "all") {
        return true;
      }
      return resolveSkillSourceType(item) === installedSkillSource;
    });
  }, [installedSkillSource, skillAssets]);

  const resetFilters = () => {
    setQuery("");
    setSearchInput("");
    setCategory(undefined);
    setTag(undefined);
    setGlossarySource(undefined);
    setInstalledSkillSource("all");
    setMarketSkillSource("all");
    setMarketCategory("all");
    setSkillView("installed");
  };

  const readFileAsText = (file: File) =>
    new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ""));
      reader.onerror = () => reject(reader.error);
      reader.readAsText(file);
    });

  const appendImportedSkillContent = (
    existingContent: string,
    importedContent: string,
  ) => {
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
      const contentImportMode =
        await confirmSkillContentImportMode(existingContent);
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
                  description:
                    item.description || frontMatter?.description || "",
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

  const handleImportSkillPackage = (file: File) => {
    void handleUploadSkillFile(file, {
      parentOnlyMarkdown: true,
    });
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
    item?: StructuredAsset | GlossaryAsset,
    options?: { skipSkillDetailLoad?: boolean },
  ) => {
    setPendingGlossaryMergeSourceIds([]);
    setModalMode(mode);

    if (!item) {
      setDraft(createDraft());
      setModalOpen(true);
      return;
    }

    if ("term" in item) {
      setDraft({
        id: item.id,
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
      });
    } else {
      setDraft(
        createStructuredDraft(item, {
          stripFrontMatter: activeTab === "skills" && mode !== "add",
        }),
      );

      if (
        activeTab === "skills" &&
        mode !== "add" &&
        !options?.skipSkillDetailLoad
      ) {
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

  const openSkillCreateModal = (source: SkillCreateSource) => {
    if (skillSaving) {
      return;
    }

    if (source === "zip") {
      setPendingSkillSourceUrl("");
      skillZipInputRef.current?.click();
      return;
    }

    setPendingSkillPackageFile(null);
    setPendingSkillSourceUrl("");
    setSkillUrlImportDraft("");
    setSkillUrlImportOpen(true);
  };

  const handleConfirmSkillUrlImport = async () => {
    if (skillSaving) {
      return;
    }

    const trimmedUrl = skillUrlImportDraft.trim();
    if (!trimmedUrl) {
      message.warning(t("admin.memorySkillUploadRepoPlaceholder"));
      return;
    }

    setSkillUrlImportOpen(false);
    setSkillSaving(true);

    try {
      await createSkillAsset({
        name: t("admin.memorySkillUploadDefaultName"),
        description: t("admin.memorySkillUploadPersonalDesc"),
        category: "personal",
        tags: [],
        isEnabled: true,
        source: { type: "url", url: trimmedUrl },
      });
      await Promise.all([refreshSkillAssets(), refreshSkillCategories()]);
      message.success(
        t("admin.memorySkillUploadSuccess", {
          name: t("admin.memorySkillUploadDefaultName"),
        }),
      );
    } catch (error) {
      console.error("Import skill from URL failed:", error);
      message.error(t("admin.memorySkillUploadFailed"));
    } finally {
      setSkillSaving(false);
    }
  };

  const handleSkillZipFileSelected = async (
    event: ChangeEvent<HTMLInputElement>,
  ) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file || skillSaving) {
      return;
    }
    const name = file.name.toLowerCase();
    const valid =
      name.endsWith(".zip") ||
      name.endsWith(".tgz") ||
      name.endsWith(".tar") ||
      name.endsWith(".gz");
    if (!valid) {
      message.warning(t("admin.memorySkillUploadPackageTypeError"));
      return;
    }

    const inferredName =
      file.name.replace(/\.(zip|tgz|tar|gz)$/i, "").trim() ||
      t("admin.memorySkillUploadDefaultName");

    try {
      setSkillSaving(true);
      const upload = await uploadSkillTempFile(file);
      await createSkillAsset({
        name: inferredName,
        description: t("admin.memorySkillUploadPersonalDesc"),
        category: "personal",
        tags: [],
        isEnabled: true,
        source: { type: "uploaded_zip", uploadId: upload.uploadId },
      });
      await Promise.all([refreshSkillAssets(), refreshSkillCategories()]);
      message.success(
        t("admin.memorySkillUploadSuccess", { name: inferredName }),
      );
    } catch (error) {
      console.error("Upload skill package failed:", error);
      message.error(t("admin.memorySkillUploadFailed"));
    } finally {
      setSkillSaving(false);
    }
  };

  const closeModal = () => {
    setModalOpen(false);
    setPendingGlossaryMergeSourceIds([]);
    setPendingSkillPackageFile(null);
    setPendingSkillSourceUrl("");
    syncShareParams(activeTab);
  };

  const openShareModal = (
    tab: ShareableTab,
    item: StructuredAsset,
  ) => {
    if (hideUserGroupSurfaces) {
      return;
    }
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
    if (hideUserGroupSurfaces) {
      return;
    }
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
      const detail = await getSkillAssetDetail(
        share.sourceSkillId || share.skillId || share.id,
      );
      openModal("view", detail || buildStructuredAssetFromSkillShare(share), {
        skipSkillDetailLoad: true,
      });
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
    } finally {
      setSkillShareAction(share.id);
    }
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
          headRevisionId: detail.headRevisionId,
          draft: detail.draft,
          isEnabled: detail.isEnabled,
          autoEvo: detail.autoEvo,
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
      const matchedSkill = skillAssets.find((item) => item.id === itemId);
      const hasReviewableDraft = matchedSkill
        ? hasSkillDraftPreviewStatus(matchedSkill)
        : false;

      if (
        !shouldReloadProposal &&
        !isSkillUpdatePending(skillUpdateStatus) &&
        !hasReviewableDraft
      ) {
        message.info(t("admin.memoryDiffNoPending"));
        return false;
      }

      if (!matchedSkill) {
        message.warning(t("admin.memoryDiffTargetMissing"));
        return false;
      }

      try {
        const backendProposal = await loadSkillChangeProposal(matchedSkill);
        if (!backendProposal) {
          setChangeProposals((previous) =>
            previous.filter((item) => item.targetId !== itemId),
          );
          message.info(t("admin.memoryDiffNoPending"));
          return false;
        }

        setChangeProposals((previous) => {
          const next = previous.filter(
            (item) => item.targetId !== backendProposal.targetId,
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
        return false;
      }
      return true;
    }

    if (!skillAssets.some((item) => item.id === itemId)) {
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
    setProposalFieldDecisions((previous) => ({
      ...previous,
      [fieldKey]: decision,
    }));
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
          proposal.backendSuggestions?.filter(
            (item) => !handledIdSet.has(item.id),
          ) || [];

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
  const clearBackendSuggestionSubmitting = (suggestionIds: string[]) => {
    setBackendSuggestionSubmitting((previous) => {
      const next = { ...previous };
      suggestionIds.forEach((item) => {
        delete next[item];
      });
      return next;
    });
  };
  const setBackendSuggestionSelected = (
    suggestionId: string,
    checked: boolean,
  ) => {
    const suggestion = activeBackendSuggestions.find(
      (item) => item.id === suggestionId,
    );
    if (suggestion && !isBackendSuggestionSelectable(suggestion)) {
      return;
    }
    setSelectedBackendSuggestionIds((previous) => {
      if (checked) {
        return previous.includes(suggestionId)
          ? previous
          : [...previous, suggestionId];
      }
      return previous.filter((item) => item !== suggestionId);
    });
  };
  const setAllBackendSuggestionsSelected = (checked: boolean) => {
    setSelectedBackendSuggestionIds(
      checked ? [...selectableBackendSuggestionIds] : [],
    );
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
      console.error(
        "Submit evolution suggestion field decision failed:",
        error,
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
    if (
      backendSuggestionMutationLockRef.current ||
      isAnyBackendSuggestionMutating
    ) {
      return;
    }

    const suggestionId = suggestion.id;
    const shouldDirectDeleteSkill =
      activeProposal.tab === "skills" &&
      decision === "accept" &&
      isSkillRemoveSuggestion(suggestion);
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
        await refreshSkillAssets({ preserveChangeProposals: true });
      } else {
        await refreshSkillAssets({ preserveChangeProposals: true });
      }
    } catch (error) {
      console.error("Submit backend suggestion decision failed:", error);
      if (decision === "accept") {
        setActiveReviewStep(0);
        setBackendDraftLoading(false);
      }
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
    if (
      backendSuggestionMutationLockRef.current ||
      isAnyBackendSuggestionMutating
    ) {
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
      ...suggestionIds.reduce<Record<string, ProposalFieldDecision>>(
        (result, item) => {
          result[item] = decision;
          return result;
        },
        {},
      ),
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
              ...suggestionIds.filter(
                (item) => !approvedBackendSuggestionIds.includes(item),
              ),
            ]
          : approvedBackendSuggestionIds;

      if (decision === "accept") {
        setActiveReviewStep(1);
        setBackendDraftLoading(true);
        await batchApproveEvolutionSuggestions(suggestionIds);
        message.success(
          t("admin.memoryDiffBatchApproveSuccess", {
            count: suggestionIds.length,
          }),
        );
        markBackendSuggestionsApproved(suggestionIds);
      } else {
        await batchRejectEvolutionSuggestions(suggestionIds);
        message.success(
          t("admin.memoryDiffBatchRejectSuccess", {
            count: suggestionIds.length,
          }),
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
        await refreshSkillAssets({ preserveChangeProposals: true });
      } else {
        await refreshSkillAssets({ preserveChangeProposals: true });
      }
    } catch (error) {
      console.error("Submit backend suggestion batch decision failed:", error);
      if (decision === "accept") {
        setActiveReviewStep(0);
        setBackendDraftLoading(false);
      }
    } finally {
      clearBackendSuggestionSubmitting(suggestionIds);
      setBackendSuggestionBatchSubmitting("");
      backendSuggestionMutationLockRef.current = false;
    }
  };
  const buildBackendDraftUserInstruct = (extraInstruction = "") => {
    const instructions = [
      t("admin.memorySkillDraftDefaultInstruction"),
      extraInstruction.trim(),
    ].filter(Boolean);
    return instructions.join("\n");
  };
  const startBackendDraftPreviewLoading = () => {
    setActiveReviewStep(1);
    setBackendDraftPreview(null);
    setBackendSkillDiffLines([]);
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
      await loadSkillDraftPreview(activeProposal.targetId);
      return true;
    } catch (error) {
      console.error("Load draft preview failed:", error);
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
      if (!activeProposal) {
        return false;
      }
      await generateSkillDraft(activeProposal.targetId, {
        suggestionIds: shouldOmitSuggestionIds
          ? undefined
          : suggestionIds,
        userInstruct,
      });
      await loadSkillDraftPreview(activeProposal.targetId);
      return true;
    } catch (error) {
      console.error("Load managed draft preview failed:", error);
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
      await confirmSkillDraft(activeProposal.targetId);
      message.success(t("admin.memorySkillDraftConfirmSuccess"));
      confirmedDraftProposalIdsRef.current.add(activeProposal.id);
      setChangeProposals((previous) =>
        previous.filter((item) => item.id !== activeProposal.id),
      );
      setActiveProposalId(undefined);
      await refreshSkillAssets({ preserveChangeProposals: true });
      navigateToMemoryList(activeProposal.tab);
    } catch (error) {
      console.error("Confirm managed draft failed:", error);
    } finally {
      setBackendDraftSubmitting("");
    }
  };
  const discardBackendDraft = async () => {
    setBackendDraftSubmitting("discard");
    try {
      if (!activeProposal) {
        return;
      }
      await discardSkillDraft(activeProposal.targetId);
      message.success(t("admin.memorySkillDraftDiscardSuccess"));
      setBackendDraftPreview(null);
      setApprovedBackendSuggestionIds([]);
      setRejectedBackendSuggestionIds([]);
      setSelectedBackendSuggestionIds([]);
      setChangeProposals((previous) =>
        previous.filter((item) => item.id !== activeProposal.id),
      );
      setActiveProposalId(undefined);
      navigateToMemoryList("skills");
      await refreshSkillAssets();
    } catch (error) {
      console.error("Discard managed draft failed:", error);
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
  const sendReviewQuestion = async () => {
    const text = qaQuestionDraft.trim();
    if (!text) {
      return;
    }

    setQaQuestionDraft("");

    if (activeProposal?.backendSuggestions && activeReviewStep === 1) {
      const updated = await loadBackendDraftPreview(
        approvedBackendSuggestionIds,
        text,
        {
          omitSuggestionIds: true,
        },
      );
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
    if (activeProposal?.backendSuggestions) {
      void loadCurrentDraftPreview();
      return;
    }

    if (
      activeProposal?.backendSuggestions &&
      (activeProposal.backendSuggestions?.length ||
        approvedBackendSuggestionIds.length)
    ) {
      void loadBackendDraftPreview(approvedBackendSuggestionIds);
      return;
    }
    setActiveReviewStep(1);
  };

  const goToReviewChoose = () => {
    setIsPreviewContentEditing(false);
    if (activeProposal?.backendSuggestions) {
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
    setActiveReviewStep(0);
  };

  const finishCloseChangeReview = () => {
    setIsPreviewContentEditing(false);
    setActiveProposalId(undefined);
    reviewRouteReloadKeyRef.current = "";
    navigateToMemoryList(activeProposal?.tab || activeTab);
  };
  const closeChangeReview = () => {
    if (
      activeProposal?.backendSuggestions &&
      activeReviewStep === 1
    ) {
      finishCloseChangeReview();
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
      manualMergedDraft?.content ?? activeProposalMerged.content;

    setManualPreviewContentDraft(currentContent);
    setIsPreviewContentEditing(true);
  };

  const savePreviewContentEdit = () => {
    if (!activeProposal || !effectiveProposalMerged) {
      return;
    }

    const nextMerged = cloneStructuredAsset(effectiveProposalMerged);
    nextMerged.content = manualPreviewContentDraft;
    setManualMergedDraft(nextMerged);

    setIsPreviewContentEditing(false);
    message.success(t("admin.memoryDiffManualSaveSuccess"));
  };

  const approveChangeProposal = async () => {
    if (!activeProposal || !effectiveProposalMerged) {
      return;
    }

    if (activeProposal.backendSuggestionId) {
      const suggestionId = activeProposal.backendSuggestionId;
      const isSuggestionAlreadyReviewed =
        reviewedBackendSuggestionIds.includes(suggestionId);
      setReviewSuggestionSubmitting(true);
      try {
        if (hasEffectiveChange) {
          if (!isSuggestionAlreadyReviewed) {
            await approveEvolutionSuggestion(suggestionId);
            markBackendSuggestionReviewed(suggestionId);
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
        await refreshSkillAssets();
      } catch (error) {
        console.error("Submit evolution suggestion failed:", error);
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

    const itemExists = skillAssets.some(
      (item) => item.id === activeProposal.targetId,
    );
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
          ? cloneStructuredAsset(effectiveProposalMerged)
          : item,
      ),
    );

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
              proposal.mergeFrom?.some((mergeItem) =>
                removedIdSet.has(mergeItem.id),
              ),
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

  const handleDelete = (item: StructuredAsset | GlossaryAsset) => {
    const itemName = "term" in item ? item.term : item.name;

    Modal.confirm({
      title: t("common.delete"),
      content:
        activeTab === "skills"
          ? t("admin.memorySkillDeleteConfirm", { name: itemName })
          : t("admin.memoryDeleteConfirm", { name: itemName }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        if (activeTab === "skills") {
          try {
            await removeSkillAsset(item.id);
            await refreshSkillAssets({ page: skillListPage });
            message.success(t("admin.memorySkillDeleteSuccess"));
          } catch (error) {
            console.error("Delete skill asset failed:", error);
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
      const builtinSkillUid =
        (
          item as StructuredAsset & { marketItemId?: string }
        ).marketItemId?.trim() || item.id.trim();
      if (!builtinSkillUid) {
        message.warning(t("admin.memoryBuiltinSkillMissing"));
        return;
      }

      setBuiltinSkillEnableLoading((previous) =>
        new Set(previous).add(builtinSkillUid),
      );
      try {
        await enableBuiltinSkill(builtinSkillUid);
        // No extra list refresh here — caller handles optimistic UI update.
        // Data syncs when the user switches tabs.
        message.success(t("admin.memoryBuiltinSkillEnableSuccess"));
      } catch (error) {
        console.error("Enable builtin skill failed:", error);
        throw error;
      } finally {
        setBuiltinSkillEnableLoading((previous) => {
          const next = new Set(previous);
          next.delete(builtinSkillUid);
          return next;
        });
      }
    },
    [t],
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
        normalizedAliases.some(
          (item) => item.length > GLOSSARY_ALIAS_MAX_LENGTH,
        )
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
      } finally {
        setGlossarySaving(false);
      }

      return;
    } else if (activeTab === "skills") {
      if (!draft.name.trim()) {
        message.warning(`${t("common.pleaseInput")}${t("admin.memoryName")}`);
        return;
      }
      if (!draft.description.trim()) {
        message.warning(
          `${t("common.pleaseInput")}${t("admin.memoryDescription")}`,
        );
        return;
      }

      const normalizedSkillTags = normalizeTagValues(draft.tags);
      if (
        modalMode !== "edit" &&
        normalizedSkillTags.length > SKILL_TAG_MAX_COUNT
      ) {
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
        category: draft.category.trim(),
        tags: normalizedSkillTags,
        content: draft.content.trim(),
      };

      try {
        setSkillSaving(true);
        if (modalMode === "edit") {
          if (!payload.id) {
            message.warning(t("admin.memoryDiffTargetMissing"));
            return;
          }

          await patchSkillAsset(
            payload.id,
            buildSkillUpdatePayload({
              name: payload.name,
              description: payload.description,
            }),
          );
          setChangeProposals((previous) =>
            previous.filter(
              (item) =>
                !(item.tab === "skills" && item.targetId === payload.id),
            ),
          );
        } else {
          if (
            !draft.content.trim() &&
            !pendingSkillPackageFile &&
            !pendingSkillSourceUrl.trim()
          ) {
            message.warning(t("admin.memorySkillUploadMissing"));
            return;
          }

          let source: CreateSkillPayload["source"];
          if (pendingSkillSourceUrl.trim()) {
            source = { type: "url", url: pendingSkillSourceUrl.trim() };
          } else {
            const packageFile =
              pendingSkillPackageFile ||
              (await buildSkillZipBlob({
                name: payload.name,
                description: payload.description,
                body: draft.content,
                filename: payload.name,
              }));
            const upload = await uploadSkillTempFile(packageFile);
            source = { type: "uploaded_zip", uploadId: upload.uploadId };
          }

          await createSkillAsset({
            name: payload.name,
            description: payload.description,
            category: payload.category || "personal",
            tags: payload.tags,
            isEnabled: true,
            source,
          });
          setPendingSkillPackageFile(null);
          setPendingSkillSourceUrl("");
        }

        await Promise.all([refreshSkillAssets(), refreshSkillCategories()]);
      } catch (error) {
        console.error("Save skill draft failed:", error);
        message.error(getLocalizedErrorMessage(error));
        return;
      } finally {
        setSkillSaving(false);
      }

      setModalOpen(false);
      message.success(t(saveSuccessMessageKey));
      return;
    }

    setModalOpen(false);
    message.success(t(saveSuccessMessageKey));
  };

  const handleConfirmShare = async () => {
    if (hideUserGroupSurfaces || !shareTarget) {
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
    if (hideUserGroupSurfaces || !shareModalOpen) {
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

        const userPayload =
          (userResponse.data as any)?.data || userResponse.data || {};
        const groupPayload =
          (groupResponse.data as any)?.data || groupResponse.data || {};

        setShareUsers(
          Array.isArray(userPayload.users) ? userPayload.users : [],
        );
        setShareGroups(
          Array.isArray(groupPayload.groups) ? groupPayload.groups : [],
        );
      } catch (error) {
        console.error("Fetch share targets failed:", error);
      } finally {
        setShareLoading(false);
      }
    };

    fetchShareOptions();
  }, [hideUserGroupSurfaces, shareModalOpen, t]);

  useEffect(() => {
    if (
      hideUserGroupSurfaces ||
      !shareModalOpen ||
      !shareTarget ||
      shareTarget.tab !== "skills"
    ) {
      setShareStatusError("");
      setShareStatusRecords([]);
      setShareStatusLoading(false);
      return;
    }

    void refreshShareStatus(shareTarget.item.id, { showErrorToast: false });
  }, [hideUserGroupSurfaces, shareModalOpen, shareTarget, refreshShareStatus]);

  useEffect(() => {
    const sharedTab = routeListTab || parseMemoryTab(searchParams.get("tab"));
    const sharedItemId = searchParams.get("item");

    if (!sharedTab || !sharedItemId) {
      handledShareKeyRef.current = "";
      return;
    }

    if (sharedTab !== "skills") {
      return;
    }
    if (sharedTab === "skills" && !skillsInitialized) {
      return;
    }

    const shareKey = `${sharedTab}:${sharedItemId}`;
    if (handledShareKeyRef.current === shareKey) {
      return;
    }

    const matchedItem = shareableItems[sharedTab].find(
      (item) => item.id === sharedItemId,
    );
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
      const backendProposals = proposals.filter(
        (proposal) => proposal.backendConflictId,
      );
      if (backendProposals.length) {
        await Promise.all(
          backendProposals.map((proposal) => {
            const conflictId = proposal.backendConflictId || proposal.id;
            const conflictWord =
              proposal.backendConflictWord || proposal.after.term;
            const resolution = resolutions[proposal.id];
            const mode =
              resolution?.mode ||
              (proposal.backendConflictGroupIds?.length
                ? "separate"
                : "create");
            const selectedGroupIds = resolution?.mergeGroupIds?.length
              ? resolution.mergeGroupIds
              : resolution?.selectedGroupIds?.length
                ? resolution.selectedGroupIds
                : proposal.backendConflictGroupIds || [];

            if (mode === "merge") {
              if (selectedGroupIds.length < 2) {
                throw new Error(
                  t("admin.memoryGlossaryInboxMergeSelectAtLeastTwo"),
                );
              }

              const targetGroups = proposal.backendConflictGroups || [];
              const mergeGroupsFromResolution =
                resolution?.mergeGroups?.filter((item) => item.length >= 2) ||
                [];
              const mergeGroups = mergeGroupsFromResolution.length
                ? mergeGroupsFromResolution
                : [selectedGroupIds];
              const fallbackMergedTerm =
                targetGroups.find((group) => mergeGroups[0]?.includes(group.id))
                  ?.term || proposal.after.term;
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
                      aliasesSource.map((item) => item.trim()).filter(Boolean),
                    ),
                  ),
                  description,
                };
              });
              const firstMergedGroupIds = mergeGroups
                .map((groupIds) => groupIds[0])
                .filter(Boolean);
              if (!firstMergedGroupIds.length) {
                throw new Error(
                  t("admin.memoryGlossaryInboxSelectTargetFirst"),
                );
              }
              const writeGroupIds = resolution?.writeGroupIds || [];
              const shouldWriteToMergedGroup =
                !writeGroupIds.length ||
                writeGroupIds.some((groupId) =>
                  groupId.startsWith(MERGED_GLOSSARY_GROUP_OPTION_ID),
                );
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
                throw new Error(
                  t("admin.memoryGlossaryInboxSelectTargetFirst"),
                );
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
                throw new Error(
                  t("admin.memoryGlossaryInboxSelectTargetFirst"),
                );
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
            const normalizedNewAliases = (
              resolution?.newGroupAliases?.length
                ? resolution.newGroupAliases
                : proposal.after.aliases
            )
              .map((item) => item.trim())
              .filter(Boolean);
            if (normalizedNewAliases.some((alias) => alias === newGroupTerm)) {
              throw new Error(t("admin.memoryGlossaryGroupAliasDuplicate"));
            }
            const newGroupContent = (
              resolution?.newGroupContent ?? proposal.after.content
            ).trim();
            if (
              newGroupTerm &&
              newGroupContent &&
              newGroupTerm === newGroupContent
            ) {
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
              group_ids: extraWriteGroupIds.length
                ? extraWriteGroupIds
                : undefined,
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
          const mergeSourceIds =
            proposal.mergeFrom?.map((item) => item.id) ?? [];
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
          (proposal) =>
            !proposals.some((selected) => selected.id === proposal.id),
        ),
      );
      message.success(t("admin.memoryGlossaryInboxAcceptSuccess"));
    } catch (error) {
      console.error("Accept glossary conflicts failed:", error);
    } finally {
      setGlossaryInboxSubmitting("");
    }
  };
  const rejectGlossaryProposals = async (
    proposals: GlossaryChangeProposal[],
  ) => {
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
        await Promise.all(
          backendConflictIds.map((id) => removeGlossaryConflict(id)),
        );
        await refreshGlossaryConflicts({ silent: true });
        message.success(t("admin.memoryGlossaryInboxRejectSuccess"));
        return;
      }

      setGlossaryChangeProposals((previous) =>
        previous.filter(
          (proposal) =>
            !proposals.some((selected) => selected.id === proposal.id),
        ),
      );
      message.success(t("admin.memoryGlossaryInboxRejectSuccess"));
    } catch (error) {
      console.error("Reject glossary conflicts failed:", error);
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
          activeTab === "skills"
            ? getPendingProposal("skills", record.id)
            : undefined;
        const hasReviewableDraft =
          activeTab === "skills" && hasSkillDraftPreviewStatus(record);
        const showPendingTag =
          !record.autoEvo && (Boolean(pendingProposal) || hasReviewableDraft);

        return (
          <div
            className={`memory-table-main${
              activeTab === "skills" ? " memory-table-main-with-icon" : ""
            }`}
          >
            {activeTab === "skills" ? (
              <span className="memory-table-main-icon" aria-hidden="true">
                {renderSkillCategoryIcon(record.category)}
              </span>
            ) : null}
            <div className="memory-table-main-copy">
              <div className="memory-table-main-title">
                {activeTab === "skills" ? (
                  <button
                    type="button"
                    className="memory-term-link"
                    onClick={() => navigateToSkillDetail(record.id)}
                  >
                    {record.name}
                  </button>
                ) : (
                  <span>{record.name}</span>
                )}
                {record.draft?.hasUncommittedDraft ? (
                  <Tag color="gold">{t("admin.memoryDiffPendingTag")}</Tag>
                ) : null}
                {showPendingTag ? (
                  <Tag color="orange">{t("admin.memoryDiffPendingTag")}</Tag>
                ) : null}
                {activeTab === "skills" && record.hasPendingRemoveSuggestion ? (
                  <Tag color="red">
                    {t("admin.memorySkillPendingRemoveTag")}
                  </Tag>
                ) : null}
                {record.protect ? (
                  <Tag className="memory-protect-tag" bordered={false}>
                    <LockOutlined />
                    <span>
                      {t("admin.memoryProtect", { defaultValue: "保护" })}
                    </span>
                  </Tag>
                ) : null}
              </div>
              {record.description ? (
                <Tooltip
                  title={
                    <div className="memory-text-popover-content">
                      {record.description}
                    </div>
                  }
                  overlayClassName="memory-text-popover"
                  placement="topLeft"
                  trigger="hover"
                >
                  <div className="memory-table-main-desc">
                    {record.description}
                  </div>
                </Tooltip>
              ) : (
                <div className="memory-table-main-desc">
                  {record.description}
                </div>
              )}
            </div>
          </div>
        );
      },
    },
    {
      title: t("admin.memoryCategory"),
      dataIndex: "category",
      key: "category",
      width: 180,
      render: (value: string) =>
        value ? (
          <Tag className="memory-category-tag" bordered={false}>
            {value}
          </Tag>
        ) : (
          "-"
        ),
    },
  ];

  const genericColumns: ColumnsType<StructuredAsset> = [
    ...structuredInfoColumns,
    {
      title: t("admin.memorySkillEnabled"),
      key: "isEnabled",
      width: 90,
      render: (_value, record) => (
        <Switch
          checked={record.isEnabled !== false}
          loading={skillEnableLoading.has(record.id)}
          onChange={(checked) => {
            void (async () => {
              setSkillEnableLoading((prev) => new Set(prev).add(record.id));
              try {
                await patchSkillAsset(
                  record.id,
                  buildSkillPatchPayload(record, { isEnabled: checked }),
                );
                await refreshSkillAssets({ preserveChangeProposals: true });
                message.success(
                  checked
                    ? t("admin.memorySkillEnableSuccess")
                    : t("admin.memorySkillDisableSuccess"),
                );
              } catch (error) {
                console.error("Toggle is_enabled failed:", error);
                await refreshSkillAssets({ preserveChangeProposals: true });
              } finally {
                setSkillEnableLoading((prev) => {
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
      title: t("admin.memoryAutoUpdate"),
      key: "autoEvo",
      width: 90,
      render: (_value, record) => {
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
                  await patchSkillAsset(
                    record.id,
                    buildSkillPatchPayload(record, { autoEvo: checked }),
                  );
                  await refreshSkillAssets({ preserveChangeProposals: true });
                } catch (error) {
                  console.error("Toggle auto_evo failed:", error);
                  await refreshSkillAssets({ preserveChangeProposals: true });
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
          <Tooltip title={t("admin.memorySkillAutoEvoDisabledByRemove")}>
            {switchNode}
          </Tooltip>
        ) : (
          switchNode
        );
      },
    },
    {
      title: t("admin.memoryOperations"),
      key: "actions",
      width: 200,
      fixed: "right",
      render: (_value, record) => (
        <Space size={4}>
          <Tooltip title={t("admin.memoryEditItem")}>
            <Button
              type="text"
              icon={<EditOutlined />}
              onClick={() => openModal("edit", record)}
            />
          </Tooltip>
          {!hideUserGroupSurfaces ? (
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
        </Space>
      ),
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
                <span>
                  {t("admin.memoryProtect", { defaultValue: "保护" })}
                </span>
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
  const tagOptions = [...new Set([...availableTags, ...draft.tags])].map(
    (item) => ({
      label: item,
      value: item,
    }),
  );
  const isGlossaryRouteRequested = Boolean(glossaryRouteItemId);
  const isReviewMode = Boolean(
    activeProposal && (activeProposalDiff || isBackendSuggestionReviewMode),
  );
  const glossaryDetailExists = useMemo(
    () =>
      glossaryDetailTarget
        ? glossaryAssets.some((item) => item.id === glossaryDetailTarget.id)
        : false,
    [glossaryAssets, glossaryDetailTarget],
  );
  const getSkillShareStatusMeta = (status: SkillShareStatus) => {
    if (status === "accepted") {
      return {
        color: "success",
        text: t("admin.memorySkillShareStatusAccepted"),
      };
    }
    if (status === "rejected") {
      return {
        color: "error",
        text: t("admin.memorySkillShareStatusRejected"),
      };
    }
    if (status === "failed") {
      return {
        color: "warning",
        text: t("admin.memorySkillShareStatusFailed"),
      };
    }
    if (status === "unknown") {
      return {
        color: "default",
        text: t("admin.memorySkillShareStatusUnknown"),
      };
    }
    return {
      color: "processing",
      text: t("admin.memorySkillShareStatusPending"),
    };
  };
  const outletContext = {
    t,
    activeTab,
    setActiveTab,
    currentTabMeta,
    tabMeta,
    memoryTabOrder,
    openSkillShareCenter,
    incomingPendingCount: hideUserGroupSurfaces ? 0 : incomingPendingCount,
    hideUserGroupSurfaces,
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
    glossaryDetailTarget,
    glossaryDetailExists,
    closeGlossaryDetail,
    openModal,
    openSkillCreateModal,
    glossarySourceColorMap,
    glossarySourceLabelMap,
    resetFilters,
    navigateToMemoryList,
    navigateToSkillDetail,
    setGlossaryDetailTarget,
    setGlossaryInboxOpen,
    refreshSkillAssets,
    refreshAllSkillAssets,
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
    filteredGlossaryItems,
    glossaryColumns,
    selectedGlossaryAssetIds,
    setGlossaryListPage,
    setGlossaryListPageSize,
    setSelectedGlossaryAssetIds,
    skillLoading,
    skillsInitialized,
    manualSkillReviewSummary,
    manualSkillReviewLoading,
    manualSkillReviewRunning,
    manualSkillReviewResults,
    manualSkillReviewResultStatus,
    refreshManualSkillReviewSummary,
    handleRunManualSkillReview,
    skillListPage,
    skillListPageSize,
    skillListTotal,
    setSkillListPage,
    setSkillListPageSize,
    handleSkillListPageChange,
    skillAssets,
    filteredSkillTree,
    filteredInstalledSkillTree,
    filteredStructuredItems,
    genericColumns,
    skillView,
    setSkillView,
    installedSkillSource,
    setInstalledSkillSource,
    marketSkillSource,
    setMarketSkillSource,
    marketCategory,
    setMarketCategory,
    handleEnableBuiltinSkill,
    handleDelete,
    builtinSkillEnableLoading,
    openChangeReview,
    isReviewMode,
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
    clearSelectedBackendSuggestions,
    backendSuggestionSubmitting,
    selectedBackendSuggestionIds,
    isBackendSuggestionSelectable,
    setBackendSuggestionSelected,
    submitBackendSuggestionDecision,
    backendDraftDiffLines,
    backendDraftPreview,
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
    <div
      className={`admin-page memory-page ${isReviewMode ? "is-review-mode" : ""}${embeddedTab ? " is-embedded" : ""}`}
    >
      <MemoryManagementContext.Provider
        value={{ ...outletContext, embedded: Boolean(embeddedTab) }}
      >
        {embeddedTab ? (
          <MemoryManagementListPage />
        ) : (
          <Outlet context={outletContext} />
        )}
      </MemoryManagementContext.Provider>

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
        glossarySaving={glossarySaving}
        skillSaving={skillSaving}
        isReadOnly={isReadOnly}
        draft={draft}
        setDraft={setDraft}
        pendingGlossaryMergeSourceIds={pendingGlossaryMergeSourceIds}
        modalMode={modalMode}
        tagOptions={tagOptions}
        normalizeTagValues={normalizeTagValues}
        handleImportSkillPackage={handleImportSkillPackage}
        pendingSkillPackageFile={pendingSkillPackageFile}
        pendingSkillSourceUrl={pendingSkillSourceUrl}
      />

      <input
        ref={skillZipInputRef}
        type="file"
        accept=".zip"
        hidden
        onChange={handleSkillZipFileSelected}
      />

      <Modal
        open={skillUrlImportOpen}
        title={t("admin.memorySkillCreateImportTitle")}
        okText={t("admin.memorySkillImportApply")}
        cancelText={t("common.cancel")}
        destroyOnClose
        onOk={handleConfirmSkillUrlImport}
        onCancel={() => setSkillUrlImportOpen(false)}
      >
        <Input
          value={skillUrlImportDraft}
          placeholder={t("admin.memorySkillUploadRepoPlaceholder")}
          onChange={(event) => setSkillUrlImportDraft(event.target.value)}
          onPressEnter={handleConfirmSkillUrlImport}
        />
      </Modal>

      {!hideUserGroupSurfaces && (
        <>
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
        </>
      )}

    </div>
  );
}
