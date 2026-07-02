import { Tag, Tooltip, Typography } from "antd";
import { ArrowRightOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { getSourceTypeTitle } from "../../utils/status";
import { formatValidFeishuAccountNames } from "../../utils/feishuAccount";
import { providerAuthOptions } from "../../constants/sourceTypeOptions";
import type { DataSourceManagementVm } from "../../hooks/useDataSourceManagement";

const { Paragraph, Text } = Typography;

export default function DataSourceProviderPanel({ vm }: { vm: DataSourceManagementVm }) {
  const {
    t,
    canCreateLocalSource,
    localSourceCount,
    localScanChatEnabled,
    localScanChatSaving,
    handleToggleLocalScanChat,
    isFeishuAuthValid,
    isNotionAuthValid,
    isFeishuSetupReady,
    isNotionSetupReady,
    validFeishuAccounts,
    notionOauthConnection,
    handleManageFeishuAuth,
    openSourceCreateWizard,
    openCloudSetupModal,
  } = vm;

  return (
    <main className="data-source-provider-panel">
      <div className="data-source-provider-panel-header">
        <div>
          <Text strong className="data-source-provider-title">
            {t("admin.dataSourceProviderTitle")}
          </Text>
          <Paragraph className="data-source-provider-subtitle">
            {t("admin.dataSourceProviderSubtitle")}
          </Paragraph>
        </div>
      </div>
      <div className="data-source-provider-grid">
        {canCreateLocalSource ? (
          <div className="data-source-local-scan-card">
            <span className="data-source-provider-logo data-source-icon-local">
              <FolderOpenOutlined />
            </span>
            <span className="data-source-provider-card-copy">
              <span className="data-source-provider-title-row">
                <span className="data-source-provider-name">
                  {t("admin.dataSourceLocalScanChatTitle")}
                </span>
              </span>
              <span className="data-source-provider-desc">
                {t("admin.dataSourceLocalScanChatDesc", {
                  count: localSourceCount,
                })}
              </span>
            </span>
            <Tooltip
              title={
                t("admin.dataSourceLocalScanChatSwitchHint")
              }
            >
              <button
                type="button"
                role="switch"
                aria-checked={localScanChatEnabled}
                aria-label={t("admin.dataSourceLocalScanChatSwitchAria")}
                disabled={localScanChatSaving}
                className={`data-source-chat-switch${localScanChatEnabled ? " is-on" : ""}${
                  localScanChatSaving ? " is-disabled" : ""
                }`}
                onClick={() => {
                  void handleToggleLocalScanChat(!localScanChatEnabled);
                }}
              >
                <span className="data-source-chat-switch-thumb" aria-hidden="true" />
                <span className="data-source-chat-switch-label">
                  {localScanChatEnabled
                    ? t("admin.dataSourceLocalScanChatSwitchEnabledStatus")
                    : t("admin.dataSourceLocalScanChatSwitchDisabledStatus")}
                </span>
              </button>
            </Tooltip>
          </div>
        ) : null}
        {providerAuthOptions.map((item) => {
          const isFeishu = item.type === "feishu";
          const isAuthValid = isFeishu ? isFeishuAuthValid : isNotionAuthValid;
          const isSetupReady = isFeishu ? isFeishuSetupReady : isNotionSetupReady;
          const isProviderLocked = !isAuthValid && !isSetupReady;
          const authStatusText = isAuthValid
            ? t("admin.dataSourceProviderAuthValid")
            : isProviderLocked
              ? t("admin.dataSourceProviderCredentialMissing")
              : t("admin.dataSourceProviderAuthPending");
          return (
            <button
              key={item.type}
              type="button"
              className={`data-source-provider-card ${isProviderLocked ? "locked" : ""}`}
              onClick={() => {
                if (isFeishu) {
                  handleManageFeishuAuth();
                  return;
                }
                if (isNotionAuthValid) {
                  openSourceCreateWizard("notion", { connection: notionOauthConnection });
                  return;
                }
                openCloudSetupModal("notion", "create");
              }}
            >
              <span className={`data-source-provider-logo data-source-icon-${item.type}`}>
                {item.logoUrl ? (
                  <img
                    alt=""
                    aria-hidden="true"
                    loading="lazy"
                    src={item.logoUrl}
                    onError={(event) => {
                      event.currentTarget.style.display = "none";
                    }}
                  />
                ) : (
                  item.icon
                )}
              </span>
              <span className="data-source-provider-card-copy">
                <span className="data-source-provider-title-row">
                  <span className="data-source-provider-name">
                    {getSourceTypeTitle(item.type, t)}
                  </span>
                  {item.adminOnly ? (
                    <Tag color="orange">{t("admin.dataSourceAdminOnly")}</Tag>
                  ) : null}
                  {item.type === "feishu" || item.type === "notion" ? (
                    <Tag
                      color={
                        isAuthValid
                          ? "success"
                          : isProviderLocked
                            ? "default"
                            : "processing"
                      }
                    >
                      {authStatusText}
                    </Tag>
                  ) : null}
                </span>
                <span className="data-source-provider-desc">
                  {isAuthValid
                    ? isFeishu
                      ? t("admin.dataSourceFeishuAuthConnectedHint", {
                          account:
                            validFeishuAccounts.length > 0
                              ? formatValidFeishuAccountNames(validFeishuAccounts)
                              : t("admin.dataSourceFeishuConnectedAccountFallback"),
                        })
                      : t("admin.dataSourceNotionConnected", {
                          account: notionOauthConnection?.accountName || "Notion workspace",
                        })
                    : isProviderLocked
                      ? isFeishu
                        ? t("admin.dataSourceFeishuLockHint")
                        : t("admin.dataSourceNotionSetupRequiredHint")
                      : isFeishu
                        ? t("admin.dataSourceFeishuAuthReadyHint")
                        : t("admin.dataSourceNotionAuthPendingHint")}
                </span>
              </span>
              <span className="data-source-provider-card-arrow" aria-hidden="true">
                <ArrowRightOutlined />
              </span>
            </button>
          );
        })}
      </div>
    </main>
  );
}
