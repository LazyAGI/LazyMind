import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { message, Modal } from "antd";
import { InfoCircleOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import PluginInstalledView from "./PluginInstalledView";
import { AgentAppsAuth } from "@/components/auth";
import { localizeErrorCode } from "@/components/request";
import { isAdminRole } from "@/modules/dataSource/utils/role";
import { useMemoryManagementOutletContext } from "../../context";
import type { SkillViewMode, StructuredAsset } from "../../shared";
import type { MarketSkillAsset } from "./skillMarketMockData";
import {
  getSkillMarketItem,
  installSkillFromMarket,
  listBuiltinSkills,
  listSkillMarketPage,
  listTrashedSkillAssetsPage,
  organizeSkills,
  emptySkillTrash,
  purgeSkillAsset,
  restoreSkillAsset,
} from "../../skillApi";
import SkillAdminPublishModal from "./SkillAdminPublishModal";
import SkillInstalledView from "./SkillInstalledView";
import SkillManagementToolbar from "./SkillManagementToolbar";
import SkillMarketView from "./SkillMarketView";
import SkillTrashedView from "./SkillTrashedView";
import {
  collectMarketCategories,
  filterMarketSkills,
  mapSkillAssetRecordToStructuredAsset,
} from "./skillHelpers";
import { mapMarketSkillRecordToAsset } from "./skillMarketMockData";
import NewPluginModal from "@/modules/plugin/components/NewPluginModal";
import { shouldShowSkillMessageCenter } from "./collaborationVisibility";
import "./index.scss";

const DEFAULT_MARKET_PAGE_SIZE = 8;
const MAX_SKILL_ORGANIZE_SELECTION = 20;

export default function SkillManagementSection() {
  const listContentRef = useRef<HTMLDivElement>(null);
  const marketRequestIdRef = useRef(0);
  const navigate = useNavigate();
  const [newPluginOpen, setNewPluginOpen] = useState(false);
  const [organizeMode, setOrganizeMode] = useState(false);
  const [organizeSubmitting, setOrganizeSubmitting] = useState(false);
  const [selectedOrganizeSkills, setSelectedOrganizeSkills] = useState<
    Map<string, StructuredAsset>
  >(new Map());
  const [memoryTableBodyHeight, setMemoryTableBodyHeight] = useState<number>();
  const [marketKeyword, setMarketKeyword] = useState("");
  const [debouncedMarketKeyword, setDebouncedMarketKeyword] = useState("");
  const [adminPublishOpen, setAdminPublishOpen] = useState(false);
  const [marketCatalogAssets, setMarketCatalogAssets] = useState<MarketSkillAsset[]>([]);
  // Use a ref for the builtin cache so updating it never triggers useCallback/useEffect rebuilds.
  const marketBuiltinCacheRef = useRef<MarketSkillAsset[]>([]);
  // Keep a state copy only for rendering (installedSkills comparison in SkillMarketView).
  const [marketBuiltinAssets, setMarketBuiltinAssets] = useState<MarketSkillAsset[]>([]);
  const [marketCatalogLoading, setMarketCatalogLoading] = useState(false);
  const [marketListPage, setMarketListPage] = useState(1);
  const [marketListPageSize, setMarketListPageSize] = useState(DEFAULT_MARKET_PAGE_SIZE);
  const [marketListTotal, setMarketListTotal] = useState(0);
  const [marketInstallingId, setMarketInstallingId] = useState<string>();
  const [trashAssets, setTrashAssets] = useState<StructuredAsset[]>([]);
  const [trashLoading, setTrashLoading] = useState(false);
  const [trashListPage, setTrashListPage] = useState(1);
  const [trashListPageSize, setTrashListPageSize] = useState(12);
  const [trashListTotal, setTrashListTotal] = useState(0);
  const [trashSearchInput, setTrashSearchInput] = useState("");
  const [trashKeyword, setTrashKeyword] = useState("");
  const [trashCategory, setTrashCategory] = useState<string>();
  const [trashActionLoading, setTrashActionLoading] = useState<Set<string>>(new Set());
  const [emptyTrashLoading, setEmptyTrashLoading] = useState(false);

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
    installedSkillSource,
    setInstalledSkillSource,
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
              category: marketCategory,
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
        category: marketCategory,
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
            category: marketCategory,
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

  const loadTrashAssets = useCallback(async () => {
    setTrashLoading(true);
    try {
      const result = await listTrashedSkillAssetsPage({
        keyword: trashKeyword,
        category: trashCategory,
        page: trashListPage,
        pageSize: trashListPageSize,
      });
      setTrashAssets(result.records.map(mapSkillAssetRecordToStructuredAsset));
      setTrashListTotal(result.total);
    } catch (error) {
      console.error("Load trashed skills failed:", error);
      setTrashAssets([]);
      setTrashListTotal(0);
    } finally {
      setTrashLoading(false);
    }
  }, [
    t,
    trashCategory,
    trashKeyword,
    trashListPage,
    trashListPageSize,
  ]);

  useEffect(() => {
    if (skillView !== "market") {
      return;
    }

    void loadMarketCatalog();
  }, [loadMarketCatalog, skillView]);

  useEffect(() => {
    if (skillView !== "trash") {
      return;
    }
    void loadTrashAssets();
  }, [loadTrashAssets, skillView]);

  useEffect(() => {
    void (async () => {
      try {
        const result = await listTrashedSkillAssetsPage({ page: 1, pageSize: 1 });
        setTrashListTotal(result.total);
      } catch {
        // Ignore badge refresh errors; trash tab load will surface them.
      }
    })();
  }, [skillAssets.length, skillListTotal]);

  useEffect(() => {
    if (
      skillView !== "installed" &&
      skillView !== "plugins" &&
      skillView !== "trash"
    ) {
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
    trashListPage,
    trashListPageSize,
    trashAssets.length,
  ]);

  const marketSkillAssets = marketCatalogAssets;

  const installedTableData = filteredInstalledSkillTree;

  const marketCategories = useMemo(() => {
    const fromCatalog = collectMarketCategories([
      ...marketBuiltinAssets,
      ...marketSkillAssets,
    ]);
    return [
      ...new Set([...fromCatalog, ...availableCategories].filter(Boolean)),
    ].sort((left, right) => left.localeCompare(right, "zh-CN"));
  }, [availableCategories, marketBuiltinAssets, marketSkillAssets]);

  const messageCenterCount = incomingPendingCount;
  const manualSkillReviewCount = manualSkillReviewSummary?.qualifiedSessionCount ?? 0;
  const manualSkillReviewButtonBusy =
    manualSkillReviewRunning ||
    manualSkillReviewSummary?.runningTask?.status === "pending" ||
    manualSkillReviewSummary?.runningTask?.status === "running";
  const manualSkillReviewButtonDisabled =
    manualSkillReviewLoading ||
    manualSkillReviewButtonBusy ||
    manualSkillReviewCount <= 0;

  const tableScroll = memoryTableBodyHeight
    ? { x: 1070, y: memoryTableBodyHeight }
    : { x: 1070 };

  const handleInstalledReset = () => {
    setInstalledSkillSource("all");
    resetFilters();
  };

  const handleTrashReset = () => {
    setTrashSearchInput("");
    setTrashKeyword("");
    setTrashCategory(undefined);
    setTrashListPage(1);
  };

  const runTrashAction = async (
    actionKey: string,
    action: () => Promise<void>,
    successMessage: string,
  ) => {
    setTrashActionLoading((previous) => new Set(previous).add(actionKey));
    try {
      await action();
      await Promise.all([
        loadTrashAssets(),
        refreshSkillAssets({ page: skillListPage }),
      ]);
      message.success(successMessage);
    } catch (error) {
      console.error("Skill trash action failed:", error);
      if (!(error as { isAxiosError?: boolean })?.isAxiosError) {
        message.error(localizeErrorCode("2000509"));
      }
    } finally {
      setTrashActionLoading((previous) => {
        const next = new Set(previous);
        next.delete(actionKey);
        return next;
      });
    }
  };

  const handleRestoreTrashedSkill = (item: StructuredAsset) => {
    void runTrashAction(
      `restore:${item.id}`,
      async () => {
        const restored = await restoreSkillAsset(item.id);
        if (!restored) {
          throw new Error("restore failed");
        }
      },
      t("admin.memorySkillTrashRestoreSuccess"),
    );
  };

  const handlePurgeTrashedSkill = (item: StructuredAsset) => {
    Modal.confirm({
      title: t("admin.memorySkillTrashPurgeConfirmTitle"),
      content: t("admin.memorySkillTrashPurgeConfirmContent", { name: item.name }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        await runTrashAction(
          `purge:${item.id}`,
          async () => {
            const purged = await purgeSkillAsset(item.id);
            if (!purged) {
              throw new Error("purge failed");
            }
          },
          t("admin.memorySkillTrashPurgeSuccess"),
        );
      },
    });
  };

  const handleEmptyTrash = async () => {
    setEmptyTrashLoading(true);
    try {
      const purged = await emptySkillTrash();
      await Promise.all([
        loadTrashAssets(),
        refreshSkillAssets({ page: skillListPage }),
      ]);
      message.success(
        t("admin.memorySkillTrashEmptySuccess", { count: purged }),
      );
    } catch (error) {
      console.error("Empty skill trash failed:", error);
    } finally {
      setEmptyTrashLoading(false);
    }
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
    nextView: SkillViewMode | "plugins",
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

    const additions = records.filter((record) => !next.has(record.id));
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

  const handleOrganizeSubmit = () => {
    const skills = [...selectedOrganizeSkills.values()];
    if (skills.length === 0 || organizeSubmitting) {
      return;
    }

    Modal.confirm({
      title: t("admin.memorySkillOrganizeConfirmTitle", {
        count: skills.length,
      }),
      content: t("admin.memorySkillOrganizeConfirmContent"),
      okText: t("admin.memorySkillOrganizeConfirmSubmit"),
      cancelText: t("common.cancel"),
      onOk: async () => {
        setOrganizeSubmitting(true);
        try {
          const result = await organizeSkills(
            skills.map((skill) => skill.id),
          );
          if (!result.taskId || result.status !== "running") {
            throw new Error("Skill organize task was not accepted");
          }
          message.success(
            t("admin.memorySkillOrganizeSuccess", { count: skills.length }),
          );
          cancelSkillOrganize();
        } catch (error) {
          console.error("Submit skill organize task failed:", error);
          const reasonCode = String(
            (error as { response?: { data?: { data?: { code?: unknown } } } })
              ?.response?.data?.data?.code ?? "",
          );
          if (reasonCode === "skill_organize_draft_conflict") {
            message.error(t("admin.memorySkillOrganizeDraftConflict"));
          } else if (reasonCode === "skill_maintenance_task_running") {
            message.error(t("admin.memorySkillOrganizeTaskRunning"));
          } else {
            message.error(t("admin.memorySkillOrganizeFailed"));
          }
        } finally {
          setOrganizeSubmitting(false);
        }
      },
    });
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

  const installingUid = marketInstallingId || [...builtinSkillEnableLoading][0];

  return (
    <div className="memory-skill-management">
      <SkillManagementToolbar
        t={t}
        skillView={skillView}
        onSkillViewChange={handleSkillViewChange}
        installedCount={skillListTotal}
        trashCount={trashListTotal}
        onCreateSkill={openSkillCreateModal}
        organizeMode={organizeMode}
        organizeDisabled={
          skillLoading || manualSkillReviewButtonBusy || skillListTotal <= 0
        }
        onOrganizeSkills={() => {
          setSelectedOrganizeSkills(new Map());
          setOrganizeMode(true);
        }}
        manualSkillReviewCount={manualSkillReviewCount}
        manualSkillReviewLoading={manualSkillReviewLoading}
        manualSkillReviewRunning={manualSkillReviewButtonBusy}
        onSkillReviewClick={handleSkillReviewClick}
        messageCenterCount={messageCenterCount}
        onMessageCenterClick={handleSkillMessageCenter}
        showMessageCenter={shouldShowSkillMessageCenter({
          skillView,
          hideUserGroupSurfaces,
        })}
        isAdmin={isAdmin}
        onAdminPublish={() => setAdminPublishOpen(true)}
        onNewPlugin={() => setNewPluginOpen(true)}
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
          source={installedSkillSource}
          onSourceChange={setInstalledSkillSource}
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
          <div className="memory-skill-view-market-desc">
            <span className="memory-skill-view-market-desc__icon" aria-hidden="true">
              <InfoCircleOutlined />
            </span>
            <p className="memory-skill-view-market-desc__text">
              {t("admin.memorySkillViewMarketHelp")}
            </p>
          </div>
          <SkillMarketView
            t={t}
            loading={marketCatalogLoading}
            skillAssets={marketSkillAssets}
            installedSkills={skillAssets}
            keyword={marketKeyword}
            onKeywordChange={setMarketKeyword}
            onSearch={(value) => {
              const nextKeyword = value.trim();
              setMarketKeyword(value);
              setDebouncedMarketKeyword(nextKeyword);
              setMarketListPage(1);
            }}
            source={marketSkillSource}
            onSourceChange={(value) => {
              setMarketSkillSource(value);
              setMarketListPage(1);
            }}
            category={marketCategory}
            onCategoryChange={(value) => {
              setMarketCategory(value);
              setMarketListPage(1);
            }}
            categories={marketCategories}
            onReset={handleMarketReset}
            onInstall={handleMarketInstall}
            onDetail={handleMarketDetail}
            installingUid={installingUid}
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

      {skillView === "trash" ? (
        <SkillTrashedView
          t={t}
          loading={trashLoading}
          dataSource={trashAssets}
          searchInput={trashSearchInput}
          onSearchInputChange={setTrashSearchInput}
          onSearch={(value) => {
            setTrashKeyword(value.trim());
            setTrashListPage(1);
          }}
          category={trashCategory}
          onCategoryChange={(value) => {
            setTrashCategory(value);
            setTrashListPage(1);
          }}
          categories={availableCategories}
          categoriesLoading={skillCategoriesLoading}
          onReset={handleTrashReset}
          page={trashListPage}
          pageSize={trashListPageSize}
          total={trashListTotal}
          onPageChange={(nextPage, nextPageSize) => {
            setTrashListPage(nextPage);
            setTrashListPageSize(nextPageSize);
          }}
          actionLoading={trashActionLoading}
          emptyTrashLoading={emptyTrashLoading}
          onRestore={handleRestoreTrashedSkill}
          onPurge={handlePurgeTrashedSkill}
          onEmptyTrash={handleEmptyTrash}
          tableScroll={tableScroll}
          listContentRef={listContentRef}
        />
      ) : null}

      <SkillAdminPublishModal
        open={adminPublishOpen}
        t={t}
        onClose={() => setAdminPublishOpen(false)}
        onPublished={async () => {
          await refreshSkillAssets({ page: skillListPage });
          await loadMarketCatalog();
        }}
      />

      {skillView === "plugins" ? (
        <PluginInstalledView
          t={t}
          onNewPlugin={() => setNewPluginOpen(true)}
          tableScroll={tableScroll}
          listContentRef={listContentRef}
        />
      ) : null}

      <NewPluginModal
        open={newPluginOpen}
        onCancel={() => setNewPluginOpen(false)}
        onCreated={(draftId) => {
          setNewPluginOpen(false);
          navigate(`/memory-management/plugins/${draftId}`);
        }}
      />
    </div>
  );
}
