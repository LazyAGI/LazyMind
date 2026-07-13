import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { message } from "antd";
import { InfoCircleOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import PluginInstalledView from "./PluginInstalledView";
import { AgentAppsAuth } from "@/components/auth";
import { getLocalizedErrorMessage } from "@/components/request";
import { isAdminRole } from "@/modules/dataSource/utils/role";
import { useMemoryManagementOutletContext } from "../../context";
import type { StructuredAsset } from "../../shared";
import type { MarketSkillAsset } from "./skillMarketMockData";
import {
  getSkillMarketItem,
  installSkillFromMarket,
  listBuiltinSkills,
  listSkillMarketPage,
} from "../../skillApi";
import SkillAdminPublishModal from "./SkillAdminPublishModal";
import SkillInstalledView from "./SkillInstalledView";
import SkillManagementToolbar from "./SkillManagementToolbar";
import SkillMarketView from "./SkillMarketView";
import { collectMarketCategories } from "./skillHelpers";
import { mapMarketSkillRecordToAsset } from "./skillMarketMockData";
import NewPluginModal from "@/modules/plugin/components/NewPluginModal";
import "./index.scss";

export default function SkillManagementSection() {
  const listContentRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const [newPluginOpen, setNewPluginOpen] = useState(false);
  const [memoryTableBodyHeight, setMemoryTableBodyHeight] = useState<number>();
  const [marketKeyword, setMarketKeyword] = useState("");
  const [adminPublishOpen, setAdminPublishOpen] = useState(false);
  const [marketCatalogAssets, setMarketCatalogAssets] = useState<MarketSkillAsset[]>([]);
  const [marketCatalogLoading, setMarketCatalogLoading] = useState(false);
  const [marketInstallingId, setMarketInstallingId] = useState<string>();

  const {
    t,
    openSkillShareCenter,
    incomingPendingCount,
    openSkillCreateModal,
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

  const loadMarketCatalog = useCallback(async () => {
    setMarketCatalogLoading(true);
    try {
      const [firstResult, builtinRecords] = await Promise.all([
        listSkillMarketPage({
          page: 1,
          pageSize: 100,
          category: marketCategory,
        }),
        listBuiltinSkills(),
      ]);

      const records = [...builtinRecords, ...firstResult.records];
      const pageSize = Math.max(1, firstResult.pageSize || 100);
      const totalPages = Math.ceil(firstResult.total / pageSize);

      for (let page = 2; page <= totalPages; page += 1) {
        const pageResult = await listSkillMarketPage({
          page,
          pageSize,
          category: marketCategory,
        });
        records.push(...pageResult.records);
      }

      const deduped = new Map<string, MarketSkillAsset>();
      records.forEach((item) => {
        deduped.set(item.marketItemId, mapMarketSkillRecordToAsset(item));
      });
      setMarketCatalogAssets(Array.from(deduped.values()));
    } catch (error) {
      console.error("Load skill plaza catalog failed:", error);
      setMarketCatalogAssets([]);
      message.error(
        getLocalizedErrorMessage(error, t("admin.memorySkillMarketLoadFailed")) ||
          t("admin.memorySkillMarketLoadFailed"),
      );
    } finally {
      setMarketCatalogLoading(false);
    }
  }, [marketCategory, t]);

  useEffect(() => {
    if (skillView !== "market") {
      return;
    }

    void loadMarketCatalog();
  }, [loadMarketCatalog, skillView]);

  useEffect(() => {
    if (skillView !== "installed") {
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
    window.addEventListener("resize", updateTableHeight);

    return () => {
      resizeObserver.disconnect();
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

  const marketCategories = useMemo(
    () => collectMarketCategories(marketSkillAssets),
    [marketSkillAssets],
  );

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


  const handleMarketReset = () => {
    setMarketKeyword("");
    setMarketSkillSource("all");
    setMarketCategory("all");
  };

  const handleSkillMessageCenter = () => {
    openSkillShareCenter("incoming");
  };

  const handleMarketInstall = (item: StructuredAsset) => {
    if ((item as MarketSkillAsset).marketSource === "builtin") {
      void _handleEnableBuiltinSkill(item);
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
        message.error(
          getLocalizedErrorMessage(error, t("admin.memoryBuiltinSkillEnableFailed")) ||
            t("admin.memoryBuiltinSkillEnableFailed"),
        );
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
        onSkillViewChange={setSkillView}
        installedCount={skillListTotal}
        onCreateSkill={openSkillCreateModal}
        manualSkillReviewCount={manualSkillReviewCount}
        manualSkillReviewLoading={manualSkillReviewLoading}
        manualSkillReviewRunning={manualSkillReviewButtonBusy}
        onSkillReviewClick={handleSkillReviewClick}
        messageCenterCount={messageCenterCount}
        onMessageCenterClick={handleSkillMessageCenter}
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
            source={marketSkillSource}
            onSourceChange={setMarketSkillSource}
            category={marketCategory}
            onCategoryChange={setMarketCategory}
            categories={marketCategories}
            onReset={handleMarketReset}
            onInstall={handleMarketInstall}
            onDetail={handleMarketDetail}
            installingUid={installingUid}
          />
        </div>
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
        <PluginInstalledView t={t} onNewPlugin={() => setNewPluginOpen(true)} />
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
