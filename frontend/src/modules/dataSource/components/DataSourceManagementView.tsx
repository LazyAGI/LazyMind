import { Typography } from "antd";
import DataSourceWizardModal from "./DataSourceWizardModal";
import DataSourceAssetTable from "./management/DataSourceAssetTable";
import DataSourceProviderPanel from "./management/DataSourceProviderPanel";
import DataSourceManagementModals from "./management/DataSourceManagementModals";
import type { DataSourceManagementVm } from "../hooks/useDataSourceManagement";

const { Paragraph } = Typography;

export default function DataSourceManagementView({ vm }: { vm: DataSourceManagementVm }) {
  const {
    t,
    form,
    activeView,
    setActiveView,
    wizardOpen,
    wizardStep,
    setWizardStep,
    wizardMode,
    selectedType,
    syncMode,
    wizardSaving,
    wizardSavingMode,
    isFeishuSetupReady,
    isNotionSetupReady,
    localPathOptions,
    localPathLoading,
    loadLocalPathOptions,
    handleSearchLocalPathOptions,
    handleLoadLocalPathChildren,
    resetLocalPathBrowseOptions,
    feishuTargetTreeData,
    feishuTargetLoading,
    loadFeishuTargetOptions,
    handleSearchFeishuTargetOptions,
    handleLoadFeishuTargetChildren,
    resetFeishuTargetBrowseOptions,
    handleCloseWizard,
    handleNextStep,
    handleSave,
    handleSelectType,
    handleResetFeishuSetup,
    handleResetNotionSetup,
  } = vm;

  return (
    <div className="admin-page data-source-page">
      <div className="admin-page-toolbar data-source-page-toolbar">
        <div className="admin-page-toolbar-left data-source-page-toolbar-left">
          <div>
            <h2 className="admin-page-title">{t("admin.dataSourceManagement")}</h2>
            <Paragraph className="data-source-page-subtitle">
              {t("admin.dataSourceSubtitle")}
            </Paragraph>
          </div>
        </div>
      </div>

      <div className="data-source-view-tabs">
        <button
          type="button"
          className={activeView === "assets" ? "selected" : ""}
          onClick={() => setActiveView("assets")}
        >
          {t("admin.dataSourceListTitle")}
        </button>
        <button
          type="button"
          className={activeView === "connectors" ? "selected" : ""}
          onClick={() => setActiveView("connectors")}
        >
          {t("admin.dataSourceProviderTitle")}
        </button>
      </div>

      <section className="data-source-workbench">
        {activeView === "assets" ? (
          <DataSourceAssetTable vm={vm} />
        ) : (
          <DataSourceProviderPanel vm={vm} />
        )}
      </section>

      <DataSourceManagementModals vm={vm} />

      <DataSourceWizardModal
        t={t}
        wizardMode={wizardMode}
        wizardOpen={wizardOpen}
        wizardStep={wizardStep}
        form={form}
        selectedType={selectedType}
        isFeishuSetupReady={isFeishuSetupReady}
        isNotionSetupReady={isNotionSetupReady}
        syncMode={syncMode}
        saving={wizardSaving}
        savingMode={wizardSavingMode || undefined}
        localPathOptions={localPathOptions}
        localPathLoading={localPathLoading}
        feishuTargetLoading={feishuTargetLoading}
        feishuTargetTreeData={feishuTargetTreeData}
        allowTypeSelection={false}
        onClose={handleCloseWizard}
        onPrev={() => setWizardStep((step) => step - 1)}
        onNext={handleNextStep}
        onSave={(mode) => {
          void handleSave(mode);
        }}
        onSelectType={handleSelectType}
        onResetFeishuSetup={handleResetFeishuSetup}
        onResetNotionSetup={handleResetNotionSetup}
        onLoadLocalPathOptions={(path) => {
          void loadLocalPathOptions(path);
        }}
        onSearchLocalPathOptions={handleSearchLocalPathOptions}
        onLoadLocalPathChildren={handleLoadLocalPathChildren}
        onResetLocalPathBrowseOptions={resetLocalPathBrowseOptions}
        onLoadFeishuTargetOptions={() => {
          void loadFeishuTargetOptions();
        }}
        onSearchFeishuTargetOptions={handleSearchFeishuTargetOptions}
        onLoadFeishuTargetChildren={handleLoadFeishuTargetChildren}
        onResetFeishuTargetBrowseOptions={resetFeishuTargetBrowseOptions}
      />
    </div>
  );
}
