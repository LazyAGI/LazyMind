import { useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Empty,
  Input,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
} from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import { useMemoryManagementOutletContext } from "../../context";
import type { ExperienceAsset, StructuredAsset } from "../../shared";
import GlossaryListSection from "../../components/GlossaryListSection";

const defaultMemoryListPageSize = 6;
const memoryListPageSizeOptions = [6, 12, 20, 50];
const showGlossaryInboxUi = true;

export default function MemoryManagementListPage() {
  const listContentRef = useRef<HTMLDivElement>(null);
  const [memoryTableBodyHeight, setMemoryTableBodyHeight] = useState<number>();
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(defaultMemoryListPageSize);
  const {
    t,
    activeTab,
    openSkillShareCenter,
    incomingPendingCount,
    glossaryChangeProposals,
    openModal,
    currentTabMeta,
    memoryTabOrder,
    tabMeta,
    setActiveTab,
    setGlossaryDetailTarget,
    setGlossaryInboxOpen,
    resetFilters,
    navigateToMemoryList,
    experienceFeatureEnabled,
    experienceSettingSaving,
    handleExperienceFeatureToggle,
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
    selectedGlossaryAssets,
    glossaryAssets,
    glossaryLoading,
    glossaryListPage,
    glossaryListPageSize,
    glossaryListTotal,
    glossaryLoadError,
    refreshGlossaryAssets,
    handleBatchMergeGlossary,
    handleBatchDeleteGlossary,
    filteredExperienceItems,
    experienceLoading,
    experienceColumns,
    filteredGlossaryItems,
    glossaryColumns,
    selectedGlossaryAssetIds,
    setGlossaryListPage,
    setGlossaryListPageSize,
    setSelectedGlossaryAssetIds,
    skillLoading,
    skillListPage,
    skillListPageSize,
    skillListTotal,
    setSkillListPage,
    setSkillListPageSize,
    skillAssets,
    filteredSkillTree,
    filteredStructuredItems,
    genericColumns,
    toolColumns,
  } = useMemoryManagementOutletContext();

  const activeListTotal = useMemo(() => {
    if (activeTab === "experience") {
      return filteredExperienceItems.length;
    }
    if (activeTab === "skills") {
      return skillListTotal;
    }
    if (activeTab === "tools") {
      return filteredStructuredItems.length;
    }
    return 0;
  }, [
    activeTab,
    filteredExperienceItems.length,
    skillListTotal,
    filteredStructuredItems.length,
  ]);

  useEffect(() => {
    if (activeTab === "skills") {
      setSkillListPage(1);
      return;
    }
    setCurrentPage(1);
  }, [activeTab, category, query, setSkillListPage, tag]);

  const activePage = activeTab === "skills" ? skillListPage : currentPage;
  const activePageSize = activeTab === "skills" ? skillListPageSize : pageSize;

  useEffect(() => {
    const maxPage = Math.max(1, Math.ceil(activeListTotal / activePageSize));
    if (activePage <= maxPage) {
      return;
    }
    if (activeTab === "skills") {
      setSkillListPage(maxPage);
    } else {
      setCurrentPage(maxPage);
    }
  }, [activeListTotal, activePage, activePageSize, activeTab, setSkillListPage]);

  const memoryListPagination = getLocalizedTablePagination(
    {
      current: activePage,
      pageSize: activePageSize,
      total: activeListTotal,
      showSizeChanger: true,
      pageSizeOptions: memoryListPageSizeOptions,
      showTotal: (total) => t("common.totalItems", { total }),
      onChange: (page, nextPageSize) => {
        if (activeTab === "skills") {
          setSkillListPage(page);
          setSkillListPageSize(nextPageSize);
          return;
        }
        setCurrentPage(page);
        setPageSize(nextPageSize);
      },
      onShowSizeChange: (_current, nextPageSize) => {
        if (activeTab === "skills") {
          setSkillListPage(1);
          setSkillListPageSize(nextPageSize);
          return;
        }
        setCurrentPage(1);
        setPageSize(nextPageSize);
      },
    },
    t,
  );
  const memoryTableScroll = memoryTableBodyHeight
    ? { x: 980, y: memoryTableBodyHeight }
    : { x: 980 };

  useEffect(() => {
    if (activeTab === "glossary") {
      return undefined;
    }

    const contentElement = listContentRef.current;
    if (!contentElement) {
      return undefined;
    }

    const updateTableHeight = () => {
      const headerElement = contentElement.querySelector<HTMLElement>(".ant-table-thead");
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
  }, [activeListTotal, activePageSize, activeTab]);

  return (
    <div className={`memory-list-page ${activeTab === "glossary" ? "is-glossary-tab" : ""}`}>
      <div className="memory-page-header">
        <div>
          <div className="memory-page-title-row">
            <h2 className="admin-page-title">{t("admin.memoryManagement")}</h2>
            <Tooltip placement="top" title={t("admin.memoryManagementHelp")}>
              <button
                aria-label={t("admin.memoryManagementHelpAriaLabel")}
                className="memory-page-title-help"
                type="button"
              >
                <QuestionCircleOutlined />
              </button>
            </Tooltip>
          </div>
          <p className="memory-page-subtitle">
            {t("admin.memoryManagementSubtitle")}
          </p>
        </div>
        <Space>
          {activeTab === "skills" ? (
            <Button onClick={() => openSkillShareCenter("incoming")}>
              {t("admin.memorySkillShareInboxButton", {
                count: incomingPendingCount,
              })}
            </Button>
          ) : null}
          {showGlossaryInboxUi && activeTab === "glossary" ? (
            <Button onClick={() => setGlossaryInboxOpen(true)}>
              {t("admin.memoryGlossaryInboxButton", {
                count: glossaryChangeProposals.length,
              })}
            </Button>
          ) : null}
          {activeTab !== "tools" && activeTab !== "experience" ? (
            <Button
              type="primary"
              className="admin-page-primary-button"
              onClick={() => openModal("add")}
            >
              {activeTab === "glossary"
                ? t("admin.memoryCreateGlossaryButton")
                : t("admin.memoryCreateButton", { unit: currentTabMeta.unit })}
            </Button>
          ) : null}
        </Space>
      </div>

      <div className="memory-tab-grid">
        {memoryTabOrder.map((tabKey: string) => {
          const tabItem = tabMeta[tabKey];

          return (
            <button
              key={tabKey}
              type="button"
              className={`memory-tab-card ${activeTab === tabKey ? "is-active" : ""}`}
              onClick={() => {
                setActiveTab(tabKey);
                if (tabKey !== "glossary") {
                  setGlossaryDetailTarget(null);
                }
                resetFilters();
                navigateToMemoryList(tabKey);
              }}
            >
              <span className="memory-tab-icon">{tabItem.icon}</span>
              <span className="memory-tab-copy">
                <strong>{tabItem.title}</strong>
                <span>{tabItem.description}</span>
              </span>
            </button>
          );
        })}
      </div>

      {activeTab === "experience" ? (
        <div className="memory-experience-feature-bar">
          <div className="memory-experience-feature-copy">
            <span
              className={`memory-experience-feature-status ${
                experienceFeatureEnabled ? "is-on" : "is-off"
              }`}
            >
              <span className="memory-experience-feature-status-dot" />
              {experienceFeatureEnabled ? t("admin.enabled") : t("admin.disabled")}
            </span>
            <div className="memory-experience-feature-text">
              <strong>{t("admin.memoryHabitFeatureToggle")}</strong>
              <span>
                {experienceFeatureEnabled
                  ? t("admin.memoryHabitFeatureEnabledHint")
                  : t("admin.memoryHabitFeatureDisabledHint")}
              </span>
            </div>
          </div>
          <Switch
            checked={experienceFeatureEnabled}
            loading={experienceSettingSaving}
            checkedChildren={t("admin.enable")}
            unCheckedChildren={t("admin.disable")}
            onChange={(checked) => void handleExperienceFeatureToggle(checked)}
          />
        </div>
      ) : null}

      {activeTab !== "experience" ? (
        <div className="memory-filter-bar">
          <Input.Search
            allowClear
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            onSearch={(value) => setQuery(value)}
            placeholder={t("admin.memorySearchPlaceholder")}
            className="memory-filter-search"
          />
          {activeTab === "tools" || activeTab === "skills" ? (
            <>
              <Select
                allowClear
                value={category}
                placeholder={t("admin.memoryAllCategories")}
                options={availableCategories.map((item: string) => ({
                  label: item,
                  value: item,
                }))}
                className="memory-filter-select"
                onChange={(value) => setCategory(value)}
              />
              {activeTab === "skills" ? (
                <Select
                  allowClear
                  value={tag}
                  placeholder={t("admin.memoryAllTags")}
                  options={availableTags.map((item: string) => ({
                    label: item,
                    value: item,
                  }))}
                  className="memory-filter-select"
                  onChange={(value) => setTag(value)}
                />
              ) : null}
            </>
          ) : activeTab === "glossary" ? (
            <Select
              allowClear
              value={glossarySource}
              placeholder={t("admin.memoryAllSources")}
              options={availableGlossarySourceOptions}
              className="memory-filter-select"
              onChange={(value) => setGlossarySource(value)}
            />
          ) : null}
          <Button onClick={resetFilters}>{t("admin.memoryReset")}</Button>
        </div>
      ) : null}

      <div className="memory-list-content" ref={listContentRef}>
        {activeTab === "experience" ? (
          <Table<ExperienceAsset>
            className="admin-page-table memory-table"
            rowKey="id"
            loading={experienceLoading}
            dataSource={filteredExperienceItems}
            columns={experienceColumns}
            tableLayout="fixed"
            pagination={memoryListPagination}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={t("admin.memoryEmpty")}
                />
              ),
            }}
            scroll={memoryTableScroll}
          />
        ) : activeTab === "glossary" ? (
          <GlossaryListSection
            t={t}
            assets={glossaryAssets}
            columns={glossaryColumns}
            filteredItems={filteredGlossaryItems}
            glossaryListPage={glossaryListPage}
            glossaryListPageSize={glossaryListPageSize}
            glossaryListTotal={glossaryListTotal}
            glossaryLoadError={glossaryLoadError}
            glossaryLoading={glossaryLoading}
            glossarySource={glossarySource}
            handleBatchDeleteGlossary={handleBatchDeleteGlossary}
            handleBatchMergeGlossary={handleBatchMergeGlossary}
            query={query}
            refreshGlossaryAssets={refreshGlossaryAssets}
            selectedGlossaryAssetIds={selectedGlossaryAssetIds}
            selectedGlossaryAssets={selectedGlossaryAssets}
            setGlossaryListPage={setGlossaryListPage}
            setGlossaryListPageSize={setGlossaryListPageSize}
            setSelectedGlossaryAssetIds={setSelectedGlossaryAssetIds}
          />
        ) : (
          <Table<StructuredAsset>
            className="admin-page-table memory-table"
            rowKey="id"
            loading={activeTab === "skills" ? skillLoading : false}
            dataSource={activeTab === "skills" ? filteredSkillTree : filteredStructuredItems}
            columns={activeTab === "tools" ? toolColumns : genericColumns}
            rowClassName={(record) =>
              activeTab === "skills" && record.isBuiltinTemplate
                ? "memory-builtin-template-row"
                : ""
            }
            expandable={
              activeTab === "skills"
                ? {
                    defaultExpandAllRows: true,
                    rowExpandable: (record) =>
                      skillAssets.some((item: StructuredAsset) => item.parentId === record.id),
                  }
                : undefined
            }
            pagination={memoryListPagination}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={t("admin.memoryEmpty")}
                />
              ),
            }}
            scroll={memoryTableScroll}
          />
        )}
      </div>
    </div>
  );
}
