import { useEffect, useMemo, useRef, useState } from "react";
import { Button, Tooltip, message } from "antd";
import { PlusOutlined, QuestionCircleOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import PluginInstalledView from "./PluginInstalledView";
import { AgentAppsAuth } from "@/components/auth";
import { isAdminRole } from "@/modules/dataSource/utils/role";
import { useMemoryManagementOutletContext } from "../../context";
import type { StructuredAsset } from "../../shared";
import { isSkillUpdatePendingForRecord } from "../../shared";
import { removeSkillAsset, listSkillAssetsPage } from "../../skillApi";
import SkillAdminPublishModal from "./SkillAdminPublishModal";
import SkillInstalledView from "./SkillInstalledView";
import SkillMarketView from "./SkillMarketView";
import SkillUploadView from "./SkillUploadView";
import {
  collectMarketCategories,
  mapSkillAssetRecordToStructuredAsset,
} from "./skillHelpers";
import {
  createInstalledSkillFromMock,
  isMockMarketSkill,
  resolveMarketSkillAssets,
} from "./skillMarketMockData";
import NewPluginModal from "@/modules/plugin/components/NewPluginModal";
import "./index.scss";

export default function SkillManagementSection() {
  const listContentRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const [newPluginOpen, setNewPluginOpen] = useState(false);
  const [memoryTableBodyHeight, setMemoryTableBodyHeight] = useState<number>();
  const [marketKeyword, setMarketKeyword] = useState("");
  const [adminPublishOpen, setAdminPublishOpen] = useState(false);
  const [mockInstalledUids, setMockInstalledUids] = useState<Set<string>>(
    new Set(),
  );
  const [mockInstalledSkills, setMockInstalledSkills] = useState<
    StructuredAsset[]
  >([]);
  const [marketCatalogAssets, setMarketCatalogAssets] = useState<
    StructuredAsset[]
  >([]);
  const [marketCatalogLoading, setMarketCatalogLoading] = useState(false);

  const {
    t,
    openSkillShareCenter,
    incomingPendingCount,
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
    handleEnableBuiltinSkill,
    builtinSkillEnableLoading,
    openChangeReview,
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
  } = useMemoryManagementOutletContext();

  const isAdmin = isAdminRole(AgentAppsAuth.getUserInfo()?.role);

  useEffect(() => {
    if (skillView !== "market") {
      return;
    }

    let ignore = false;
    setMarketCatalogLoading(true);

    void (async () => {
      try {
        const firstResult = await listSkillAssetsPage({
          page: 1,
          pageSize: 100,
        });
        if (ignore) {
          return;
        }

        const records = [...firstResult.records];
        const pageSize = Math.max(1, firstResult.pageSize || 100);
        const totalPages = Math.ceil(firstResult.total / pageSize);

        for (let page = 2; page <= totalPages; page += 1) {
          const pageResult = await listSkillAssetsPage({ page, pageSize });
          if (ignore) {
            return;
          }
          records.push(...pageResult.records);
        }

        const deduped = new Map<string, StructuredAsset>();
        records.forEach((item) => {
          deduped.set(item.id, mapSkillAssetRecordToStructuredAsset(item));
        });
        setMarketCatalogAssets(Array.from(deduped.values()));
      } catch (error) {
        if (!ignore) {
          console.error("Load skill market catalog failed:", error);
          setMarketCatalogAssets([]);
        }
      } finally {
        if (!ignore) {
          setMarketCatalogLoading(false);
        }
      }
    })();

    return () => {
      ignore = true;
    };
  }, [skillView]);

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

  const marketSkillAssets = useMemo(
    () => resolveMarketSkillAssets(marketCatalogAssets),
    [marketCatalogAssets],
  );

  const usingMockMarketData = useMemo(
    () =>
      !marketCatalogAssets.some(
        (item) => item.isBuiltinTemplate && !item.parentId,
      ),
    [marketCatalogAssets],
  );

  const installedTableData = useMemo(() => {
    if (skillListPage !== 1 || !mockInstalledSkills.length) {
      return filteredInstalledSkillTree;
    }

    return [
      ...mockInstalledSkills.map((item) => ({ ...item, children: undefined })),
      ...filteredInstalledSkillTree,
    ];
  }, [filteredInstalledSkillTree, mockInstalledSkills, skillListPage]);

  const installedTableTotal = useMemo(() => {
    const mockCount = skillListPage === 1 ? mockInstalledSkills.length : 0;
    if (installedSkillSource === "all") {
      return skillListTotal + mockCount;
    }
    return filteredInstalledSkillTree.length + mockCount;
  }, [
    filteredInstalledSkillTree.length,
    installedSkillSource,
    mockInstalledSkills.length,
    skillListPage,
    skillListTotal,
  ]);

  const marketCategories = useMemo(
    () =>
      collectMarketCategories(
        marketSkillAssets.filter(
          (item) => item.isBuiltinTemplate && !item.parentId,
        ),
      ),
    [marketSkillAssets],
  );

  const installedUpdateCount = useMemo(
    () =>
      skillAssets.filter(
        (item) =>
          !item.isBuiltinTemplate &&
          !item.parentId &&
          isSkillUpdatePendingForRecord(item),
      ).length,
    [skillAssets],
  );

  const messageCenterCount = installedUpdateCount + incomingPendingCount;

  const tableScroll = memoryTableBodyHeight
    ? { x: 980, y: memoryTableBodyHeight }
    : { x: 980 };

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
    if (incomingPendingCount > 0) {
      openSkillShareCenter("incoming");
      return;
    }

    const firstUpdatedSkill = skillAssets.find(
      (item) =>
        !item.isBuiltinTemplate &&
        !item.parentId &&
        isSkillUpdatePendingForRecord(item),
    );

    if (firstUpdatedSkill) {
      void openChangeReview(
        "skills",
        firstUpdatedSkill.id,
        firstUpdatedSkill.updateStatus,
      );
      return;
    }

    Modal.info({
      title: t("admin.memorySkillMessageCenterEmptyTitle"),
      content: t("admin.memorySkillMessageCenterEmptyDesc"),
    });
  };

  const handleMarketInstall = (item: StructuredAsset) => {
    if (isMockMarketSkill(item)) {
      const uid = item.builtinSkillUid || item.id;
      if (mockInstalledUids.has(uid)) {
        return;
      }

      setMockInstalledUids((previous) => new Set(previous).add(uid));
      setMockInstalledSkills((previous) => [
        ...previous,
        createInstalledSkillFromMock(item),
      ]);
      message.success(
        usingMockMarketData
          ? t("admin.memorySkillMarketMockInstallSuccess", { name: item.name })
          : t("admin.memoryBuiltinSkillEnableSuccess"),
      );
      return;
    }

    void handleEnableBuiltinSkill(item);
  };

  const handleMarketUninstall = (item: StructuredAsset) => {
    if (isMockMarketSkill(item)) {
      const uid = item.builtinSkillUid || item.id;
      if (!mockInstalledUids.has(uid)) {
        message.info(t("admin.memorySkillMarketNotInstalled"));
        return;
      }

      Modal.confirm({
        title: t("admin.memorySkillMarketUninstallTitle"),
        content: t("admin.memorySkillMarketUninstallContent", {
          name: item.name,
        }),
        okText: t("common.confirm"),
        cancelText: t("common.cancel"),
        okButtonProps: { danger: true },
        onOk: () => {
          setMockInstalledUids((previous) => {
            const next = new Set(previous);
            next.delete(uid);
            return next;
          });
          setMockInstalledSkills((previous) =>
            previous.filter((skill) => skill.originBuiltinSkillUid !== uid),
          );
          message.success(
            t("admin.memorySkillMarketUninstallSuccess", { name: item.name }),
          );
        },
      });
      return;
    }

    const enabledCopy = skillAssets.find(
      (candidate) => candidate.originBuiltinSkillUid === item.builtinSkillUid,
    );

    if (!enabledCopy) {
      message.info(t("admin.memorySkillMarketNotInstalled"));
      return;
    }

    Modal.confirm({
      title: t("admin.memorySkillMarketUninstallTitle"),
      content: t("admin.memorySkillMarketUninstallContent", {
        name: enabledCopy.name,
      }),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      onOk: async () => {
        await removeSkillAsset(enabledCopy.id);
        await refreshSkillAssets({ page: skillListPage });
        setMarketCatalogAssets([]);
        message.success(
          t("admin.memorySkillMarketUninstallSuccess", {
            name: enabledCopy.name,
          }),
        );
      },
    });
  };

  const handleMarketDetail = (item: StructuredAsset) => {
    openModal("view", item);
  };

  const installingUid = [...builtinSkillEnableLoading][0];

  return (
    <div className="memory-skill-management">
      <div
        className="memory-skill-view-bar"
        role="tablist"
        aria-label={t("admin.memorySkillViewBarLabel")}
      >
        <div className="memory-skill-view-tabs">
          <button
            type="button"
            role="tab"
            className={`memory-skill-view-tab ${skillView === "installed" ? "is-active" : ""}`}
            aria-selected={skillView === "installed"}
            onClick={() => setSkillView("installed")}
          >
            {t("admin.memorySkillViewInstalled")}
          </button>
          <button
            type="button"
            role="tab"
            className={`memory-skill-view-tab ${skillView === "market" ? "is-active" : ""}`}
            aria-selected={skillView === "market"}
            onClick={() => setSkillView("market")}
          >
            {t("admin.memorySkillViewMarket")}
            <Tooltip title={t("admin.memorySkillViewMarketHelp")}>
              <button
                type="button"
                className="memory-skill-tooltip-help"
                aria-label={t("admin.memorySkillViewMarketHelp")}
                onClick={(event) => event.stopPropagation()}
              >
                <QuestionCircleOutlined />
              </button>
            </Tooltip>
          </button>
          <button
            type="button"
            role="tab"
            className={`memory-skill-view-tab ${skillView === "upload" ? "is-active" : ""}`}
            aria-selected={skillView === "upload"}
            onClick={() => setSkillView("upload")}
          >
            {t("admin.memorySkillViewUpload")}
          </button>
          <button
            type="button"
            role="tab"
            className={`memory-skill-view-tab ${skillView === "plugins" ? "is-active" : ""}`}
            aria-selected={skillView === "plugins"}
            onClick={() => setSkillView("plugins")}
          >
            我的插件
          </button>
        </div>

        <div className="memory-skill-bar-actions">
          {skillView === "installed" ? (
            <Button onClick={handleSkillMessageCenter}>
              {t("admin.memorySkillMessageCenterButton", {
                count: messageCenterCount,
              })}
            </Button>
          ) : null}
          {skillView === "market" && isAdmin ? (
            <Button type="primary" onClick={() => setAdminPublishOpen(true)}>
              {t("admin.memorySkillAdminPublishButton")}
            </Button>
          ) : null}
          {skillView === "plugins" ? (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setNewPluginOpen(true)}
            >
              新建插件
            </Button>
          ) : null}
        </div>
      </div>

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
          total={installedTableTotal}
          onPageChange={(nextPage, nextPageSize) => {
            setSkillListPage(nextPage);
            setSkillListPageSize(nextPageSize);
          }}
          tableScroll={tableScroll}
          listContentRef={listContentRef}
        />
      ) : null}

      {skillView === "market" ? (
        <>
          {usingMockMarketData ? (
            <div className="memory-skill-market-demo-banner">
              {t("admin.memorySkillMarketMockBanner")}
            </div>
          ) : null}
          <SkillMarketView
            t={t}
            loading={marketCatalogLoading}
            skillAssets={marketSkillAssets}
            mockInstalledUids={mockInstalledUids}
            keyword={marketKeyword}
            onKeywordChange={setMarketKeyword}
            source={marketSkillSource}
            onSourceChange={setMarketSkillSource}
            category={marketCategory}
            onCategoryChange={setMarketCategory}
            categories={marketCategories}
            onReset={handleMarketReset}
            onInstall={handleMarketInstall}
            onUninstall={handleMarketUninstall}
            onDetail={handleMarketDetail}
            installingUid={installingUid}
          />
        </>
      ) : null}

      {skillView === "upload" ? (
        <SkillUploadView
          t={t}
          onUploaded={async () => {
            await refreshSkillAssets({ page: skillListPage });
          }}
          onNavigateInstalled={() => setSkillView("installed")}
        />
      ) : null}

      <SkillAdminPublishModal
        open={adminPublishOpen}
        t={t}
        onClose={() => setAdminPublishOpen(false)}
        onPublished={async () => {
          await refreshSkillAssets({ page: skillListPage });
          setMarketCatalogAssets([]);
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
