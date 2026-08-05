import type { MouseEvent, ReactNode } from "react";
import { Alert, Form, Input, Modal, Skeleton, Tag } from "antd";
import {
  ArrowRightOutlined,
  BulbOutlined,
  CheckCircleFilled,
  FolderOpenOutlined,
} from "@ant-design/icons";
import { Link } from "react-router-dom";
import { FeishuCredentialHintAlertFromForm } from "@/modules/dataSource/common/FeishuCredentialHintAlert";
import { formatValidFeishuAccountNames } from "@/modules/dataSource/utils/feishuAccount";
import {
  CLOUD_CAPABILITY_I18N_KEYS,
  CLOUD_QUICK_ACTION_PATHS,
  cloudProviderCapabilityConfigs,
  type CloudCapabilityId,
  type CloudQuickActionId,
} from "../constants/cloudProviderCapabilities";
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
  if (type === "local") {
    return t("modelProvider.cloudDocuments.localDetailSubtitle");
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

function ProviderCapabilityGuide({
  type,
  isEnabled,
  t,
}: {
  type: "feishu" | "notion" | "local" | "googledrive";
  isEnabled: boolean;
  t: CloudDocumentProvidersVm["t"];
}) {
  const config = cloudProviderCapabilityConfigs[type];
  const capabilityIds: CloudCapabilityId[] = isEnabled
    ? config.enabledCapabilities
    : config.previewCapabilities;
  const scenarioKey = isEnabled ? config.scenarioKey : config.previewScenarioKey;
  const quickActions: CloudQuickActionId[] = isEnabled ? config.quickActions : [];

  return (
    <div className="model-provider-cloud-doc-capability-guide">
      <div className="model-provider-cloud-doc-capability-row">
        <span
          className={`model-provider-cloud-doc-capability-label${isEnabled ? " is-enabled" : ""}`}
        >
          {isEnabled ? <CheckCircleFilled aria-hidden="true" /> : null}
          {isEnabled
            ? t("modelProvider.cloudDocuments.enabledCapabilitiesLabel")
            : type === "local"
              ? t("modelProvider.cloudDocuments.configuredCapabilitiesLabel")
              : t("modelProvider.cloudDocuments.previewCapabilitiesLabel")}
        </span>
        <div className="model-provider-cloud-doc-capability-list">
          {capabilityIds.map((id) => (
            <span key={id} className="model-provider-cloud-doc-capability-item">
              {t(CLOUD_CAPABILITY_I18N_KEYS[id])}
            </span>
          ))}
        </div>
      </div>
      <p className="model-provider-cloud-doc-scenario">
        <BulbOutlined aria-hidden="true" />
        <span>{t(scenarioKey)}</span>
      </p>
      {quickActions.length > 0 ? (
        <div className="model-provider-cloud-doc-quick-actions">
          {quickActions.map((action) => (
            <Link
              key={action}
              className="model-provider-cloud-doc-quick-action"
              to={CLOUD_QUICK_ACTION_PATHS[action]}
              onClick={(event: MouseEvent<HTMLAnchorElement>) => event.stopPropagation()}
            >
              {action === "knowledge"
                ? t("modelProvider.cloudDocuments.goKnowledge")
                : t("modelProvider.cloudDocuments.goChat")}
              <ArrowRightOutlined aria-hidden="true" />
            </Link>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ProviderLogo({
  type,
  icon,
  logoUrl,
}: {
  type: string;
  icon: ReactNode;
  logoUrl?: string;
}) {
  if (type === "local") {
    return (
      <span className="model-provider-cloud-doc-resource-logo">
        <FolderOpenOutlined />
      </span>
    );
  }
  return (
    <span className="model-provider-cloud-doc-resource-logo">
      <span className="model-provider-cloud-doc-resource-fallback-icon">{icon}</span>
      {logoUrl ? (
        <img
          alt=""
          aria-hidden="true"
          loading="lazy"
          src={logoUrl}
          onLoad={(event) => {
            event.currentTarget.classList.add("is-loaded");
          }}
          onError={(event) => {
            event.currentTarget.style.display = "none";
          }}
        />
      ) : null}
    </span>
  );
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
      <div className="model-provider-cloud-doc-grid" aria-busy="true">
        {cloudProviderOptions
          .filter((item) => item.type !== "local" || canCreateLocalSource)
          .map((item) => (
            <div className="model-provider-cloud-doc-skeleton" key={item.type}>
              <Skeleton active avatar={{ shape: "square", size: 44 }} paragraph={{ rows: 3 }} />
            </div>
          ))}
      </div>
    );
  }

  return (
    <div className="model-provider-cloud-doc-grid">
      {canCreateLocalSource ? (
        <div className="model-provider-cloud-doc-resource-row">
          <ProviderLogo type="local" icon={<FolderOpenOutlined />} />
          <div className="model-provider-cloud-doc-resource-copy">
            <h2>{getProviderTitle("local", t)}</h2>
            <p>{getProviderDescription("local", t, vm)}</p>
            <ProviderCapabilityGuide
              type="local"
              isEnabled={vm.localSourceCount > 0}
              t={t}
            />
          </div>
          <div className="model-provider-cloud-doc-resource-controls">
            <div className="model-provider-cloud-doc-directory-count">
              <strong>{vm.localSourceCount}</strong>
              <span>{t("modelProvider.cloudDocuments.directoryCountUnit")}</span>
            </div>
            <button
              type="button"
              className="model-provider-cloud-doc-resource-action"
              onClick={handleManageLocalSource}
            >
              {t("modelProvider.cloudDocuments.manageLocal")}
              <ArrowRightOutlined />
            </button>
          </div>
        </div>
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

        const handleManage = () => {
          if (isFeishu) {
            handleManageFeishuAuth();
            return;
          }
          if (isGoogleDrive) {
            handleManageGoogleDrive();
            return;
          }
          handleOpenNotionSetup();
        };

        return (
          <div
            key={item.type}
            className={`model-provider-cloud-doc-resource-row${isProviderLocked ? " is-locked" : ""}`}
          >
            <ProviderLogo type={item.type} icon={item.icon} logoUrl={item.logoUrl} />
            <div className="model-provider-cloud-doc-resource-copy">
              <h2>{getProviderTitle(item.type, t)}</h2>
              <p>{getProviderDescription(item.type, t, vm)}</p>
              <ProviderCapabilityGuide type={item.type} isEnabled={isAuthValid} t={t} />
            </div>
            <div className="model-provider-cloud-doc-resource-controls">
              <Tag
                className="model-provider-cloud-doc-resource-status"
                color={
                  isAuthValid ? "success" : isProviderLocked ? "default" : "processing"
                }
              >
                {authStatusText}
              </Tag>
              <button
                type="button"
                className="model-provider-cloud-doc-resource-action"
                onClick={handleManage}
              >
                {isAuthValid
                  ? t("modelProvider.cloudDocuments.manageAccount")
                  : t("modelProvider.cloudDocuments.configureConnection")}
                <ArrowRightOutlined />
              </button>
            </div>
          </div>
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
