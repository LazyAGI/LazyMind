import { Button, Empty, Form, Modal, Space, Steps } from "antd";
import type { FormInstance, TreeSelectProps } from "antd";
import type { DataNode } from "antd/es/tree";
import type { SourceFormValues, SourceType, SyncMode } from "../constants/types";
import WizardTypeStep from "./wizard/WizardTypeStep";
import WizardConnectionStep from "./wizard/WizardConnectionStep";
import type { LocalPathSelectOption } from "./wizard/treeSelectUtils";

export type { LocalPathSelectOption } from "./wizard/treeSelectUtils";

interface DataSourceWizardModalProps {
  t: any;
  wizardMode: "create" | "edit";
  wizardOpen: boolean;
  wizardStep: number;
  form: FormInstance<SourceFormValues>;
  selectedType: SourceType | null;
  isFeishuSetupReady: boolean;
  isNotionSetupReady?: boolean;
  syncMode: SyncMode;
  saving: boolean;
  savingMode?: "create" | "createAndSync";
  localPathOptions?: LocalPathSelectOption[];
  localPathLoading?: boolean;
  feishuTargetLoading?: boolean;
  feishuTargetTreeData?: DataNode[];
  allowTypeSelection?: boolean;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  onSave: (mode: "create" | "createAndSync") => void;
  onSelectType: (type: SourceType) => void;
  onResetFeishuSetup: () => void;
  onResetNotionSetup?: () => void;
  onLoadLocalPathOptions?: (path?: string) => void;
  onSearchLocalPathOptions?: (keyword: string) => void;
  onLoadLocalPathChildren?: TreeSelectProps["loadData"];
  onResetLocalPathBrowseOptions?: () => void;
  onLoadFeishuTargetOptions?: () => void;
  onSearchFeishuTargetOptions?: (keyword: string) => void;
  onLoadFeishuTargetChildren?: TreeSelectProps["loadData"];
  onResetFeishuTargetBrowseOptions?: () => void;
}

export default function DataSourceWizardModal({
  t,
  wizardMode,
  wizardOpen,
  wizardStep,
  form,
  selectedType,
  isFeishuSetupReady,
  isNotionSetupReady = false,
  syncMode,
  saving,
  savingMode,
  localPathOptions = [],
  localPathLoading = false,
  feishuTargetLoading = false,
  feishuTargetTreeData = [],
  allowTypeSelection = true,
  onClose,
  onPrev,
  onNext,
  onSave,
  onSelectType,
  onResetFeishuSetup,
  onResetNotionSetup,
  onLoadLocalPathOptions,
  onSearchLocalPathOptions,
  onLoadLocalPathChildren,
  onResetLocalPathBrowseOptions,
  onLoadFeishuTargetOptions,
  onSearchFeishuTargetOptions,
  onLoadFeishuTargetChildren,
  onResetFeishuTargetBrowseOptions,
}: DataSourceWizardModalProps) {
  const isEditMode = wizardMode === "edit";

  return (
    <Modal
      title={wizardMode === "edit" ? t("admin.dataSourceEdit") : t("admin.dataSourceCreate")}
      open={wizardOpen}
      width={980}
      onCancel={() => {
        if (!saving) {
          onClose();
        }
      }}
      destroyOnHidden
      maskClosable={false}
      footer={
        <div className="data-source-wizard-footer">
          <Button disabled={saving} onClick={onClose}>{t("common.cancel")}</Button>
          <Space wrap>
            {allowTypeSelection && wizardStep > 0 && !isEditMode ? (
              <Button disabled={saving} onClick={onPrev}>{t("admin.dataSourceWizardPrev")}</Button>
            ) : null}
            {wizardStep < 1 ? (
              <Button type="primary" disabled={saving} onClick={onNext}>
                {t("admin.dataSourceWizardNext")}
              </Button>
            ) : null}
            {wizardStep === 1 ? (
              <>
                <Button
                  disabled={saving}
                  loading={savingMode === "create"}
                  onClick={() => onSave("create")}
                >
                  {isEditMode
                    ? t("admin.dataSourceSaveOnly")
                    : t("admin.dataSourceCreateOnly")}
                </Button>
                <Button
                  type="primary"
                  disabled={saving}
                  loading={savingMode === "createAndSync"}
                  onClick={() => onSave("createAndSync")}
                >
                  {isEditMode
                    ? t("admin.dataSourceSaveAndSync")
                    : t("admin.dataSourceCreateAndSync")}
                </Button>
              </>
            ) : null}
          </Space>
        </div>
      }
    >
      {!isEditMode && allowTypeSelection ? (
        <Steps
          current={wizardStep}
          items={[
            { title: t("admin.dataSourceWizardType") },
            { title: t("admin.dataSourceWizardConnection") },
          ]}
          className="data-source-wizard-steps"
        />
      ) : null}

      <Form form={form} layout="vertical" className="data-source-wizard-form">
        {allowTypeSelection && wizardStep === 0 ? (
          <WizardTypeStep
            t={t}
            selectedType={selectedType}
            isFeishuSetupReady={isFeishuSetupReady}
            isNotionSetupReady={isNotionSetupReady}
            onSelectType={onSelectType}
            onResetFeishuSetup={onResetFeishuSetup}
            onResetNotionSetup={onResetNotionSetup}
          />
        ) : null}

        {wizardStep === 1 ? (
          selectedType ? (
            <WizardConnectionStep
              t={t}
              form={form}
              isEditMode={isEditMode}
              selectedType={selectedType}
              syncMode={syncMode}
              localPathOptions={localPathOptions}
              localPathLoading={localPathLoading}
              feishuTargetLoading={feishuTargetLoading}
              feishuTargetTreeData={feishuTargetTreeData}
              onLoadLocalPathOptions={onLoadLocalPathOptions}
              onSearchLocalPathOptions={onSearchLocalPathOptions}
              onLoadLocalPathChildren={onLoadLocalPathChildren}
              onResetLocalPathBrowseOptions={onResetLocalPathBrowseOptions}
              onLoadFeishuTargetOptions={onLoadFeishuTargetOptions}
              onSearchFeishuTargetOptions={onSearchFeishuTargetOptions}
              onLoadFeishuTargetChildren={onLoadFeishuTargetChildren}
              onResetFeishuTargetBrowseOptions={onResetFeishuTargetBrowseOptions}
            />
          ) : (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t("admin.dataSourceSelectTypeInPrevStep")}
            />
          )
        ) : null}
      </Form>
    </Modal>
  );
}
