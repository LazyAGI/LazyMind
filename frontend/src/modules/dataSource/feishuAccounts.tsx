import { Alert, Button, Input, Modal, Space, Typography } from "antd";
import {
  ArrowLeftOutlined,
  FileTextOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import { FEISHU_OPEN_PLATFORM_URL } from "./common/FeishuCredentialHintAlert";
import { useFeishuAccounts } from "./hooks/useFeishuAccounts";
import FeishuAccountTable from "./components/feishu/FeishuAccountTable";
import FeishuAccountFormModal from "./components/feishu/FeishuAccountFormModal";
import "./index.scss";

const { Link, Paragraph, Text } = Typography;

export default function FeishuAccountPage() {
  const navigate = useNavigate();
  const {
    t,
    form,
    callbackUrl,
    accounts,
    accountsLoading,
    modalOpen,
    editingAccountId,
    submitting,
    manualOauthModalOpen,
    manualOauthCallbackValue,
    manualOauthSubmitting,
    setModalOpen,
    setEditingAccountId,
    setManualOauthModalOpen,
    setManualOauthCallbackValue,
    openAccountModal,
    handleSaveAccount,
    handleAuthorizeAccount,
    handleDeleteAccount,
    handleToggleChat,
    handleSubmitManualOauthCallback,
  } = useFeishuAccounts();

  return (
    <div className="admin-page data-source-page data-source-feishu-account-page">
      <div className="admin-page-toolbar data-source-page-toolbar">
        <div className="admin-page-toolbar-left data-source-page-toolbar-left">
          <div>
            <Button
              type="link"
              icon={<ArrowLeftOutlined />}
              className="data-source-provider-back-button"
              onClick={() => navigate("/data-sources?view=connectors")}
            >
              {t("admin.dataSourceProviderBack")}
            </Button>
            <h2 className="admin-page-title">
              {t("admin.dataSourceFeishuAccountManagementTitle")}
            </h2>
            <Paragraph className="data-source-page-subtitle">
              {t("admin.dataSourceFeishuAccountManagementSubtitle")}
            </Paragraph>
          </div>
        </div>
        <Space>
          <Button
            icon={<FileTextOutlined />}
            onClick={() => navigate("/data-sources/docs/feishu-setup")}
          >
            {t("admin.dataSourceFeishuSetupGuideAction")}
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => openAccountModal()}
          >
            {t("admin.dataSourceFeishuAccountCreate")}
          </Button>
        </Space>
      </div>

      <section className="data-source-feishu-account-shell">
        <Alert
          showIcon
          type="warning"
          className="data-source-feishu-account-alert"
          message={
            <div className="data-source-feishu-account-alert-message">
              <div>{t("admin.dataSourceFeishuAccountSecurityHint")}</div>
              <div>
                {t("admin.dataSourceFeishuAccountCallbackPrefix")}
                <Link
                  href={FEISHU_OPEN_PLATFORM_URL}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t("admin.dataSourceFeishuAccountOpenPlatform")}
                </Link>
                {t("admin.dataSourceFeishuAccountCallbackMiddle")}
                <Text code copyable={{ text: callbackUrl }}>
                  {callbackUrl}
                </Text>
                {t("admin.dataSourceFeishuAccountCallbackSuffix")}
                <Text className="data-source-feishu-account-alert-highlight">
                  {t("admin.dataSourceFeishuAccountCallbackTarget")}
                </Text>
                {t("admin.dataSourceFeishuAccountCallbackSuffixEnd")}
              </div>
            </div>
          }
        />
        {accounts.length > 1 ? (
          <Alert
            showIcon
            type="info"
            className="data-source-feishu-account-reauth-alert"
            message={t("admin.dataSourceFeishuAccountReauthorizeHint")}
          />
        ) : null}
        <FeishuAccountTable
          t={t}
          accounts={accounts}
          accountsLoading={accountsLoading}
          onAuthorize={handleAuthorizeAccount}
          onEdit={openAccountModal}
          onDelete={handleDeleteAccount}
          onToggleChat={handleToggleChat}
        />
      </section>

      <FeishuAccountFormModal
        t={t}
        open={modalOpen}
        isEditing={Boolean(editingAccountId)}
        submitting={submitting}
        form={form}
        onCancel={() => {
          setModalOpen(false);
          setEditingAccountId(null);
        }}
        onOk={handleSaveAccount}
      />

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
            onChange={(event) => setManualOauthCallbackValue(event.target.value)}
            placeholder={t("admin.dataSourceOauthManualCallbackPlaceholder")}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </Space>
      </Modal>
    </div>
  );
}
