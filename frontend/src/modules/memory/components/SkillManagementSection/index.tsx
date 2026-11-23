import { getLocalizedErrorMessage } from "@/components/request";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Input, message, Modal, Popover, Segmented, Select, Space, Tooltip } from "antd";
import SkillModuleState from "./SkillModuleState";
import { SearchOutlined } from "@ant-design/icons";
import { Link, useNavigate } from "react-router-dom";
import WorkflowInstalledView from "./WorkflowInstalledView";
import { AgentAppsAuth } from "@/components/auth";
import { isAdminRole } from "@/modules/dataSource/utils/role";
import { useMemoryManagementOutletContext } from "../../context";
import type { SkillViewMode, StructuredAsset } from "../../shared";
import type { MarketSkillAsset } from "./skillMarketMockData";
import {
  cancelSkillOrganizeTask,
  deleteSkillMarketItem,
  getRunningSkillOrganizeTask,
  getSkillMarketItem,
  installSkillFromMarket,
  listBuiltinSkills,
  listSkillMarketTags,
  organizeSkills,
  type SkillOrganizeDepth,
  type SkillOrganizeTaskStatus,
  isSkillOrganizeTerminalStatus,
  waitForSkillOrganize,
} from "../../skillApi";
import SkillAdminPublishModal from "./SkillAdminPublishModal";
import SkillInstalledView from "./SkillInstalledView";
import SkillManagementNavigation from "./SkillManagementNavigation";
import SkillDraftReviewPanel from "./SkillDraftReviewPanel";
import SkillOrganizeApprovalPanel from "./SkillOrganizeApprovalPanel";
import { listPendingSkillDrafts } from "./skillDraftReview";
import { updateSelectedSkillCallModes } from "./skillBatchCallMode";
import type { SkillCallMode } from "../../skillApi";
import SkillManagementToolbar, {
  type SkillOrganizeStatus,
} from "./SkillManagementToolbar";
import SkillMarketView from "./SkillMarketView";
import SkillMarketCategoryFilter, { SkillMarketCategorySummary } from "./SkillMarketCategoryFilter";
import { getMarketPage, type MarketCategoryFilter } from "./skillMarketCategories";
import { loadAdminMarketCatalog } from "./skillMarketCatalog";
import ExpandableTitleSearch from "./ExpandableTitleSearch";
import {
  collectMarketTags,
  filterMarketSkills,
} from "./skillHelpers";
import { mapMarketSkillRecordToAsset } from "./skillMarketMockData";
import NewWorkflowModal from "@/modules/workflow/components/NewWorkflowModal";
import { shouldShowSkillMessageCenter } from "./collaborationVisibility";
import {
  canSubmitSkillOrganize,
  isSkillOrganizeEligible,
  MAX_SKILL_ORGANIZE_SELECTION,
} from "./skillOrganizeRules";
import { skillOrganizeErrorDetails, skillOrganizeErrorText } from "../../skillOrganizeCopy";
import "./index.scss";

const DEFAULT_MARKET_PAGE_SIZE = 12;

function describeSkillOrganizeError(
  error: unknown,
  t: (key: string) => string,
): string {
  const detail = skillOrganizeErrorDetails(error);
  return skillOrganizeErrorText(detail.code, detail.message, t);
}

