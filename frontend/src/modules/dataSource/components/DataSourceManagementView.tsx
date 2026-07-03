import { Alert, Typography } from "antd";
import { Link } from "react-router-dom";
import TypedConfirmModal from '@/components/ui/TypedConfirmModal';
import DataSourceWizardModal from "./DataSourceWizardModal";
import DataSourceAssetTable from "./management/DataSourceAssetTable";
import type { DataSourceManagementVm } from "../hooks/useDataSourceManagement";
import { CLOUD_DOCUMENTS_PATH } from "@/modules/modelProvider/utils/cloudDocumentUrls";

const { Paragraph } = Typography;

export default function DataSourceManagementView({ vm }: { vm: DataSourceManagementVm }) {
  const {
    t,
    form,
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
    requestSaveWithSyncConfirm,
    confirmRef,
    handleTypedConfirm,
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

      <Alert
        showIcon
        type="info"
        className="data-source-cloud-doc-link-alert"
        message={
          <>
            {t("modelProvider.cloudDocuments.linkFromDataSource")}{" "}
            <Link to={CLOUD_DOCUMENTS_PATH}>{t("modelProvider.tabs.cloudDocuments")}</Link>
          </>
        }
      />

      <section className="data-source-workbench">
        <DataSourceAssetTable vm={vm} />
      </section>

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
          requestSaveWithSyncConfirm(mode);
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

      <TypedConfirmModal ref={confirmRef} onClick={handleTypedConfirm} />
    </div>
  );
}
