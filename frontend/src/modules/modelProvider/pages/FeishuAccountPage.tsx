import { Button, Input, Modal, Space } from "antd";
import {
  ArrowLeftOutlined,
  InfoCircleOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import FeishuAccountTable from "../components/feishu/FeishuAccountTable";
import FeishuAccountFormModal from "../components/feishu/FeishuAccountFormModal";
import { useFeishuAccounts } from "../hooks/useFeishuAccounts";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

const FEISHU_LOGO_URL = "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96";

export default function FeishuAccountPage() {
  const navigate = useNavigate();
  const {
    t,
    form,
    accounts,
    accountsLoading,
    chatUpdatingAccountIds,
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
    handleAddAccount,
    addingAccount,
    handleAuthorizeAccount,
    handleDeleteAccount,
    handleToggleChat,
    handleSubmitManualOauthCallback,
  } = useFeishuAccounts();

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-feishu-page">
      <button
        type="button"
        className="model-provider-cloud-doc-breadcrumb"
        onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
      >
        <ArrowLeftOutlined />
        <span>{t("modelProvider.cloudDocuments.backToProviders")}</span>
      </button>

      <section className="model-provider-service-category model-provider-cloud-doc-feishu-section">
        <div className="model-provider-service-category-top">
          <div className="model-provider-service-category-head">
            <span className="model-provider-cloud-doc-feishu-logo">
              <img alt="" aria-hidden="true" loading="lazy" src={FEISHU_LOGO_URL} />
            </span>
            <div>
              <h3>{t("modelProvider.cloudDocuments.feishuAccountManagementTitle")}</h3>
              <p>{t("modelProvider.cloudDocuments.feishuAccountManagementSubtitle")}</p>
            </div>
          </div>
          <Space size={10} wrap className="model-provider-cloud-doc-feishu-actions">
            <Button type="primary" icon={<PlusOutlined />} loading={addingAccount} onClick={handleAddAccount}>
              {t("modelProvider.cloudDocuments.feishuAccountCreate")}
            </Button>
          </Space>
        </div>

        <div className="model-provider-cloud-doc-setup-card">
          <div className="model-provider-cloud-doc-setup-card-main">
            <span className="model-provider-cloud-doc-setup-card-icon" aria-hidden="true">
              <SafetyCertificateOutlined />
            </span>
            <div className="model-provider-cloud-doc-setup-card-copy">
              <h4>{t("modelProvider.cloudDocuments.feishuSetupCardTitle")}</h4>
              <p>{t("modelProvider.cloudDocuments.feishuAccountSecurityHint")}</p>
            </div>
          </div>
          {accounts.length > 1 ? (
            <div className="model-provider-cloud-doc-setup-note">
              <InfoCircleOutlined />
              <span>{t("modelProvider.cloudDocuments.feishuAccountReauthorizeHint")}</span>
            </div>
          ) : null}
        </div>

        <FeishuAccountTable
          t={t}
          accounts={accounts}
          accountsLoading={accountsLoading}
          chatUpdatingAccountIds={chatUpdatingAccountIds}
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
          <p>{t("admin.dataSourceOauthManualCallbackDesc")}</p>
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