export default function SkillManagementSection() {
  const listContentRef = useRef<HTMLDivElement>(null);
  const marketRequestIdRef = useRef(0);
  const organizePollingControllerRef = useRef<AbortController | null>(null);
  const organizeRequestIdRef = useRef("");
  const organizeCancellingRef = useRef(false);
  const cancelledSkillRefreshRef = useRef<AbortController | null>(null);
  const organizeGenerationRef = useRef(0);
  const navigate = useNavigate();
  const [newWorkflowOpen, setNewWorkflowOpen] = useState(false);
  const [workflowSearchInput, setWorkflowSearchInput] = useState("");
  const [workflowQuery, setWorkflowQuery] = useState("");
  const [selectedSkills, setSelectedSkills] = useState<Map<string, StructuredAsset>>(new Map());
  const [batchCallModeLoading, setBatchCallModeLoading] = useState(false);
  const batchCallModeLock = useRef(false);
  const [draftReviewOpen, setDraftReviewOpen] = useState(false);
  const [reviewKind, setReviewKind] = useState<"organize" | "drafts">("drafts");
  const [pendingOrganizeCount, setPendingOrganizeCount] = useState(0);
  const [organizeApplying, setOrganizeApplying] = useState(false);
  const [organizeApprovalToken, setOrganizeApprovalToken] = useState(0);
  const [draftApplying, setDraftApplying] = useState(false);
  const [pendingDraftCount, setPendingDraftCount] = useState(0);
  const [organizeMode, setOrganizeMode] = useState(false);
  const [organizeDepth, setOrganizeDepth] = useState<SkillOrganizeDepth>("light");
  const organizeCategoryBeforeRef = useRef<string | undefined>(undefined);
  const organizeLockedInternalRef = useRef(false);
  const [organizeSubmitting, setOrganizeSubmitting] = useState(false);
  const [organizeStatus, setOrganizeStatus] = useState<SkillOrganizeStatus>("idle");
  const [organizeRunStatus, setOrganizeRunStatus] = useState<SkillOrganizeTaskStatus | "">("");
  const [organizeElapsedMs, setOrganizeElapsedMs] = useState(0);
  const [organizeError, setOrganizeError] = useState("");
  const [organizeErrorCode, setOrganizeErrorCode] = useState("");
  const [organizeBlockingSkills, setOrganizeBlockingSkills] = useState<string[]>([]);
  const [organizeDraftConflict, setOrganizeDraftConflict] = useState(false);
  const [organizePendingReviewWarning, setOrganizePendingReviewWarning] = useState(false);
  const organizeStartedAtRef = useRef<number | null>(null);
  const [selectedOrganizeSkills, setSelectedOrganizeSkills] = useState<
    Map<string, StructuredAsset>
  >(new Map());
  const [marketKeyword, setMarketKeyword] = useState("");
  const [marketSearchExpanded, setMarketSearchExpanded] = useState(false);
  const [debouncedMarketKeyword, setDebouncedMarketKeyword] = useState("");
  const [adminPublishOpen, setAdminPublishOpen] = useState(false);
  const [marketCatalogAssets, setMarketCatalogAssets] = useState<MarketSkillAsset[]>([]);
  const [marketTags, setMarketTags] = useState<string[]>([]);
  // Use a ref for the builtin cache so updating it never triggers useCallback/useEffect rebuilds.
  const marketBuiltinCacheRef = useRef<MarketSkillAsset[]>([]);
  // Keep a state copy only for rendering (installedSkills comparison in SkillMarketView).
  const [marketBuiltinAssets, setMarketBuiltinAssets] = useState<MarketSkillAsset[]>([]);
  const [marketCatalogLoading, setMarketCatalogLoading] = useState(false);
  const [marketListPage, setMarketListPage] = useState(1);
  const [marketListPageSize, setMarketListPageSize] = useState(DEFAULT_MARKET_PAGE_SIZE);
  const [marketColumns, setMarketColumns] = useState(1);
  const [marketMajorCategory, setMarketMajorCategory] = useState<MarketCategoryFilter>("all");
  const [marketInstallingId, setMarketInstallingId] = useState<string>();
  const [marketDeletingId, setMarketDeletingId] = useState<string>();

  const {
    t,
    openSkillShareCenter,
    incomingPendingCount,
    openSkillCreateModal,
    openModal,
    skillAssets,
    skillLoading,
    skillListError,
    refreshSkillAssets,
    refreshSkillMetadata,
    genericColumns,
    cloudSkillLoading,
    cloudSkillError,
    retryCloudSkills,
    skillView,
    setSkillView,
    marketSkillSource,
    setMarketSkillSource,
    marketCategory,
    setMarketCategory,
    category,
    setCategory,
    handleEnableBuiltinSkill: _handleEnableBuiltinSkill,
    builtinSkillEnableLoading,
    searchInput,
    setSearchInput,
    setQuery,
    filteredInstalledSkillTree,
    skillListPage,
    skillListPageSize,
    skillListTotal,
    setSkillListPage,
    setSkillListPageSize,
    manualSkillReviewSummary,
    manualSkillReviewLoading,
    manualSkillReviewRunning,
    handleRunManualSkillReview,
  } = useMemoryManagementOutletContext();

  useEffect(() => {
    if (skillLoading || draftReviewOpen) return;
    let current = true;
    void listPendingSkillDrafts().then((rows) => {
      if (current) setPendingDraftCount(rows.length);
    }).catch(() => {
      // Keep the review entry available; its full page reports load errors and retry.
    });
    return () => { current = false; };
  }, [skillLoading, skillAssets, organizeStatus, draftReviewOpen]);

  useEffect(() => {
    const latest = new Map<string, StructuredAsset>(skillAssets.map((skill: StructuredAsset) => [skill.id, skill]));
    const reconcile = (previous: Map<string, StructuredAsset>, organize: boolean) => {
      const next = new Map(previous);
      let changed = false;
      previous.forEach((skill, id) => {
        const current = latest.get(id) ?? skill;
        if (organize && !isSkillOrganizeEligible(current, organizeDepth)) {
          next.delete(id);
          changed = true;
        } else if (current !== skill) {
          next.set(id, current);
          changed = true;
        }
      });
      return changed ? next : previous;
    };
    setSelectedSkills((previous) => reconcile(previous, false));
    setSelectedOrganizeSkills((previous) => reconcile(previous, true));
  }, [skillAssets, organizeDepth]);

  const refreshSkillAssetsRef = useRef(refreshSkillAssets);
  const skillListPageRef = useRef(skillListPage);

  const refreshCancelledSkillAssets = useCallback(() => {
    cancelledSkillRefreshRef.current?.abort();
    const controller = new AbortController();
    cancelledSkillRefreshRef.current = controller;
    // Release task controls independently of refreshing the cleaned-up drafts.
    void refreshSkillAssetsRef.current({
      page: skillListPageRef.current,
      preserveChangeProposals: true,
      background: true,
      signal: controller.signal,
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) console.error("Refresh cancelled skill drafts failed:", error);
    });
  }, []);

  const isAdmin = isAdminRole(AgentAppsAuth.getUserInfo()?.role);

  useEffect(() => {
    refreshSkillAssetsRef.current = refreshSkillAssets;
  }, [refreshSkillAssets]);

  useEffect(() => {
    skillListPageRef.current = skillListPage;
  }, [skillListPage]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedMarketKeyword(marketKeyword.trim());
    }, 300);
    return () => window.clearTimeout(timer);
  }, [marketKeyword]);

  useEffect(
    () => () => {
      organizePollingControllerRef.current?.abort();
      cancelledSkillRefreshRef.current?.abort();
      organizeGenerationRef.current += 1;
    },
    [],
  );

  useEffect(() => {
    setMarketListPage(1);
  }, [debouncedMarketKeyword, marketCategory, marketMajorCategory, marketSkillSource, marketListPageSize, marketColumns]);

  const ensureMarketBuiltins = useCallback(async (forceRefresh = false) => {
    if (!forceRefresh && marketBuiltinCacheRef.current.length) {
      return marketBuiltinCacheRef.current;
    }
    const records = await listBuiltinSkills();
    const assets = records.map(mapMarketSkillRecordToAsset);
    marketBuiltinCacheRef.current = assets;
    setMarketBuiltinAssets(assets);
    return assets;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const includeAdminMarketSkills = marketSkillSource !== "builtin";
  const loadMarketCatalog = useCallback(async (forceRefreshBuiltins = false) => {
    const requestId = ++marketRequestIdRef.current;
    setMarketCatalogLoading(true);
    try {
      const builtins = await ensureMarketBuiltins(forceRefreshBuiltins);
      if (requestId !== marketRequestIdRef.current) {
        return;
      }

      const filteredBuiltins = filterMarketSkills(builtins, {
        keyword: debouncedMarketKeyword,
        tag: marketCategory,
        source: "builtin",
      });
      const adminRecords = includeAdminMarketSkills
        ? await loadAdminMarketCatalog(debouncedMarketKeyword, marketCategory)
        : [];
      if (requestId !== marketRequestIdRef.current) return;
      setMarketCatalogAssets([...filteredBuiltins, ...adminRecords.map(mapMarketSkillRecordToAsset)]);
    } catch (error) {
      if (requestId !== marketRequestIdRef.current) {
        return;
      }
      console.error("Load skill plaza catalog failed:", error);
      setMarketCatalogAssets([]);
      message.error(t("admin.memorySkillMarketLoadFailed"));
    } finally {
      if (requestId === marketRequestIdRef.current) {
        setMarketCatalogLoading(false);
      }
    }
  }, [
    debouncedMarketKeyword,
    ensureMarketBuiltins,
    includeAdminMarketSkills,
    marketCategory,
    t,
  ]);

  const loadMarketTags = useCallback(async () => {
    try {
      setMarketTags(await listSkillMarketTags());
    } catch (error) {
      console.error("Load skill plaza tags failed:", error);
      setMarketTags([]);
    }
  }, []);

  useEffect(() => {
    if (skillView !== "market") {
      return;
    }

    void Promise.all([loadMarketCatalog(), loadMarketTags()]);
  }, [loadMarketCatalog, loadMarketTags, skillView]);

  const matchingMarketAssets = useMemo(() => filterMarketSkills(marketCatalogAssets, {
    keyword: "", tag: "all", source: marketSkillSource,
  }), [marketCatalogAssets, marketSkillSource]);
  const marketPage = useMemo(() => getMarketPage(
    matchingMarketAssets, marketMajorCategory, marketListPage, marketListPageSize, marketColumns,
  ), [matchingMarketAssets, marketMajorCategory, marketListPage, marketListPageSize, marketColumns]);
  const marketSkillAssets = marketPage.items;
  const marketListTotal = marketPage.total;

  useEffect(() => {
    if (marketListPage !== marketPage.page) setMarketListPage(marketPage.page);
  }, [marketListPage, marketPage.page]);

  const installedTableData = filteredInstalledSkillTree;

  const marketFilterTags = useMemo(() => {
    if (marketSkillSource === "builtin") {
      return collectMarketTags(marketBuiltinAssets);
    }
    return marketTags;
  }, [marketBuiltinAssets, marketSkillSource, marketTags]);

  const messageCenterCount = incomingPendingCount;
  const hasPendingOrganizeDrafts = pendingOrganizeCount > 0 || pendingDraftCount > 0 || skillAssets.some((skill: StructuredAsset) => skill.draft?.hasUncommittedDraft);
  const manualSkillReviewCount = manualSkillReviewSummary?.qualifiedSessionCount ?? 0;
  const manualSkillReviewButtonBusy =
    manualSkillReviewRunning ||
    manualSkillReviewSummary?.runningTask?.status === "pending" ||
    manualSkillReviewSummary?.runningTask?.status === "running";
  const manualSkillReviewButtonDisabled =
    manualSkillReviewLoading ||
    manualSkillReviewButtonBusy ||
    organizeMode ||
    organizeSubmitting ||
    manualSkillReviewCount <= 0;
  const manualSkillReviewDisabledReason = organizeSubmitting
    ? t("admin.memorySkillReviewDisabledOrganizeRunning")
    : organizeMode
      ? t("admin.memorySkillReviewDisabledOrganizeSelecting")
      : manualSkillReviewLoading
        ? t("admin.memorySkillReviewDisabledLoading")
        : manualSkillReviewButtonBusy
          ? t("admin.memorySkillReviewDisabledRunning")
          : manualSkillReviewCount <= 0
            ? t("admin.memorySkillReviewDisabledEmpty")
            : undefined;
  const organizeDisabledReason = organizeSubmitting
    ? t("admin.memorySkillOrganizeTaskRunning")
    : manualSkillReviewButtonBusy
      ? t("admin.memorySkillOrganizeDisabledReviewRunning")
      : undefined;

  const tableScroll = { x: 656 };

  const handleSkillSelectionChange = (records: StructuredAsset[], selected: boolean) => {
    setSelectedSkills((previous) => {
      const next = new Map(previous);
      records.forEach((record) => {
        if (!selected) next.delete(record.id);
        else if (!record.cloudResourceId && !record.readonly) next.set(record.id, record);
      });
      return next;
    });
  };

  const handleBatchCallMode = async (mode: SkillCallMode) => {
    if (batchCallModeLock.current || !selectedSkills.size) return;
    batchCallModeLock.current = true;
    setBatchCallModeLoading(true);
    try {
      const result = await updateSelectedSkillCallModes([...selectedSkills.values()], mode);
      setSelectedSkills((previous) => {
        const next = new Map(previous);
        result.succeededIds.forEach((id) => next.delete(id));
        return next;
      });
      if (result.succeededIds.length) {
        message.success(t("admin.memorySkillBatchModeSuccess", { count: result.succeededIds.length }));
      }
      if (result.failed.length) {
        Modal.error({
          title: t("admin.memorySkillBatchModeFailed", { count: result.failed.length }),
          content: <ul>{result.failed.map(({ skill, error }) => <li key={skill.id}>{skill.name}: {getLocalizedErrorMessage(error)}</li>)}</ul>,
        });
      }
      await refreshSkillAssets({ preserveChangeProposals: true });
    } finally {
      batchCallModeLock.current = false;
      setBatchCallModeLoading(false);
    }
  };

  const handleSkillMessageCenter = () => {
    openSkillShareCenter("incoming");
  };

  const restoreOrganizeCategory = () => {
    if (!organizeLockedInternalRef.current) return;
    setCategory(organizeCategoryBeforeRef.current);
    organizeLockedInternalRef.current = false;
  };

  const cancelSkillOrganize = () => {
    restoreOrganizeCategory();
    setOrganizeMode(false);
    setSelectedOrganizeSkills(new Map());
  };

  const startSkillOrganize = (mode: SkillOrganizeDepth) => {
    if (organizeSubmitting) return;
    if (organizeMode && mode === organizeDepth) return;
    organizeGenerationRef.current += 1;

    if (mode === "deep") {
      if (!organizeLockedInternalRef.current) {
        organizeCategoryBeforeRef.current = category;
        organizeLockedInternalRef.current = true;
      }
      setSkillListPage(1);
      if (category !== "internal") {
        setCategory("internal");
      }
    } else if (organizeLockedInternalRef.current) {
      restoreOrganizeCategory();
    }

    if (organizeMode) {
      if (mode === organizeDepth) return;
      const removed = [...selectedOrganizeSkills.values()].filter(
        (skill) => !isSkillOrganizeEligible(skill, mode),
      );
      if (removed.length) {
        const next = new Map(selectedOrganizeSkills);
        removed.forEach((skill) => next.delete(skill.id));
        setSelectedOrganizeSkills(next);
        message.warning(t("admin.memorySkillOrganizeSelectionRemoved", {
          count: removed.length,
          names: removed.map((skill) => skill.name).join("、"),
        }), 8);
      }
    } else {
      setSelectedOrganizeSkills(new Map(
        [...selectedSkills]
          .filter(([, skill]) => isSkillOrganizeEligible(skill, mode))
          .slice(0, MAX_SKILL_ORGANIZE_SELECTION),
      ));
    }

    setOrganizeDepth(mode);
    setOrganizeError("");
    setOrganizeErrorCode("");
    setOrganizeBlockingSkills([]);
    setOrganizeDraftConflict(false);
    setOrganizeStatus("idle");
    setOrganizeMode(true);
    setSelectedSkills(new Map());
  };

  const handleSkillViewChange = (
    nextView: SkillViewMode | "workflows",
  ) => {
    if (draftApplying || batchCallModeLoading) return;
    setDraftReviewOpen(false);
    if (nextView !== "installed") {
      cancelSkillOrganize();
    }
    setSkillView(nextView);
  };

  const handleOrganizeSelectionChange = (
    records: StructuredAsset[],
    selected: boolean,
  ) => {
    const next = new Map(selectedOrganizeSkills);
    if (!selected) {
      records.forEach((record) => next.delete(record.id));
      setSelectedOrganizeSkills(next);
      return;
    }

    const additions = records.filter(
      (record) => isSkillOrganizeEligible(record, organizeDepth) && !next.has(record.id),
    );
    const availableSlots = Math.max(
      0,
      MAX_SKILL_ORGANIZE_SELECTION - next.size,
    );
    additions.slice(0, availableSlots).forEach((record) => {
      next.set(record.id, record);
    });
    setSelectedOrganizeSkills(next);

    if (additions.length > availableSlots) {
      message.warning(t("admin.memorySkillOrganizeLimitWarning"));
    }
  };

  const followSkillOrganize = useCallback(
    async (
      requestId: string,
      pollingController: AbortController,
      seed?: { status?: string; startedAt?: number },
    ) => {
      const isCurrent = () => !pollingController.signal.aborted && organizePollingControllerRef.current === pollingController;
      if (!isCurrent()) return;
      organizeGenerationRef.current += 1;
      cancelledSkillRefreshRef.current?.abort();
      organizeRequestIdRef.current = requestId;
      setOrganizeSubmitting(true);
      setOrganizeStatus("running");
      const startedAt = seed?.startedAt ?? Date.now();
      organizeStartedAtRef.current = startedAt;
      setOrganizeElapsedMs(Math.max(0, Date.now() - startedAt));
      setOrganizeRunStatus(
        seed?.status && !isSkillOrganizeTerminalStatus(seed.status)
          ? seed.status === "running"
            ? "pending"
            : (seed.status as SkillOrganizeTaskStatus)
          : "pending",
      );

      try {
        const task = await waitForSkillOrganize(
          requestId,
          pollingController.signal,
          (progress) => {
            if (!isCurrent()) {
              return;
            }
            if (!isSkillOrganizeTerminalStatus(progress.status)) {
              setOrganizeRunStatus(
                progress.status === "running" ? "pending" : progress.status,
              );
            }
            const origin = organizeStartedAtRef.current ?? startedAt;
            setOrganizeElapsedMs(Math.max(0, Date.now() - origin));
          },
        );
        if (!isCurrent()) return;
        if (task.status === "failed") {
          setOrganizeError(skillOrganizeErrorText(task.errorCode, task.error, t));
          setOrganizeErrorCode(task.errorCode);
          setOrganizeDraftConflict(task.errorCode === "skill_organize_draft_conflict");
          setOrganizeStatus("error");
          return;
        }
        if (task.status === "cancelled" || task.status === "skipped") {
          setOrganizeStatus("skipped");
          if (task.pendingReview) setOrganizePendingReviewWarning(true);
          if (task.status === "cancelled") refreshCancelledSkillAssets();
          return;
        }
        await refreshSkillAssetsRef.current({ page: skillListPageRef.current });
        if (!isCurrent()) return;
        setOrganizeStatus("success");
        setOrganizeApprovalToken((token) => token + 1);
      } catch (error) {
        if (!isCurrent()) {
          return;
        }
        console.error("Skill organize task failed:", error);
        setOrganizeError(describeSkillOrganizeError(error, t));
        setOrganizeErrorCode(skillOrganizeErrorDetails(error).code);
        setOrganizeStatus("error");
      } finally {
        if (isCurrent()) {
          organizePollingControllerRef.current = null;
          organizeRequestIdRef.current = "";
          setOrganizeSubmitting(false);
          setOrganizeRunStatus("");
          setOrganizeElapsedMs(0);
          organizeStartedAtRef.current = null;
        }
      }
    },
    [refreshCancelledSkillAssets],
  );

  useEffect(() => {
    const pollingController = new AbortController();
    organizePollingControllerRef.current = pollingController;

    void (async () => {
      try {
        const task = await getRunningSkillOrganizeTask(
          pollingController.signal,
        );
        if (!task || pollingController.signal.aborted || organizePollingControllerRef.current !== pollingController) {
          if (organizePollingControllerRef.current === pollingController) {
            organizePollingControllerRef.current = null;
          }
          return;
        }
        await followSkillOrganize(task.requestId, pollingController, {
          status: task.status,
          startedAt: Date.parse(task.task?.startedAt || task.task?.createdAt || "") || Date.now(),
        });
      } catch (error) {
        if (pollingController.signal.aborted) {
          return;
        }
        if (organizePollingControllerRef.current === pollingController) {
          organizePollingControllerRef.current = null;
        }
        console.error("Load running skill organize task failed:", error);
      }
    })();

    return () => pollingController.abort();
  }, [followSkillOrganize]);

  const cancelRunningSkillOrganize = async () => {
    const requestId = organizeRequestIdRef.current;
    const pollingController = organizePollingControllerRef.current;
    if (!requestId || !pollingController || organizeCancellingRef.current) return;
    organizeCancellingRef.current = true;
    try {
      const result = await cancelSkillOrganizeTask(requestId);
      if (organizePollingControllerRef.current !== pollingController) return;
      pollingController.abort();
      organizePollingControllerRef.current = null;
      organizeRequestIdRef.current = "";
      setOrganizeStatus("skipped");
      setOrganizeError("");
      setOrganizeErrorCode("");
      if (result?.pendingReview) setOrganizePendingReviewWarning(true);
      setOrganizeSubmitting(false);
      setOrganizeRunStatus("");
      setOrganizeElapsedMs(0);
      organizeStartedAtRef.current = null;
      refreshCancelledSkillAssets();
    } catch (error) {
      if (organizePollingControllerRef.current !== pollingController) return;
      setOrganizeError(describeSkillOrganizeError(error, t));
      setOrganizeErrorCode(skillOrganizeErrorDetails(error).code);
    } finally {
      organizeCancellingRef.current = false;
    }
  };

  const handleOrganizeSubmit = async (mode: SkillOrganizeDepth) => {
    const skills = [...selectedOrganizeSkills.values()];
    if (mode !== organizeDepth || skills.some((skill) => !isSkillOrganizeEligible(skill, organizeDepth))) {
      message.warning(t("admin.memorySkillOrganizeSelectionInvalid"));
      return;
    }
    if (!canSubmitSkillOrganize(skills.length)) {
      message.warning(t("admin.memorySkillOrganizeMinimumWarning"));
      return;
    }
    if (organizeSubmitting) {
      return;
    }

    organizePollingControllerRef.current?.abort();
    cancelledSkillRefreshRef.current?.abort();
    const pollingController = new AbortController();
    organizePollingControllerRef.current = pollingController;
    setOrganizeSubmitting(true);
    setOrganizeStatus("running");
    setOrganizeError("");
    setOrganizeErrorCode("");
    setOrganizeBlockingSkills([]);
    setOrganizeDraftConflict(false);
    // Exit selection mode immediately so the page stays usable while the
    // organize task runs in the background.
    cancelSkillOrganize();

    try {
      const result = await organizeSkills(
        skills.map((skill) => `skills/${skill.category}/${skill.name}`),
        mode,
      );
      if (!result.requestId || !result.taskId) {
        throw new Error("Skill organize task was not accepted");
      }
      if (pollingController.signal.aborted || organizePollingControllerRef.current !== pollingController) {
        return;
      }
      await followSkillOrganize(result.requestId, pollingController);
    } catch (error) {
      if (pollingController.signal.aborted || organizePollingControllerRef.current !== pollingController) {
        return;
      }
      if (organizePollingControllerRef.current === pollingController) {
        organizePollingControllerRef.current = null;
      }
      console.error("Skill organize task failed:", error);
      const detail = skillOrganizeErrorDetails(error);
      setOrganizeBlockingSkills(detail.blockingSkills);
      setOrganizeDraftConflict(detail.code === "skill_organize_draft_conflict");
      setOrganizeError(describeSkillOrganizeError(error, t));
      setOrganizeErrorCode(skillOrganizeErrorDetails(error).code);
      setOrganizeStatus("error");
      setOrganizeSubmitting(false);
    }
  };

  const handleMarketInstall = (item: StructuredAsset) => {
    if ((item as MarketSkillAsset).marketSource === "builtin") {
      void (async () => {
        try {
          await _handleEnableBuiltinSkill(item);
          // Optimistically mark this builtin item as installed so the UI
          // updates immediately without triggering a loading state.
          const updated = marketBuiltinCacheRef.current.map((asset) =>
            asset.id === item.id ? { ...asset, installed: true } : asset,
          );
          marketBuiltinCacheRef.current = updated;
          setMarketBuiltinAssets(updated);
          setMarketCatalogAssets((previous) =>
            previous.map((asset) =>
              asset.id === item.id ? { ...asset, installed: true } : asset,
            ),
          );
        } catch {
          // Error already handled and shown by _handleEnableBuiltinSkill.
        }
      })();
      return;
    }
    const marketItemId = (item as MarketSkillAsset).marketItemId || item.id;
    if (!marketItemId) {
      message.warning(t("admin.memoryBuiltinSkillMissing"));
      return;
    }

    setMarketInstallingId(marketItemId);
    void (async () => {
      try {
        await installSkillFromMarket(marketItemId);
        await refreshSkillAssets({ page: skillListPage });
        await loadMarketCatalog();
        message.success(t("admin.memoryBuiltinSkillEnableSuccess"));
      } catch (error) {
        console.error("Install market skill failed:", error);
      } finally {
        setMarketInstallingId(undefined);
      }
    })();
  };

  const handleSkillReviewClick = () => {
    if (manualSkillReviewButtonDisabled) {
      return;
    }
    void handleRunManualSkillReview();
  };

  const handleMarketDetail = (item: StructuredAsset) => {
    if ((item as MarketSkillAsset).marketSource === "builtin") {
      openModal("view", item, { skipSkillDetailLoad: true });
      return;
    }
    const marketItemId = (item as MarketSkillAsset).marketItemId || item.id;
    void (async () => {
      try {
        const detail = await getSkillMarketItem(marketItemId);
        if (detail) {
          openModal("view", mapMarketSkillRecordToAsset(detail), {
            skipSkillDetailLoad: true,
          });
          return;
        }
      } catch (error) {
        console.error("Load market skill detail failed:", error);
      }
      openModal("view", item, { skipSkillDetailLoad: true });
    })();
  };

  const handleMarketDelete = (item: StructuredAsset) => {
    const marketItem = item as MarketSkillAsset;
    if (!isAdmin || marketItem.marketSource !== "admin" || marketDeletingId) {
      return;
    }
    const marketItemId = marketItem.marketItemId || item.id;
    if (!marketItemId) {
      return;
    }

    Modal.confirm({
      title: t("admin.memorySkillMarketDeleteConfirmTitle"),
      content: t("admin.memorySkillMarketDeleteConfirmContent"),
      okText: t("common.delete"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        setMarketDeletingId(marketItemId);
        try {
          const deleted = await deleteSkillMarketItem(marketItemId);
          if (!deleted) {
            throw new Error("Skill market delete was not confirmed");
          }

          setMarketCatalogAssets((previous) =>
            previous.filter(
              (asset) => (asset.marketItemId || asset.id) !== marketItemId,
            ),
          );

          await loadMarketTags();
          await loadMarketCatalog();
          message.success(t("admin.memorySkillMarketDeleteSuccess"));
        } catch (error) {
          console.error("Delete market skill failed:", error);
          message.error(getLocalizedErrorMessage(error));
          throw error;
        } finally {
          setMarketDeletingId(undefined);
        }
      },
    });
  };

  const installingUid = marketInstallingId || [...builtinSkillEnableLoading][0];
  const marketFilters = (
    <div className="memory-skill-market-toolbar-filters">
      <SkillMarketCategoryFilter
        t={t}
        value={marketMajorCategory}
        groups={marketPage.groups}
        onChange={(value) => { setMarketMajorCategory(value); setMarketListPage(1); }}
        tags={marketFilterTags}
        tag={marketCategory}
        onTagChange={(value) => { setMarketCategory(value); setMarketListPage(1); }}
      />
      <Popover
        trigger="click"
        placement="bottomRight"
        arrow={false}
        overlayClassName="memory-skill-market-search-overlay"
        open={marketSearchExpanded}
        onOpenChange={setMarketSearchExpanded}
        content={
          <Input.Search
            autoFocus
            allowClear
            value={marketKeyword}
            onChange={(event) => setMarketKeyword(event.target.value)}
            onSearch={(value) => {
              const nextKeyword = value.trim();
              setMarketKeyword(value);
              setDebouncedMarketKeyword(nextKeyword);
              setMarketListPage(1);
            }}
            placeholder={t("admin.memorySkillMarketSearchPlaceholder")}
            className="memory-skill-market-search"
          />
        }
      >
        <Tooltip title={t("admin.memorySkillMarketSearchPlaceholder")}>
          <Button
            aria-label={t("admin.memorySkillMarketSearchPlaceholder")}
            aria-expanded={marketSearchExpanded}
            icon={<SearchOutlined />}
          />
        </Tooltip>
      </Popover>
      <Select
        value={marketSkillSource}
        className="memory-skill-market-source"
        options={[
          { value: "all", label: t("admin.memorySkillMarketSourceAll") },
          { value: "builtin", label: t("admin.memorySkillMarketSourceBuiltin") },
          { value: "admin", label: t("admin.memorySkillMarketSourceAdmin") },
        ]}
        onChange={(value) => {
          setMarketSkillSource(value);
          setMarketListPage(1);
        }}
      />
    </div>
  );

  return (
    <div className={`memory-skill-management${skillView === "workflows" ? " is-workflow-view" : ""}`}>
      <SkillManagementNavigation t={t} skillView={skillView} onSkillViewChange={handleSkillViewChange} disabled={draftApplying || organizeApplying || batchCallModeLoading} />
      <div className="memory-skill-workspace-main">
      {draftReviewOpen ? (
        <Segmented
          style={{ flexShrink: 0, alignSelf: "flex-start", marginBottom: 12 }}
          value={reviewKind}
          disabled={draftApplying || organizeApplying}
          onChange={(value) => setReviewKind(value as "organize" | "drafts")}
          options={[
            { value: "organize", label: t("admin.memorySkillOrganizeApprovalTitle") },
            { value: "drafts", label: t("admin.memorySkillDraftReviewTitle") },
          ]}
        />
      ) : null}
      {skillView === "installed" ? (
        <SkillOrganizeApprovalPanel
          t={t}
          hiddenRequestId={organizeRunStatus ? organizeRequestIdRef.current : ""}
          expanded={draftReviewOpen && reviewKind === "organize"}
          refreshToken={organizeApprovalToken}
          onExpandedChange={(open) => { if (!draftApplying) setDraftReviewOpen(open); }}
          onPendingCountChange={setPendingOrganizeCount}
          onApplyingChange={setOrganizeApplying}
          onApplied={() => refreshSkillAssetsRef.current({ preserveChangeProposals: true })}
        />
      ) : null}
      {draftReviewOpen ? (reviewKind === "drafts" ? (
        <SkillDraftReviewPanel
          t={t}
          onClose={() => { if (!organizeApplying) setDraftReviewOpen(false); }}
          onApplied={async () => {
            const generation = organizeGenerationRef.current;
            const clearDraftConflict = organizeStatus === "error" && organizeDraftConflict;
            await refreshSkillAssets({ preserveChangeProposals: true });
            if (!clearDraftConflict || organizeGenerationRef.current !== generation || organizePollingControllerRef.current) return;
            setOrganizeBlockingSkills([]);
            setOrganizeDraftConflict(false);
            setOrganizeError("");
            setOrganizeErrorCode("");
            setOrganizeStatus((status) => status === "error" ? "idle" : status);
          }}
          onPendingCountChange={setPendingDraftCount}
          onApplyingChange={setDraftApplying}
          onOpenSkill={(id) => navigate(`/memory-management/skills/${encodeURIComponent(id)}`)}
        />
      ) : null) : (<>
      <SkillManagementToolbar
        t={t}
        skillView={skillView}
        installedCount={skillListTotal}
        onCreateSkill={openSkillCreateModal}
        organizeMode={organizeMode}
        organizeStatus={organizeStatus}
        organizeRunStatus={organizeRunStatus}
        organizeElapsedMs={organizeElapsedMs}
        organizeError={organizeError}
        organizeDisabledReason={organizeDisabledReason}
        organizeDisabled={
          skillLoading ||
          organizeSubmitting ||
          manualSkillReviewButtonBusy ||
          skillListTotal <= 0
        }
        onOrganizeSkills={startSkillOrganize}
        onOrganizeCancel={cancelSkillOrganize}
        onOrganizeCancelRun={organizeRunStatus ? () => void cancelRunningSkillOrganize() : undefined}
        manualSkillReviewCount={manualSkillReviewCount}
        manualSkillReviewDisabled={manualSkillReviewButtonDisabled}
        manualSkillReviewDisabledReason={manualSkillReviewDisabledReason}
        onSkillReviewClick={handleSkillReviewClick}
        messageCenterCount={messageCenterCount}
        onMessageCenterClick={handleSkillMessageCenter}
        showMessageCenter={shouldShowSkillMessageCenter({
          skillView,
        })}
        isAdmin={isAdmin}
        marketFilters={marketFilters}
        onAdminPublish={() => setAdminPublishOpen(true)}
        onNewWorkflow={() => setNewWorkflowOpen(true)}
        pendingDraftCount={pendingDraftCount + pendingOrganizeCount}
        onReviewDrafts={() => { setReviewKind(pendingOrganizeCount > 0 ? "organize" : "drafts"); setDraftReviewOpen(true); }}
        headingExtra={
          skillView === "installed" ? (
            <ExpandableTitleSearch
              value={searchInput}
              placeholder={t("admin.memorySkillSearchPlaceholder")}
              onChange={setSearchInput}
              onSearch={setQuery}
            />
          ) : skillView === "workflows" ? (
            <ExpandableTitleSearch
              value={workflowSearchInput}
              placeholder={t("admin.memoryWorkflowSearchPlaceholder")}
              onChange={setWorkflowSearchInput}
              onSearch={(value) => {
                setWorkflowSearchInput(value);
                setWorkflowQuery(value);
              }}
            />
          ) : null
        }
      />

      {skillView === "installed" && (organizeError || organizePendingReviewWarning) ? (
        <Alert
          className="memory-skill-organize-notice"
          type="warning"
          showIcon
          message={organizeError || t("admin.memorySkillOrganizeCancelledPendingReview")}
          description={organizeBlockingSkills.length || (organizeError && organizePendingReviewWarning) ? <>
            {organizeError && organizePendingReviewWarning ? <p>{t("admin.memorySkillOrganizeCancelledPendingReview")}</p> : null}
            {organizeBlockingSkills.length ? <ul>{organizeBlockingSkills.map((path) => <li key={path}>{path}</li>)}</ul> : null}
          </> : undefined}
          action={<Space>
            {["skill_organize_model_unavailable", "skill_organize_model_transport"].includes(organizeErrorCode) &&
              <Link to="/settings?section=model_defaults">{t("admin.memorySkillOrganizeConfigureModel")}</Link>}
            {(organizeDraftConflict || hasPendingOrganizeDrafts) &&
              <Button onClick={() => { cancelSkillOrganize(); setReviewKind(!organizeDraftConflict && pendingOrganizeCount > 0 ? "organize" : "drafts"); setDraftReviewOpen(true); }}>{t("admin.memorySkillDraftReviewTitle")}</Button>}
          </Space>}
        />
      ) : null}

      {skillView === "installed" && cloudSkillError ? (
        <SkillModuleState tone="error" retryLabel={t("common.retry")} onRetry={() => void retryCloudSkills()}>
          {t("admin.memoryCloudLoadFailed")}
        </SkillModuleState>
      ) : null}
      {skillView === "installed" && skillListError ? (
        <SkillModuleState tone="error" retryLabel={t("common.retry")} onRetry={() => void refreshSkillAssets()}>
          {t("admin.memoryResourceLocalLoadFailed")}
        </SkillModuleState>
      ) : null}
      {skillView === "installed" && cloudSkillLoading ? (
        <SkillModuleState tone="loading">{t("admin.memoryCloudLoading")}</SkillModuleState>
      ) : null}

      {skillView === "installed" ? (
        <SkillInstalledView
          t={t}
          loading={skillLoading}
          skillAssets={skillAssets}
          dataSource={installedTableData}
          category={category}
          onCategoryChange={setCategory}
          organizeMode={organizeMode}
          organizeDepth={organizeDepth}
          organizeLoading={organizeSubmitting}
          selectedOrganizeSkillIds={[...selectedOrganizeSkills.keys()]}
          onOrganizeSelectionChange={handleOrganizeSelectionChange}
          onOrganizeCancel={cancelSkillOrganize}
          onOrganizeSubmit={handleOrganizeSubmit}
          columns={genericColumns}
          selectedSkillIds={[...selectedSkills.keys()]}
          onSkillSelectionChange={handleSkillSelectionChange}
          onClearSkillSelection={() => setSelectedSkills(new Map())}
          onBatchCallMode={handleBatchCallMode}
          batchCallModeLoading={batchCallModeLoading}
          page={skillListPage}
          pageSize={skillListPageSize}
          total={skillListTotal}
          onPageChange={(nextPage, nextPageSize) => {
            setSkillListPage(nextPage);
            setSkillListPageSize(nextPageSize);
          }}
          tableScroll={tableScroll}
          listContentRef={listContentRef}
        />
      ) : null}

      {skillView === "market" ? (
        <div className="memory-skill-market-panel">
          <SkillMarketCategorySummary t={t} value={marketMajorCategory} total={marketListTotal} />
          <SkillMarketView
            t={t}
            loading={marketCatalogLoading}
            skillAssets={marketSkillAssets}
            installedSkills={skillAssets}
            isAdmin={isAdmin}
            onInstall={handleMarketInstall}
            onDetail={handleMarketDetail}
            onDelete={handleMarketDelete}
            installingUid={installingUid}
            deletingUid={marketDeletingId}
            page={marketPage.page}
            pageSize={marketListPageSize}
            total={marketListTotal}
            pageCount={marketPage.pageCount}
            onColumnsChange={setMarketColumns}
            onPageChange={(nextPage, nextPageSize) => {
              setMarketListPage(nextPage);
              setMarketListPageSize(nextPageSize);
            }}
          />
        </div>
      ) : null}

      <SkillAdminPublishModal
        open={adminPublishOpen}
        t={t}
        onClose={() => setAdminPublishOpen(false)}
        onPublished={async () => {
          refreshSkillMetadata?.();
          await Promise.all([refreshSkillAssets({ page: skillListPage }), loadMarketCatalog(), loadMarketTags()]);
        }}
      />

      {skillView === "workflows" ? (
        <WorkflowInstalledView
          t={t}
          onNewWorkflow={() => setNewWorkflowOpen(true)}
          hideSearch
          searchInput={workflowSearchInput}
          query={workflowQuery}
          onSearchInputChange={setWorkflowSearchInput}
          onSearch={(value) => {
            setWorkflowSearchInput(value);
            setWorkflowQuery(value);
          }}
        />
      ) : null}

      </>)}
      </div>
      <NewWorkflowModal
        open={newWorkflowOpen}
        onCancel={() => setNewWorkflowOpen(false)}
        onCreated={(draftId) => {
          setNewWorkflowOpen(false);
          navigate(`/memory-management/workflows/${draftId}`);
        }}
      />


    </div>
  );
}
