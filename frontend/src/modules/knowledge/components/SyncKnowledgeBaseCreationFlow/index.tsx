import { FC } from "react";
import DataSourceWizardModal from "@/modules/dataSource/components/DataSourceWizardModal";
import DataSourceManagementModals from "@/modules/dataSource/components/management/DataSourceManagementModals";
import {
  useSyncKnowledgeBaseCreation,
  type SyncKnowledgeBaseCreationVm,
} from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import "@/modules/dataSource/index.scss";
import "./index.scss";

interface Props {
  onSuccess?: () => void | Promise<void>;
  vm?: SyncKnowledgeBaseCreationVm;
}

const SyncKnowledgeBaseCreationFlowInner: FC<{ vm: SyncKnowledgeBaseCreationVm }> = ({ vm }) => {
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
    handleSelectType,
    handleResetFeishuSetup,
    handleResetNotionSetup,
  } = vm;

  return (
    <>
      <DataSourceManagementModals
        vm={vm}
        titleKey="knowledge.createFromCloudDocumentsTitle"
        introKey="knowledge.createFromCloudDocumentsIntro"
      />

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
    </>
  );
};

const SyncKnowledgeBaseCreationFlowWithHook: FC<Pick<Props, "onSuccess">> = ({ onSuccess }) => {
  const vm = useSyncKnowledgeBaseCreation({ onSuccess });
  return <SyncKnowledgeBaseCreationFlowInner vm={vm} />;
};

const SyncKnowledgeBaseCreationFlow: FC<Props> = ({ onSuccess, vm }) => {
  if (vm) {
    return <SyncKnowledgeBaseCreationFlowInner vm={vm} />;
  }

  return <SyncKnowledgeBaseCreationFlowWithHook onSuccess={onSuccess} />;
};

export { useSyncKnowledgeBaseCreation };
export default SyncKnowledgeBaseCreationFlow;
