import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button, Input, message, Modal, Select, Tooltip } from "antd";
import { AppstoreOutlined, SearchOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import WorkflowInstalledView from "./WorkflowInstalledView";
import { AgentAppsAuth } from "@/components/auth";
import { isAdminRole } from "@/modules/dataSource/utils/role";
import { useMemoryManagementOutletContext } from "../../context";
import type { SkillViewMode, StructuredAsset } from "../../shared";
import type { MarketSkillAsset } from "./skillMarketMockData";
import {
  deleteSkillMarketItem,
  getSkillMarketItem,
  installSkillFromMarket,
  listBuiltinSkills,
  listSkillMarketPage,
  listSkillMarketTags,
  organizeSkills,
  waitForSkillOrganize,
} from "../../skillApi";
import SkillAdminPublishModal from "./SkillAdminPublishModal";
import SkillInstalledView from "./SkillInstalledView";
import SkillManagementToolbar, {
  type SkillOrganizeStatus,
} from "./SkillManagementToolbar";
import SkillMarketView from "./SkillMarketView";
import {
  collectMarketTags,
  filterMarketSkills,
} from "./skillHelpers";
import { mapMarketSkillRecordToAsset } from "./skillMarketMockData";
import NewWorkflowModal from "@/modules/workflow/components/NewWorkflowModal";
import { shouldShowSkillMessageCenter } from "./collaborationVisibility";
import { renderSkillCategoryIcon } from "./skillCategoryIcon";
import {
  canSubmitSkillOrganize,
  isSkillOrganizeEligible,
  MAX_SKILL_ORGANIZE_SELECTION,
} from "./skillOrganizeRules";
import "./index.scss";

const DEFAULT_MARKET_PAGE_SIZE = 8;
export default function SkillManagementSection() {
  const listContentRef = useRef<HTMLDivElement>(null);
  const marketRequestIdRef = useRef(0);
  const organizePollingControllerRef = useRef<AbortController | null>(null);
  const navigate = useNavigate();
  const [newWorkflowOpen, setNewWorkflowOpen] = useState(false);
  const [organizeMode, setOrganizeMode] = useState(false);
  const [organizeSubmitting, setOrganizeSubmitting] = useState(false);
  const [organizeStatus, setOrganizeStatus] = useState<SkillOrganizeStatus>("idle");
  const [selectedOrganizeSkills, setSelectedOrganizeSkills] = useState<
    Map<string, StructuredAsset>
  >(new Map());
  const [memoryTableBodyHeight, setMemoryTableBodyHeight] = useState<number>();
  const [marketKeyword, setMarketKeyword] = useState("");
  const [marketSearchExpanded, setMarketSearchExpanded] = useState(false);
  const [marketCategoryExpanded, setMarketCategoryExpanded] = useState(false);
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
  const [marketListTotal, setMarketListTotal] = useState(0);
  const [marketInstallingId, setMarketInstallingId] = useState<string>();
  const [marketDeletingId, setMarketDeletingId] = useState<string>();

  const {
    t,
    openSkillShareCenter,
    incomingPendingCount,
    openSkillCreateModal,
    hideUserGroupSurfaces,
    openModal,
    skillAssets,
    skillLoading,
    refreshSkillAssets,
    genericColumns,
    skillView,
    setSkillView,
    marketSkillSource,
    setMarketSkillSource,
    marketCategory,
    setMarketCategory,
    category,
    setCategory,
    availableCategories,
    skillCategoriesLoading,
    handleEnableBuiltinSkill: _handleEnableBuiltinSkill,
    builtinSkillEnableLoading,
    searchInput,
    setSearchInput,
    setQuery,
    resetFilters,
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

  const isAdmin = isAdminRole(AgentAppsAuth.getUserInfo()?.role);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedMarketKeyword(marketKeyword.trim());
    }, 300);
    return () => window.clearTimeout(timer);
  }, [marketKeyword]);

  useEffect(
    () => () => {
      organizePollingControllerRef.current?.abort();
    },
    [],
  );

  useEffect(() => {
    setMarketListPage(1);
  }, [debouncedMarketKeyword, marketCategory, marketSkillSource, marketListPageSize]);

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

  const loadMarketCatalog = useCallback(async (forceRefreshBuiltins = false) => {
    const requestId = ++marketRequestIdRef.current;
    setMarketCatalogLoading(true);
    try {
      const builtins = await ensureMarketBuiltins(forceRefreshBuiltins);
      if (requestId !== marketRequestIdRef.current) {
        return;
      }

      const filteredBuiltins =
        marketSkillSource === "admin"
          ? []
          : filterMarketSkills(builtins, {
              keyword: debouncedMarketKeyword,
              tag: marketCategory,
              source: "builtin",
            });

      if (marketSkillSource === "builtin") {
        const start = (marketListPage - 1) * marketListPageSize;
        setMarketCatalogAssets(
          filteredBuiltins.slice(start, start + marketListPageSize) as MarketSkillAsset[],
        );
        setMarketListTotal(filteredBuiltins.length);
        return;
      }

      const builtinCount = marketSkillSource === "all" ? filteredBuiltins.length : 0;
      const start = (marketListPage - 1) * marketListPageSize;
      const end = start + marketListPageSize;
      const pageItems: MarketSkillAsset[] = [];

      if (builtinCount > 0 && start < builtinCount) {
        pageItems.push(
          ...(filteredBuiltins.slice(start, Math.min(end, builtinCount)) as MarketSkillAsset[]),
        );
      }

      const marketStart = Math.max(0, start - builtinCount);
      const marketEnd = Math.max(0, end - builtinCount);
      const needMarketItems = marketEnd > marketStart;
      const apiPage = needMarketItems
        ? Math.floor(marketStart / marketListPageSize) + 1
        : 1;

      const firstPage = await listSkillMarketPage({
        page: apiPage,
        pageSize: marketListPageSize,
        keyword: debouncedMarketKeyword,
        tags: marketCategory === "all" ? undefined : [marketCategory],
      });
      if (requestId !== marketRequestIdRef.current) {
        return;
      }

      if (needMarketItems) {
        const offsetInPage = marketStart % marketListPageSize;
        const needed = marketEnd - marketStart;
        let records = firstPage.records.slice(offsetInPage, offsetInPage + needed);

        if (
          records.length < needed &&
          firstPage.total > apiPage * marketListPageSize
        ) {
          const secondPage = await listSkillMarketPage({
            page: apiPage + 1,
            pageSize: marketListPageSize,
            keyword: debouncedMarketKeyword,
            tags: marketCategory === "all" ? undefined : [marketCategory],
          });
          if (requestId !== marketRequestIdRef.current) {
            return;
          }
          records = [
            ...records,
            ...secondPage.records.slice(0, needed - records.length),
          ];
        }

        pageItems.push(...records.map(mapMarketSkillRecordToAsset));
      }

      setMarketCatalogAssets(pageItems);
      setMarketListTotal(builtinCount + firstPage.total);
    } catch (error) {
      if (requestId !== marketRequestIdRef.current) {
        return;
      }
      console.error("Load skill plaza catalog failed:", error);
      setMarketCatalogAssets([]);
      setMarketListTotal(0);
    } finally {
      if (requestId === marketRequestIdRef.current) {
        setMarketCatalogLoading(false);
      }
    }
  }, [
    debouncedMarketKeyword,
    ensureMarketBuiltins,
    marketCategory,
    marketListPage,
    marketListPageSize,
    marketSkillSource,
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

  useEffect(() => {
    if (skillView !== "installed" && skillView !== "workflows") {
      return undefined;
    }

    const contentElement = listContentRef.current;
    if (!contentElement) {
      return undefined;
    }

    const updateTableHeight = () => {
      const headerElement =
        contentElement.querySelector<HTMLElement>(".ant-table-thead");
      const paginationElement = contentElement.querySelector<HTMLElement>(
        ".ant-table-pagination",
      );
      const availableHeight =
        contentElement.getBoundingClientRect().height -
        (headerElement?.getBoundingClientRect().height ?? 0) -
        (paginationElement?.getBoundingClientRect().height ?? 0) -
        12;
      const nextBodyHeight = Math.max(240, Math.floor(availableHeight));

      setMemoryTableBodyHeight((previous) =>
        previous === nextBodyHeight ? previous : nextBodyHeight,
      );
    };

    updateTableHeight();
    const resizeObserver = new ResizeObserver(updateTableHeight);
    resizeObserver.observe(contentElement);
    const mutationObserver = new MutationObserver(updateTableHeight);
    mutationObserver.observe(contentElement, { childList: true, subtree: true });
    window.addEventListener("resize", updateTableHeight);

    return () => {
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      window.removeEventListener("resize", updateTableHeight);
    };
  }, [
    skillView,
    skillListPage,
    skillListPageSize,
    skillAssets.length,
    filteredInstalledSkillTree.length,
  ]);

  const marketSkillAssets = marketCatalogAssets;

  const installedTableData = filteredInstalledSkillTree;

  const marketFilterTags = useMemo(() => {
    if (marketSkillSource === "builtin") {
      return collectMarketTags(marketBuiltinAssets);
    }
    return marketTags;
  }, [marketBuiltinAssets, marketSkillSource, marketTags]);

  const messageCenterCount = incomingPendingCount;
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
  const manualSkillReviewDisabledReason = organizeMode || organizeSubmitting
    ? t("admin.memorySkillReviewDisabledOrganizeRunning")
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

  const tableScroll = memoryTableBodyHeight
    ? { x: 1070, y: memoryTableBodyHeight }
    : { x: 1070 };

  const handleInstalledReset = () => {
    resetFilters();
  };

  const handleMarketReset = () => {
    setMarketKeyword("");
    setDebouncedMarketKeyword("");
    setMarketSkillSource("all");
    setMarketCategory("all");
    setMarketListPage(1);
  };

  const handleSkillMessageCenter = () => {
    openSkillShareCenter("incoming");
  };

  const cancelSkillOrganize = () => {
    setOrganizeMode(false);
    setSelectedOrganizeSkills(new Map());
  };

  const handleSkillViewChange = (
    nextView: SkillViewMode | "workflows",
  ) => {
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
      (record) => isSkillOrganizeEligible(record) && !next.has(record.id),
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

  const handleOrganizeSubmit = async () => {
    const skills = [...selectedOrganizeSkills.values()].filter(
      isSkillOrganizeEligible,
    );
    if (!canSubmitSkillOrganize(skills.length)) {
      message.warning(t("admin.memorySkillOrganizeMinimumWarning"));
      return;
    }
    if (organizeSubmitting) {
      return;
    }

    organizePollingControllerRef.current?.abort();
    const pollingController = new AbortController();
    organizePollingControllerRef.current = pollingController;
    setOrganizeSubmitting(true);
    setOrganizeStatus("running");
    // Exit selection mode immediately so the page stays usable while the
    // organize task runs in the background.
    cancelSkillOrganize();

    try {
      const result = await organizeSkills(
        skills.map((skill) => `skills/${skill.category}/${skill.name}`),
      );
      if (!result.requestId || !result.taskId) {
        throw new Error("Skill organize task was not accepted");
      }

      const task = await waitForSkillOrganize(
        result.requestId,
        pollingController.signal,
      );
      if (task.status === "failed") {
        throw new Error("Skill organize task failed");
      }
      if (task.status === "skipped") {
        setOrganizeStatus("skipped");
        return;
      }
      await refreshSkillAssets({ page: skillListPage });
      setOrganizeStatus("success");
    } catch (error) {
      if (pollingController.signal.aborted) {
        return;
      }
      console.error("Skill organize task failed:", error);
      setOrganizeStatus("error");
    } finally {
      if (organizePollingControllerRef.current === pollingController) {
        organizePollingControllerRef.current = null;
      }
      if (!pollingController.signal.aborted) {
        setOrganizeSubmitting(false);
      }
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

          const nextTotal = Math.max(0, marketListTotal - 1);
          const lastPage = Math.max(1, Math.ceil(nextTotal / marketListPageSize));
          setMarketCatalogAssets((previous) =>
            previous.filter(
              (asset) => (asset.marketItemId || asset.id) !== marketItemId,
            ),
          );
          setMarketListTotal(nextTotal);

          await loadMarketTags();
          if (marketListPage > lastPage) {
            setMarketListPage(lastPage);
          } else {
            await loadMarketCatalog();
          }
          message.success(t("admin.memorySkillMarketDeleteSuccess"));
        } catch (error) {
          console.error("Delete market skill failed:", error);
          message.error(t("admin.memorySkillMarketDeleteFailed"));
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
      <div className="memory-skill-market-category-popover">
        <Tooltip title={t("admin.memorySkillMarketTags")}>
          <Button
            aria-label={t("admin.memorySkillMarketTags")}
            aria-expanded={marketCategoryExpanded}
            className={marketCategory !== "all" ? "is-active" : undefined}
            icon={<AppstoreOutlined />}
            onClick={() => setMarketCategoryExpanded((expanded) => !expanded)}
          />
        </Tooltip>
        {marketCategoryExpanded ? (
          <div className="memory-skill-category-bar is-popover">
            <button
              type="button"
              className={`memory-skill-category-pill ${marketCategory === "all" ? "is-active" : ""}`}
              onClick={() => {
                setMarketCategory("all");
                setMarketListPage(1);
                setMarketCategoryExpanded(false);
              }}
            >
              <AppstoreOutlined />
              {t("admin.memorySkillCategoryAll")}
            </button>
            {marketFilterTags.map((item) => (
              <button
                key={item}
                type="button"
                className={`memory-skill-category-pill ${marketCategory === item ? "is-active" : ""}`}
                onClick={() => {
                  setMarketCategory(item);
                  setMarketListPage(1);
                  setMarketCategoryExpanded(false);
                }}
              >
                {renderSkillCategoryIcon(item)}
                {item}
              </button>
            ))}
          </div>
        ) : null}
      </div>
      <div className="memory-skill-market-search-popover">
        <Tooltip title={t("admin.memorySkillMarketSearchPlaceholder")}>
          <Button
            aria-label={t("admin.memorySkillMarketSearchPlaceholder")}
            aria-expanded={marketSearchExpanded}
            icon={<SearchOutlined />}
            onClick={() => setMarketSearchExpanded((expanded) => !expanded)}
          />
        </Tooltip>
        {marketSearchExpanded ? (
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
        ) : null}
      </div>
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
      <Button onClick={handleMarketReset}>{t("admin.memoryReset")}</Button>
    </div>
  );

  return (
    <div className="memory-skill-management">
      <SkillManagementToolbar
        t={t}
        skillView={skillView}
        onSkillViewChange={handleSkillViewChange}
        installedCount={skillListTotal}
        onCreateSkill={openSkillCreateModal}
        organizeMode={organizeMode}
        organizeStatus={organizeStatus}
        organizeDisabledReason={organizeDisabledReason}
        organizeDisabled={
          skillLoading ||
          organizeSubmitting ||
          manualSkillReviewButtonBusy ||
          skillListTotal <= 0
        }
        onOrganizeSkills={() => {
          setSelectedOrganizeSkills(new Map());
          setOrganizeStatus("idle");
          setOrganizeMode(true);
        }}
        manualSkillReviewCount={manualSkillReviewCount}
        manualSkillReviewDisabled={manualSkillReviewButtonDisabled}
        manualSkillReviewDisabledReason={manualSkillReviewDisabledReason}
        onSkillReviewClick={handleSkillReviewClick}
        messageCenterCount={messageCenterCount}
        onMessageCenterClick={handleSkillMessageCenter}
        showMessageCenter={shouldShowSkillMessageCenter({
          skillView,
          hideUserGroupSurfaces,
        })}
        isAdmin={isAdmin}
        marketFilters={marketFilters}
        onAdminPublish={() => setAdminPublishOpen(true)}
        onNewWorkflow={() => setNewWorkflowOpen(true)}
      />

      {skillView === "installed" ? (
        <SkillInstalledView
          t={t}
          loading={skillLoading}
          skillAssets={skillAssets}
          dataSource={installedTableData}
          searchInput={searchInput}
          onSearchInputChange={setSearchInput}
          onSearch={setQuery}
          category={category}
          onCategoryChange={setCategory}
          categories={availableCategories}
          categoriesLoading={skillCategoriesLoading}
          onReset={handleInstalledReset}
          organizeMode={organizeMode}
          organizeLoading={organizeSubmitting}
          selectedOrganizeSkillIds={[...selectedOrganizeSkills.keys()]}
          onOrganizeSelectionChange={handleOrganizeSelectionChange}
          onOrganizeCancel={cancelSkillOrganize}
          onOrganizeSubmit={handleOrganizeSubmit}
          columns={genericColumns}
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
            page={marketListPage}
            pageSize={marketListPageSize}
            total={marketListTotal}
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
          await refreshSkillAssets({ page: skillListPage });
          await Promise.all([loadMarketCatalog(), loadMarketTags()]);
        }}
      />

      {skillView === "workflows" ? (
        <WorkflowInstalledView
          t={t}
          onNewWorkflow={() => setNewWorkflowOpen(true)}
          tableScroll={tableScroll}
          listContentRef={listContentRef}
        />
      ) : null}

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
