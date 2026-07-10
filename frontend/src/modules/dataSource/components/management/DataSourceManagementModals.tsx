import { Alert, Button, Input, Modal, Space, Tag, Typography } from "antd";
import { ArrowRightOutlined, FileTextOutlined, PlusOutlined } from "@ant-design/icons";
import type { DataSourceManagementVm } from "../../hooks/useDataSourceManagement";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import DataSourceProviderPicker from "./DataSourceProviderPicker";
import CloudCredentialSetupModal from "./CloudCredentialSetupModal";

type SourceCreationModalsVm = Pick<
  DataSourceManagementVm,
  | "t"
  | "feishuSetupForm"
  | "cloudSetupProvider"
  | "feishuSetupModalOpen"
  | "setFeishuSetupModalOpen"
  | "feishuSetupSubmitting"
  | "handleSaveFeishuSetup"
  | "handleCancelCloudSetup"
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
  | "authSelectProvider"
  | "handleOpenFeishuGuideFromAuthSelect"
  | "handleOpenNotionGuideFromAuthSelect"
  | "handleAddFeishuAuthFromSelect"
  | "handleAddNotionAuthFromSelect"
  | "validFeishuAccounts"
  | "validNotionAccounts"
  | "handleSelectFeishuAuthConnection"
  | "handleSelectNotionAuthConnection"
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
const FEISHU_LOGO_URL = "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96";
const NOTION_LOGO_URL = "https://www.google.com/s2/favicons?domain=notion.so&sz=96";

export default function DataSourceManagementModals({
  vm,
  titleKey = "admin.dataSourceCreateKnowledgeSource",
  introKey = "admin.dataSourceCreateProviderIntro",
  hideProviderModal = false,
}: DataSourceManagementModalsProps) {
  const {
    t,
    feishuSetupForm,
    cloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    feishuSetupSubmitting,
    handleSaveFeishuSetup,
    handleCancelCloudSetup,
    createProviderModalOpen,
    setCreateProviderModalOpen,
    authSelectModalOpen,
    setAuthSelectModalOpen,
    authSelectProvider,
    handleOpenFeishuGuideFromAuthSelect,
    handleOpenNotionGuideFromAuthSelect,
    handleAddFeishuAuthFromSelect,
    handleAddNotionAuthFromSelect,
    validFeishuAccounts,
    validNotionAccounts,
    handleSelectFeishuAuthConnection,
    handleSelectNotionAuthConnection,
    manualOauthModalOpen,
    setManualOauthModalOpen,
    manualOauthCallbackValue,
    setManualOauthCallbackValue,
    manualOauthSubmitting,
    handleSubmitManualOauthCallback,
  } = vm;

  const isNotionAuthSelect = authSelectProvider === "notion";
  const authAccounts = isNotionAuthSelect ? validNotionAccounts : validFeishuAccounts;
  const authSelectTitleKey = isNotionAuthSelect
    ? "admin.dataSourceSelectNotionAuthTitle"
    : "admin.dataSourceSelectFeishuAuthTitle";
  const authSelectIntroKey = isNotionAuthSelect
    ? "admin.dataSourceSelectNotionAuthIntro"
    : "admin.dataSourceSelectFeishuAuthIntro";
  const authSelectOtherKey = isNotionAuthSelect
    ? "admin.dataSourceSelectNotionAuthOther"
    : "admin.dataSourceSelectFeishuAuthOther";
  const authSelectOtherDescKey = isNotionAuthSelect
    ? "admin.dataSourceSelectNotionAuthOtherDesc"
    : "admin.dataSourceSelectFeishuAuthOtherDesc";
  const authSelectGuideKey = isNotionAuthSelect
    ? "admin.dataSourceNotionSetupGuideAction"
    : "admin.dataSourceFeishuSetupGuideAction";
  const handleOpenGuideFromAuthSelect = isNotionAuthSelect
    ? handleOpenNotionGuideFromAuthSelect
    : handleOpenFeishuGuideFromAuthSelect;
  const handleAddAuthFromSelect = isNotionAuthSelect
    ? handleAddNotionAuthFromSelect
    : handleAddFeishuAuthFromSelect;
  const handleSelectAuthConnection = isNotionAuthSelect
    ? handleSelectNotionAuthConnection
    : handleSelectFeishuAuthConnection;
  const providerLogoUrl = isNotionAuthSelect ? NOTION_LOGO_URL : FEISHU_LOGO_URL;
  const providerLogoClass = isNotionAuthSelect
    ? "data-source-provider-logo data-source-icon-notion"
    : "data-source-provider-logo data-source-icon-feishu";

  return (
    <>
      <CloudCredentialSetupModal
        t={t}
        cloudSetupProvider={cloudSetupProvider}
        feishuSetupForm={feishuSetupForm}
        open={feishuSetupModalOpen}
        submitting={feishuSetupSubmitting}
        onCancel={handleCancelCloudSetup}
        onSave={() => {
          void handleSaveFeishuSetup();
        }}
      />
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
            <span>{t(authSelectTitleKey)}</span>
            <Button
              type="link"
              size="small"
              className="data-source-auth-select-guide"
              icon={<FileTextOutlined />}
              onClick={handleOpenGuideFromAuthSelect}
            >
              {t(authSelectGuideKey)}
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
          {t(authSelectIntroKey)}
        </Paragraph>
        <Space direction="vertical" size={10} style={{ width: "100%" }}>
          {authAccounts.map((account) => (
            <button
              key={account.id}
              type="button"
              className="data-source-auth-option-card"
              onClick={() => {
                if (account.connection) {
                  handleSelectAuthConnection(account.connection);
                }
              }}
            >
              <span className={providerLogoClass}>
                <img
                  alt=""
                  aria-hidden="true"
                  loading="lazy"
                  src={providerLogoUrl}
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
          <button
            type="button"
            className="data-source-auth-option-card data-source-auth-option-card-other"
            onClick={handleAddAuthFromSelect}
          >
            <span className="data-source-provider-logo data-source-auth-option-other-icon">
              <PlusOutlined />
            </span>
            <span className="data-source-provider-card-copy">
              <span className="data-source-provider-title-row">
                <span className="data-source-provider-name">
                  {t(authSelectOtherKey)}
                </span>
              </span>
              <span className="data-source-provider-desc">
                {t(authSelectOtherDescKey)}
              </span>
            </span>
            <span
              className="data-source-provider-card-arrow"
              aria-hidden="true"
            >
              <ArrowRightOutlined />
            </span>
          </button>
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
