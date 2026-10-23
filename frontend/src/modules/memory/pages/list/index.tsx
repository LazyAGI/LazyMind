import { Tooltip } from "antd";
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
    embedded,
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
      </div>

      {!embedded ? <nav className="memory-tab-grid" aria-label={t("admin.memoryManagement")}>
        {memoryTabOrder.map((tabKey: string) => {
          const tabItem = tabMeta[tabKey];

          return (
            <button
              key={tabKey}
              type="button"
              className={`memory-tab-card ${activeTab === tabKey ? "is-active" : ""}`}
              title={tabItem.description}
              aria-current={activeTab === tabKey ? "page" : undefined}
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
      </nav> : null}

      <div
        className={`memory-list-content ${activeTab === "skills" ? "is-skill-management" : ""}`}
      >
        {activeTab === "skills" ? (
          <SkillManagementSection />
        ) : activeTab === "experience" ? (
          <div className="memory-workspace-surface is-experience">
            <ExperienceOverview />
          </div>
        ) : activeTab === "glossary" ? (
          <div className="memory-workspace-surface is-glossary">
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
            availableGlossarySourceOptions={availableGlossarySourceOptions}
            searchInput={searchInput}
            query={query}
            inboxCount={glossaryChangeProposals.length}
            showInbox={showGlossaryInboxUi}
            onSearchInputChange={setSearchInput}
            onSearch={setQuery}
            onSourceChange={setGlossarySource}
            onCreate={() => openModal("add")}
            onOpenInbox={() => setGlossaryInboxOpen(true)}
            handleBatchDeleteGlossary={handleBatchDeleteGlossary}
            handleBatchMergeGlossary={handleBatchMergeGlossary}
            refreshGlossaryAssets={refreshGlossaryAssets}
            selectedGlossaryAssetIds={selectedGlossaryAssetIds}
            selectedGlossaryAssets={selectedGlossaryAssets}
            setGlossaryListPage={setGlossaryListPage}
            setGlossaryListPageSize={setGlossaryListPageSize}
            setSelectedGlossaryAssetIds={setSelectedGlossaryAssetIds}
          />
          </div>
        ) : null}
      </div>
    </div>
  );
}
