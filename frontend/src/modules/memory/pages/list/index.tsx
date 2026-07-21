import {
  Button,
  Input,
  Select,
  Space,
  Tooltip,
} from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
import { useMemoryManagementOutletContext } from "../../context";
import ExperienceOverview from "../../components/ExperienceOverview";
import GlossaryListSection from "../../components/GlossaryListSection";
import SkillManagementSection from "../../components/SkillManagementSection";

const showGlossaryInboxUi = true;

export default function MemoryManagementListPage() {
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
    filteredGlossaryItems,
    glossaryColumns,
    selectedGlossaryAssetIds,
    setGlossaryListPage,
    setGlossaryListPageSize,
    setSelectedGlossaryAssetIds,
  } = useMemoryManagementOutletContext();

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
            <Tooltip placement="top" title={t("admin.memoryManagementSubtitle")}>
              <button
                aria-label={t("admin.memoryManagementHelpAriaLabel")}
                className="memory-page-title-help"
                type="button"
              >
                <QuestionCircleOutlined />
              </button>
            </Tooltip>
          </div>
        </div>
        <Space>
          {showGlossaryInboxUi && activeTab === "glossary" ? (
            <Button onClick={() => setGlossaryInboxOpen(true)}>
              {t("admin.memoryGlossaryInboxButton", {
                count: glossaryChangeProposals.length,
              })}
            </Button>
          ) : null}
          {activeTab !== "experience" && activeTab !== "skills" ? (
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
      >
        {activeTab === "skills" ? (
          <SkillManagementSection />
        ) : activeTab === "experience" ? (
          <ExperienceOverview />
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
