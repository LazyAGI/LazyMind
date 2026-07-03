import { Alert, Button, Input, Modal, Space, Tag, Typography } from "antd";
import { ArrowRightOutlined, FileTextOutlined } from "@ant-design/icons";
import type { DataSourceManagementVm } from "../../hooks/useDataSourceManagement";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import DataSourceProviderPicker from "./DataSourceProviderPicker";

type SourceCreationModalsVm = Pick<
  DataSourceManagementVm,
  | "t"
  | "createProviderModalOpen"
  | "setCreateProviderModalOpen"
  | "creatableSourceTypeOptions"
  | "handleCreateProviderSelect"
  | "isFeishuAuthValid"
  | "isNotionAuthValid"
  | "isFeishuSetupReady"
  | "isNotionSetupReady"
  | "authSelectModalOpen"
  | "setAuthSelectModalOpen"
  | "handleOpenFeishuGuideFromAuthSelect"
  | "validFeishuAccounts"
  | "handleSelectFeishuAuthConnection"
  | "manualOauthModalOpen"
  | "setManualOauthModalOpen"
  | "manualOauthCallbackValue"
  | "setManualOauthCallbackValue"
  | "manualOauthSubmitting"
  | "handleSubmitManualOauthCallback"
>;

interface DataSourceManagementModalsProps {
  vm: SourceCreationModalsVm | SyncKnowledgeBaseCreationVm;
  titleKey?: string;
  introKey?: string;
  hideProviderModal?: boolean;
}

const { Paragraph } = Typography;

export default function DataSourceManagementModals({
  vm,
  titleKey = "admin.dataSourceCreateKnowledgeSource",
  introKey = "admin.dataSourceCreateProviderIntro",
  hideProviderModal = false,
}: DataSourceManagementModalsProps) {
  const {
    t,
    createProviderModalOpen,
    setCreateProviderModalOpen,
    authSelectModalOpen,
    setAuthSelectModalOpen,
    handleOpenFeishuGuideFromAuthSelect,
    validFeishuAccounts,
    handleSelectFeishuAuthConnection,
    manualOauthModalOpen,
    setManualOauthModalOpen,
    manualOauthCallbackValue,
    setManualOauthCallbackValue,
    manualOauthSubmitting,
    handleSubmitManualOauthCallback,
  } = vm;

  return (
    <>
      {!hideProviderModal ? (
        <Modal
          title={t(titleKey)}
          open={createProviderModalOpen}
          footer={null}
          width={720}
          destroyOnHidden
          onCancel={() => setCreateProviderModalOpen(false)}
        >
          <Paragraph className="data-source-create-provider-intro">
            {t(introKey)}
          </Paragraph>
          <DataSourceProviderPicker vm={vm} />
        </Modal>
      ) : null}

      <Modal
        title={
          <div className="data-source-auth-select-title">
            <span>{t("admin.dataSourceSelectFeishuAuthTitle")}</span>
            <Button
              type="link"
              size="small"
              className="data-source-auth-select-guide"
              icon={<FileTextOutlined />}
              onClick={handleOpenFeishuGuideFromAuthSelect}
            >
              {t("admin.dataSourceFeishuSetupGuideAction")}
            </Button>
          </div>
        }
        open={authSelectModalOpen}
        footer={null}
        width={640}
        destroyOnHidden
        onCancel={() => setAuthSelectModalOpen(false)}
      >
        <Paragraph className="data-source-create-provider-intro">
          {t("admin.dataSourceSelectFeishuAuthIntro")}
        </Paragraph>
        <Space direction="vertical" size={10} style={{ width: "100%" }}>
          {validFeishuAccounts.map((account) => (
            <button
              key={account.id}
              type="button"
              className="data-source-auth-option-card"
              onClick={() => {
                if (account.connection) {
                  handleSelectFeishuAuthConnection(account.connection);
                }
              }}
            >
              <span className="data-source-provider-logo data-source-icon-feishu">
                <img
                  alt=""
                  aria-hidden="true"
                  loading="lazy"
                  src="https://www.google.com/s2/favicons?domain=feishu.cn&sz=96"
                  onError={(event) => {
                    event.currentTarget.style.display = "none";
                  }}
                />
              </span>
              <span className="data-source-provider-card-copy">
                <span className="data-source-provider-title-row">
                  <span className="data-source-provider-name">
                    {account.connection?.accountName || account.name}
                  </span>
                  <Tag color="success">
                    {t("admin.dataSourceProviderAuthValid")}
                  </Tag>
                </span>
                <span className="data-source-provider-desc">
                  {account.connection?.connectionId}
                </span>
              </span>
              <span
                className="data-source-provider-card-arrow"
                aria-hidden="true"
              >
                <ArrowRightOutlined />
              </span>
            </button>
          ))}
        </Space>
      </Modal>

      <Modal
        title={t("admin.dataSourceOauthManualCallbackTitle")}
        open={manualOauthModalOpen}
        onCancel={() => {
          if (!manualOauthSubmitting) {
            setManualOauthModalOpen(false);
          }
        }}
        onOk={handleSubmitManualOauthCallback}
        okText={t("admin.dataSourceOauthManualCallbackConfirm")}
        okButtonProps={{ loading: manualOauthSubmitting }}
        cancelText={t("common.cancel")}
        destroyOnHidden
      >
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Alert
            showIcon
            type="info"
            message={t("admin.dataSourceOauthManualCallbackDesc")}
          />
          <Input.TextArea
            value={manualOauthCallbackValue}
            onChange={(event) =>
              setManualOauthCallbackValue(event.target.value)
            }
            placeholder={t("admin.dataSourceOauthManualCallbackPlaceholder")}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </Space>
      </Modal>
    </>
  );
}
