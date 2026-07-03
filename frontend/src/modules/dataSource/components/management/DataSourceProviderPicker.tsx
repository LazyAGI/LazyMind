import { ArrowRightOutlined } from "@ant-design/icons";
import { Tag } from "antd";
import {
  getSourceTypeDescription,
  getSourceTypeTitle,
} from "../../utils/status";
import type { DataSourceManagementVm } from "../../hooks/useDataSourceManagement";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import type { SourceType } from "../../constants/types";

type ProviderPickerVm = Pick<
  DataSourceManagementVm | SyncKnowledgeBaseCreationVm,
  | "t"
  | "creatableSourceTypeOptions"
  | "handleCreateProviderSelect"
  | "isFeishuAuthValid"
  | "isNotionAuthValid"
  | "isFeishuSetupReady"
  | "isNotionSetupReady"
>;

interface DataSourceProviderPickerProps {
  vm: ProviderPickerVm;
}

export default function DataSourceProviderPicker({
  vm,
}: DataSourceProviderPickerProps) {
  const {
    t,
    creatableSourceTypeOptions,
    handleCreateProviderSelect,
    isFeishuAuthValid,
    isNotionAuthValid,
    isFeishuSetupReady,
    isNotionSetupReady,
  } = vm;

  return (
    <div className="data-source-create-provider-grid">
      {creatableSourceTypeOptions.map((item) => {
        const isFeishu = item.type === "feishu";
        const isNotion = item.type === "notion";
        const isCloudProvider = isFeishu || isNotion;
        const isAuthValid = isFeishu
          ? isFeishuAuthValid
          : isNotion
            ? isNotionAuthValid
            : false;
        const isSetupReady = isFeishu
          ? isFeishuSetupReady
          : isNotion
            ? isNotionSetupReady
            : true;
        const isProviderLocked =
          isCloudProvider && !isAuthValid && !isSetupReady;
        const authStatusText = isAuthValid
          ? t("admin.dataSourceProviderAuthValid")
          : isSetupReady
            ? t("admin.dataSourceProviderAuthPending")
            : t("admin.dataSourceProviderCredentialMissing");

        return (
          <button
            key={item.type}
            type="button"
            className={`data-source-create-provider-card ${
              isProviderLocked ? "locked" : ""
            }`}
            onClick={() => handleCreateProviderSelect(item.type as SourceType)}
          >
            <span
              className={`data-source-provider-logo data-source-icon-${item.type}`}
            >
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
                {isCloudProvider ? (
                  <Tag
                    color={
                      isAuthValid
                        ? "success"
                        : isSetupReady
                          ? "processing"
                          : "default"
                    }
                  >
                    {authStatusText}
                  </Tag>
                ) : null}
              </span>
              <span className="data-source-provider-desc">
                {isProviderLocked
                  ? isFeishu
                    ? t("admin.dataSourceCreateFeishuAuthRequiredHint")
                    : t("admin.dataSourceNotionSetupRequiredForCreate")
                  : getSourceTypeDescription(item.type, t)}
              </span>
            </span>
            <span
              className="data-source-provider-card-arrow"
              aria-hidden="true"
            >
              <ArrowRightOutlined />
            </span>
          </button>
        );
      })}
    </div>
  );
}
