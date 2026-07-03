import { useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Empty,
  Input,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from "antd";
import {
  BulbOutlined,
  CheckCircleOutlined,
  InfoCircleOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { getLocalizedTablePagination } from "@/components/ui/pagination";
import { useMemoryManagementOutletContext } from "../../context";
import type { ExperienceAsset } from "../../shared";
import type { SkillReviewResultRecord } from "../../skillApi";
import GlossaryListSection from "../../components/GlossaryListSection";
import SkillManagementSection from "../../components/SkillManagementSection";

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
    glossarySource,
    setGlossarySource,
    availableGlossarySourceOptions,
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
    experienceProfileExpandable,
    filteredGlossaryItems,
    glossaryColumns,
    selectedGlossaryAssetIds,
    setGlossaryListPage,
    setGlossaryListPageSize,
    setSelectedGlossaryAssetIds,
    manualSkillReviewSummary,
    manualSkillReviewLoading,
    manualSkillReviewRunning,
    manualSkillReviewResults = [] as SkillReviewResultRecord[],
    manualSkillReviewResultStatus = "",
    refreshManualSkillReviewSummary,
    handleRunManualSkillReview,
  } = useMemoryManagementOutletContext();

  const activeListTotal = useMemo(() => {
    if (activeTab === "experience") {
      return filteredExperienceItems.length;
    }
    return 0;
  }, [activeTab, filteredExperienceItems.length]);

  useEffect(() => {
    setCurrentPage(1);
  }, [activeTab, query]);

  const activePage = currentPage;
  const activePageSize = pageSize;

  useEffect(() => {
    const maxPage = Math.max(1, Math.ceil(activeListTotal / activePageSize));
    if (activePage <= maxPage) {
      return;
    }
    setCurrentPage(maxPage);
  }, [activeListTotal, activePage, activePageSize]);

  const memoryListPagination = getLocalizedTablePagination(
    {
      current: activePage,
      pageSize: activePageSize,
      total: activeListTotal,
      showSizeChanger: true,
      pageSizeOptions: memoryListPageSizeOptions,
      showTotal: (total) => t("common.totalItems", { total }),
      onChange: (page, nextPageSize) => {
        setCurrentPage(page);
        setPageSize(nextPageSize);
      },
      onShowSizeChange: (_current, nextPageSize) => {
        setCurrentPage(1);
        setPageSize(nextPageSize);
      },
    },
    t,
  );
  const memoryTableScroll = memoryTableBodyHeight
    ? { x: 980, y: memoryTableBodyHeight }
    : { x: 980 };
  const manualSkillReviewCount = manualSkillReviewSummary?.qualifiedSessionCount ?? 0;
  const manualSkillReviewHasRunningTask =
    manualSkillReviewSummary?.runningTask?.status === "pending" ||
    manualSkillReviewSummary?.runningTask?.status === "running";
  const manualSkillReviewButtonBusy =
    manualSkillReviewRunning || manualSkillReviewHasRunningTask;
  const manualSkillReviewButtonDisabled =
    manualSkillReviewLoading || manualSkillReviewButtonBusy || manualSkillReviewCount <= 0;
  const manualSkillReviewButtonTip = manualSkillReviewButtonBusy
    ? t("admin.memoryManualSkillReviewStarted")
    : manualSkillReviewCount <= 0
      ? t("admin.memoryManualSkillReviewNoContent")
      : t("admin.memoryManualSkillReviewReady");
  const manualSkillReviewStatusText =
    manualSkillReviewCount > 0
      ? t("admin.memoryManualSkillReviewCount", {
          count: manualSkillReviewCount,
        })
      : t("admin.memoryManualSkillReviewEmpty");
  const manualSkillReviewNewResults = useMemo(
    () =>
      manualSkillReviewResults.filter(
        (item: SkillReviewResultRecord) => item.type === "new",
      ),
    [manualSkillReviewResults],
  );
  const manualSkillReviewUpdatedResults = useMemo(
    () =>
      manualSkillReviewResults.filter(
        (item: SkillReviewResultRecord) => item.type === "patch",
      ),
    [manualSkillReviewResults],
  );
  const manualSkillReviewHasResult =
    manualSkillReviewNewResults.length > 0 ||
    manualSkillReviewUpdatedResults.length > 0;
  const manualSkillReviewResultMessage =
    manualSkillReviewResultStatus === "skipped"
      ? t("admin.memoryManualSkillReviewSkipped")
      : manualSkillReviewResultStatus === "failed"
        ? t("admin.memoryManualSkillReviewFailed")
        : t("admin.memoryManualSkillReviewNoResult");

  useEffect(() => {
    if (activeTab === "glossary" || activeTab === "skills") {
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
    <div
      className={`memory-list-page ${
        activeTab === "glossary" ? "is-glossary-tab" : ""
      } ${activeTab === "skills" ? "is-skills-tab" : ""}`}
    >
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
          {showGlossaryInboxUi && activeTab === "glossary" ? (
            <Button onClick={() => setGlossaryInboxOpen(true)}>
              {t("admin.memoryGlossaryInboxButton", {
                count: glossaryChangeProposals.length,
              })}
            </Button>
          ) : null}
          {activeTab !== "experience" ? (
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

      {activeTab !== "experience" && activeTab !== "skills" ? (
        <div className="memory-filter-bar">
          <Input.Search
            allowClear
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            onSearch={(value) => setQuery(value)}
            placeholder={t("admin.memorySearchPlaceholder")}
            className="memory-filter-search"
          />
          {activeTab === "glossary" ? (
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

      <div
        className={`memory-list-content ${activeTab === "skills" ? "is-skill-management" : ""}`}
        ref={activeTab === "skills" ? undefined : listContentRef}
      >
        {activeTab === "skills" ? (
          <>
            <div className="memory-manual-skill-review-row">
              <div
                className={`memory-manual-skill-review ${
                  manualSkillReviewCount > 0 ? "is-ready" : "is-empty"
                }`}
              >
                <div className="memory-manual-skill-review-copy">
                  <strong>{t("admin.memoryManualSkillReviewTitle")}</strong>
                  <span>{manualSkillReviewStatusText}</span>
                </div>
                <Space className="memory-manual-skill-review-actions">
                  <Tooltip title={t("admin.memoryManualSkillReviewRefresh")}>
                    <Button
                      className="memory-manual-skill-review-icon-button"
                      icon={<ReloadOutlined />}
                      loading={manualSkillReviewLoading}
                      onClick={() => void refreshManualSkillReviewSummary()}
                    />
                  </Tooltip>
                  <Tooltip title={manualSkillReviewButtonTip}>
                    <Button
                      className="memory-manual-skill-review-run-button"
                      type="primary"
                      icon={<BulbOutlined />}
                      disabled={manualSkillReviewButtonDisabled}
                      loading={manualSkillReviewButtonBusy}
                      onClick={() => void handleRunManualSkillReview()}
                    >
                      {t("admin.memoryManualSkillReviewRun")}
                    </Button>
                  </Tooltip>
                </Space>
              </div>
              {manualSkillReviewRunning ? (
                <div className="memory-manual-skill-review-result is-running">
                  <span className="memory-manual-skill-review-result-icon">
                    <InfoCircleOutlined />
                  </span>
                  <div className="memory-manual-skill-review-result-body">
                    <span className="memory-manual-skill-review-result-hint">
                      {t("admin.memoryManualSkillReviewRunningHint")}
                    </span>
                  </div>
                </div>
              ) : manualSkillReviewResultStatus ? (
                <div
                  className={`memory-manual-skill-review-result ${
                    manualSkillReviewResultStatus === "done"
                      ? "is-done"
                      : manualSkillReviewResultStatus === "empty"
                        ? "is-empty"
                        : "is-warning"
                  }`}
                >
                  <span className="memory-manual-skill-review-result-icon">
                    {manualSkillReviewResultStatus === "done" ? (
                      <CheckCircleOutlined />
                    ) : (
                      <InfoCircleOutlined />
                    )}
                  </span>
                  <div className="memory-manual-skill-review-result-body">
                    {manualSkillReviewResultStatus === "done" &&
                    manualSkillReviewHasResult ? (
                      <>
                        {manualSkillReviewNewResults.length > 0 ? (
                          <div className="memory-manual-skill-review-result-line">
                            <span className="memory-manual-skill-review-result-label">
                              {t("admin.memoryManualSkillReviewNewSkills")}
                            </span>
                            <span className="memory-manual-skill-review-result-tags">
                              {manualSkillReviewNewResults.map((item: SkillReviewResultRecord) => (
                                <Tag
                                  className="memory-manual-skill-review-result-tag"
                                  key={item.id}
                                >
                                  {item.skillName}
                                </Tag>
                              ))}
                            </span>
                          </div>
                        ) : null}
                        {manualSkillReviewUpdatedResults.length > 0 ? (
                          <div className="memory-manual-skill-review-result-line">
                            <span className="memory-manual-skill-review-result-label">
                              {t("admin.memoryManualSkillReviewUpdatedSkills")}
                            </span>
                            <span className="memory-manual-skill-review-result-tags">
                              {manualSkillReviewUpdatedResults.map(
                                (item: SkillReviewResultRecord) => (
                                  <Tag
                                    className="memory-manual-skill-review-result-tag"
                                    key={item.id}
                                  >
                                    {item.skillName}
                                  </Tag>
                                ),
                              )}
                            </span>
                          </div>
                        ) : null}
                        <span className="memory-manual-skill-review-result-hint">
                          {t("admin.memoryManualSkillReviewConfirmHint")}
                        </span>
                      </>
                    ) : (
                      <span className="memory-manual-skill-review-result-hint">
                        {manualSkillReviewResultMessage}
                      </span>
                    )}
                  </div>
                </div>
              ) : null}
            </div>
            <SkillManagementSection />
          </>
        ) : activeTab === "experience" ? (
          <Table<ExperienceAsset>
            className="admin-page-table memory-table"
            rowKey="id"
            loading={experienceLoading}
            dataSource={filteredExperienceItems}
            columns={experienceColumns}
            expandable={experienceProfileExpandable}
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
        ) : null}
      </div>
    </div>
  );
}
