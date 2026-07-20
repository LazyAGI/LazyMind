import { Alert, Form, Input, Modal, Skeleton, Tag } from "antd";
import { ArrowRightOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { FeishuCredentialHintAlertFromForm } from "@/modules/dataSource/common/FeishuCredentialHintAlert";
import { formatValidFeishuAccountNames } from "@/modules/dataSource/utils/feishuAccount";
import { cloudAuthProviderOptions, cloudProviderOptions } from "../constants/cloudProviderOptions";
import {
  CLOUD_DOCUMENTS_FEISHU_SETUP_PATH,
  CLOUD_DOCUMENTS_NOTION_SETUP_PATH,
} from "../utils/cloudDocumentUrls";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";

function getProviderTitle(
  type: "feishu" | "notion" | "local" | "googledrive",
  t: CloudDocumentProvidersVm["t"],
) {
  if (type === "local") {
    return t("modelProvider.cloudDocuments.localTitle");
  }
  if (type === "feishu") {
    return t("modelProvider.cloudDocuments.feishuTitle");
  }
  if (type === "googledrive") {
    return t("modelProvider.external.googleDriveTitle");
  }
  return t("modelProvider.cloudDocuments.notionTitle");
}

function getProviderDescription(
  type: "feishu" | "notion" | "local" | "googledrive",
  t: CloudDocumentProvidersVm["t"],
  vm: CloudDocumentProvidersVm,
) {
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
  if (type === "googledrive") {
    return vm.googleDriveConnection
      ? t("admin.dataSourceGoogleDriveConnected", {
          account: vm.googleDriveConnection.accountName,
        })
      : t("modelProvider.external.googleDriveDesc");
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
    isFeishuAuthValid,
    isNotionAuthValid,
    isGoogleDriveAuthValid,
    isFeishuSetupReady,
    isNotionSetupReady,
    handleManageFeishuAuth,
    handleManageLocalSource,
    handleManageGoogleDrive,
    handleOpenNotionSetup,
  } = vm;

  if (vm.loading) {
    return (
      <div
        className="model-provider-cloud-doc-grid"
        aria-busy="true"
      >
        {cloudProviderOptions
          .filter((item) => item.type !== "local" || canCreateLocalSource)
          .map((item) => (
            <div className="model-provider-cloud-doc-skeleton" key={item.type}>
              <Skeleton active avatar={{ shape: "square", size: 44 }} paragraph={{ rows: 1 }} />
            </div>
          ))}
      </div>
    );
  }

  return (
    <div className="model-provider-cloud-doc-grid">
      {canCreateLocalSource ? (
        <button
          type="button"
          className="model-provider-cloud-doc-resource-row"
          onClick={handleManageLocalSource}
        >
          <span className="model-provider-cloud-doc-resource-logo">
            <FolderOpenOutlined />
          </span>
          <div className="model-provider-cloud-doc-resource-copy">
            <h2>{getProviderTitle("local", t)}</h2>
            <p>{t("modelProvider.cloudDocuments.localDetailSubtitle")}</p>
          </div>
          <div className="model-provider-cloud-doc-directory-count">
            <strong>{vm.localSourceCount}</strong>
            <span>{t("modelProvider.cloudDocuments.directoryCountUnit")}</span>
          </div>
          <span className="model-provider-cloud-doc-resource-action">
            {t("modelProvider.cloudDocuments.manageLocal")}
            <ArrowRightOutlined />
          </span>
        </button>
      ) : null}

      {cloudAuthProviderOptions.map((item) => {
        const isFeishu = item.type === "feishu";
        const isGoogleDrive = item.type === "googledrive";
        const isAuthValid = isFeishu
          ? isFeishuAuthValid
          : isGoogleDrive
            ? isGoogleDriveAuthValid
            : isNotionAuthValid;
        const isSetupReady = isFeishu ? isFeishuSetupReady : isNotionSetupReady;
        const isProviderLocked = !isGoogleDrive && !isAuthValid && !isSetupReady;
        const authStatusText = isAuthValid
          ? t("modelProvider.cloudDocuments.authValid")
          : isProviderLocked
            ? t("modelProvider.cloudDocuments.credentialMissing")
            : t("modelProvider.cloudDocuments.authPending");

        return (
          <button
            key={item.type}
            type="button"
            className={`model-provider-cloud-doc-resource-row${isProviderLocked ? " is-locked" : ""}`}
            onClick={() => {
              if (isFeishu) {
                handleManageFeishuAuth();
                return;
              }
              if (isGoogleDrive) {
                handleManageGoogleDrive();
                return;
              }
              handleOpenNotionSetup();
            }}
          >
            <span className="model-provider-cloud-doc-resource-logo">
              <span className="model-provider-cloud-doc-resource-fallback-icon">
                {item.icon}
              </span>
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
              ) : null}
            </span>
            <div className="model-provider-cloud-doc-resource-copy">
              <h2>{getProviderTitle(item.type, t)}</h2>
              <p>{getProviderDescription(item.type, t, vm)}</p>
            </div>
            <Tag
              className="model-provider-cloud-doc-resource-status"
              color={
                isAuthValid ? "success" : isProviderLocked ? "default" : "processing"
              }
            >
              {authStatusText}
            </Tag>
            <span className="model-provider-cloud-doc-resource-action">
              {isAuthValid
                ? t("modelProvider.cloudDocuments.manageAccount")
                : t("modelProvider.cloudDocuments.configureConnection")}
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
