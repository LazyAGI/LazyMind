import { Alert, Form, Input, Modal, Tag, Tooltip } from "antd";
import { ArrowRightOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { FeishuCredentialHintAlertFromForm } from "@/modules/dataSource/common/FeishuCredentialHintAlert";
import { formatValidFeishuAccountNames } from "@/modules/dataSource/utils/feishuAccount";
import { cloudAuthProviderOptions } from "../constants/cloudProviderOptions";
import {
  CLOUD_DOCUMENTS_FEISHU_SETUP_PATH,
  CLOUD_DOCUMENTS_NOTION_SETUP_PATH,
} from "../utils/cloudDocumentUrls";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";

function getProviderTitle(type: "feishu" | "notion" | "local", t: CloudDocumentProvidersVm["t"]) {
  if (type === "local") {
    return t("modelProvider.cloudDocuments.localTitle");
  }
  if (type === "feishu") {
    return t("modelProvider.cloudDocuments.feishuTitle");
  }
  return t("modelProvider.cloudDocuments.notionTitle");
}

function getProviderDescription(
  type: "feishu" | "notion" | "local",
  t: CloudDocumentProvidersVm["t"],
  vm: CloudDocumentProvidersVm,
) {
  if (type === "local") {
    return t("modelProvider.cloudDocuments.localDesc", { count: vm.localSourceCount });
  }
  if (type === "feishu") {
    if (vm.isFeishuAuthValid) {
      return t("modelProvider.cloudDocuments.feishuConnectedHint", {
        account:
          vm.validFeishuAccounts.length > 0
            ? formatValidFeishuAccountNames(vm.validFeishuAccounts)
            : t("modelProvider.cloudDocuments.feishuConnectedFallback"),
      });
    }
    if (!vm.isFeishuSetupReady) {
      return t("modelProvider.cloudDocuments.feishuLockHint");
    }
    return t("modelProvider.cloudDocuments.feishuAuthReadyHint");
  }

  if (vm.isNotionAuthValid) {
    return t("modelProvider.cloudDocuments.notionConnected", {
      account: vm.notionOauthConnection?.accountName || "Notion workspace",
    });
  }
  if (!vm.isNotionSetupReady) {
    return t("modelProvider.cloudDocuments.notionSetupRequiredHint");
  }
  return t("modelProvider.cloudDocuments.notionAuthPendingHint");
}

export default function CloudDocumentProviderPanel({ vm }: { vm: CloudDocumentProvidersVm }) {
  const {
    t,
    canCreateLocalSource,
    localScanChatEnabled,
    isFeishuAuthValid,
    isNotionAuthValid,
    isFeishuSetupReady,
    isNotionSetupReady,
    handleManageFeishuAuth,
    handleManageLocalSource,
    handleOpenNotionSetup,
  } = vm;

  return (
    <div className="model-provider-cloud-doc-grid">
      {canCreateLocalSource ? (
        <button
          type="button"
          className="model-provider-service-card"
          onClick={handleManageLocalSource}
        >
          <span className="model-provider-service-logo model-provider-service-logo-blue">
            <FolderOpenOutlined className="model-provider-service-logo-icon" />
          </span>
          <div className="model-provider-service-card-copy">
            <div>
              <div className="model-provider-service-title-row">
                <h4>{getProviderTitle("local", t)}</h4>
                <Tag
                  className="model-provider-service-status"
                  color={localScanChatEnabled ? "success" : "default"}
                >
                  {localScanChatEnabled
                    ? t("modelProvider.cloudDocuments.localScanChatEnabledTag")
                    : t("modelProvider.cloudDocuments.localScanChatDisabledTag")}
                </Tag>
              </div>
              <Tooltip placement="topLeft" title={getProviderDescription("local", t, vm)}>
                <span className="model-provider-service-summary-wrap">
                  <p className="model-provider-service-summary">
                    {getProviderDescription("local", t, vm)}
                  </p>
                </span>
              </Tooltip>
            </div>
          </div>
          <span className="model-provider-service-card-arrow" aria-hidden="true">
            <ArrowRightOutlined />
          </span>
        </button>
      ) : null}

      {cloudAuthProviderOptions.map((item) => {
        const isFeishu = item.type === "feishu";
        const isAuthValid = isFeishu ? isFeishuAuthValid : isNotionAuthValid;
        const isSetupReady = isFeishu ? isFeishuSetupReady : isNotionSetupReady;
        const isProviderLocked = !isAuthValid && !isSetupReady;
        const authStatusText = isAuthValid
          ? t("modelProvider.cloudDocuments.authValid")
          : isProviderLocked
            ? t("modelProvider.cloudDocuments.credentialMissing")
            : t("modelProvider.cloudDocuments.authPending");

        return (
          <button
            key={item.type}
            type="button"
            className={`model-provider-service-card${isProviderLocked ? " is-locked" : ""}`}
            onClick={() => {
              if (isFeishu) {
                handleManageFeishuAuth();
                return;
              }
              handleOpenNotionSetup();
            }}
          >
            <span className="model-provider-service-logo model-provider-service-logo-blue">
              {item.logoUrl ? (
                <img
                  alt=""
                  aria-hidden="true"
                  loading="lazy"
                  src={item.logoUrl}
                  onLoad={(event) => {
                    event.currentTarget.classList.add("is-loaded");
                  }}
                  onError={(event) => {
                    event.currentTarget.style.display = "none";
                  }}
                />
              ) : (
                item.icon
              )}
            </span>
            <div className="model-provider-service-card-copy">
              <div>
                <div className="model-provider-service-title-row">
                  <h4>{getProviderTitle(item.type, t)}</h4>
                  <Tag
                    className="model-provider-service-status"
                    color={
                      isAuthValid ? "success" : isProviderLocked ? "default" : "processing"
                    }
                  >
                    {authStatusText}
                  </Tag>
                </div>
                <Tooltip
                  placement="topLeft"
                  title={getProviderDescription(item.type, t, vm)}
                >
                  <span className="model-provider-service-summary-wrap">
                    <p className="model-provider-service-summary">
                      {getProviderDescription(item.type, t, vm)}
                    </p>
                  </span>
                </Tooltip>
              </div>
            </div>
            <span className="model-provider-service-card-arrow" aria-hidden="true">
              <ArrowRightOutlined />
            </span>
          </button>
        );
      })}
    </div>
  );
}

export function CloudDocumentModals({ vm }: { vm: CloudDocumentProvidersVm }) {
  const {
    t,
    feishuSetupForm,
    cloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    setFeishuSetupIntent,
    feishuSetupSubmitting,
    handleSaveFeishuSetup,
  } = vm;

  return (
    <Modal
      title={
        cloudSetupProvider === "feishu"
          ? t("modelProvider.cloudDocuments.feishuCredentialModalTitle")
          : t("modelProvider.cloudDocuments.notionCredentialModalTitle")
      }
      open={feishuSetupModalOpen}
      destroyOnHidden
      onCancel={() => {
        if (feishuSetupSubmitting) {
          return;
        }
        setFeishuSetupModalOpen(false);
        setFeishuSetupIntent(null);
      }}
      onOk={() => {
        void handleSaveFeishuSetup();
      }}
      okText={t("modelProvider.cloudDocuments.credentialSaveAndAuthorize")}
      okButtonProps={{ loading: feishuSetupSubmitting }}
      cancelButtonProps={{ disabled: feishuSetupSubmitting }}
      cancelText={t("common.cancel")}
    >
      <Form form={feishuSetupForm} layout="vertical">
        <Form.Item label={t("modelProvider.cloudDocuments.feishuAccountName")} name="name">
          <Input placeholder={t("modelProvider.cloudDocuments.feishuAccountNamePlaceholder")} />
        </Form.Item>
        <Form.Item
          label={t("modelProvider.cloudDocuments.appId")}
          name="appId"
          rules={[{ required: true, message: t("modelProvider.cloudDocuments.appIdRequired") }]}
        >
          <Input placeholder={t("modelProvider.cloudDocuments.appIdPlaceholder")} />
        </Form.Item>
        <Form.Item
          label={t("modelProvider.cloudDocuments.appSecret")}
          name="appSecret"
          rules={[
            { required: true, message: t("modelProvider.cloudDocuments.appSecretRequired") },
          ]}
        >
          <Input.Password placeholder={t("modelProvider.cloudDocuments.appSecretPlaceholder")} />
        </Form.Item>
        {cloudSetupProvider === "feishu" ? (
          <FeishuCredentialHintAlertFromForm form={feishuSetupForm} />
        ) : (
          <Alert showIcon type="info" message={t("modelProvider.cloudDocuments.notionCredentialHint")} />
        )}
        {cloudSetupProvider !== "feishu" ? (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a
              href={`${CLOUD_DOCUMENTS_NOTION_SETUP_PATH}?from=cloud-documents`}
              target="_blank"
              rel="noreferrer"
            >
              {t("modelProvider.cloudDocuments.notionSetupGuideAction")}
            </a>
            {t("modelProvider.cloudDocuments.notionSetupGuideHint")}
          </p>
        ) : (
          <p style={{ marginTop: 12, marginBottom: 0 }}>
            <a href={CLOUD_DOCUMENTS_FEISHU_SETUP_PATH} target="_blank" rel="noreferrer">
              {t("modelProvider.cloudDocuments.feishuSetupGuideAction")}
            </a>
          </p>
        )}
      </Form>
    </Modal>
  );
}
