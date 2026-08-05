import { ArrowRightOutlined, GoogleOutlined } from "@ant-design/icons";
import { Tag } from "antd";
import { useNavigate } from "react-router-dom";
import {
  getSourceTypeDescription,
  getSourceTypeTitle,
} from "../../utils/status";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import { CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH } from "@/modules/modelProvider/utils/cloudDocumentUrls";
import type { SourceType } from "../../constants/types";

type ProviderPickerVm = Pick<
  SyncKnowledgeBaseCreationVm,
  | "t"
  | "creatableSourceTypeOptions"
  | "handleCreateProviderSelect"
  | "isFeishuAuthValid"
  | "isNotionAuthValid"
  | "isGoogleDriveAuthValid"
  | "isFeishuSetupReady"
  | "isNotionSetupReady"
>;

interface DataSourceProviderPickerProps {
  vm: ProviderPickerVm;
  showGoogleDrive?: boolean;
}

export default function DataSourceProviderPicker({
  vm,
  showGoogleDrive = false,
}: DataSourceProviderPickerProps) {
  const navigate = useNavigate();
  const {
    t,
    creatableSourceTypeOptions,
    handleCreateProviderSelect,
    isFeishuAuthValid,
    isNotionAuthValid,
    isGoogleDriveAuthValid,
    isFeishuSetupReady,
    isNotionSetupReady,
  } = vm;
  const providerOptions = showGoogleDrive
    ? [
        ...creatableSourceTypeOptions,
        {
          type: "googledrive" as const,
          icon: <GoogleOutlined />,
          logoUrl: undefined,
          adminOnly: false,
        },
      ]
    : creatableSourceTypeOptions;

  return (
    <div className="data-source-create-provider-grid">
      {providerOptions.map((item) => {
        const isFeishu = item.type === "feishu";
        const isNotion = item.type === "notion";
        const isGoogleDrive = item.type === "googledrive";
        const isCloudProvider = isFeishu || isNotion || isGoogleDrive;
        const isAuthValid = isFeishu
          ? isFeishuAuthValid
          : isNotion
            ? isNotionAuthValid
            : isGoogleDriveAuthValid;
        const isSetupReady = isFeishu
          ? isFeishuSetupReady
          : isNotion
            ? isNotionSetupReady
            : true;
        const isProviderLocked = isGoogleDrive
          ? !isAuthValid
          : isCloudProvider && !isAuthValid && !isSetupReady;
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
            onClick={() => {
              if (isGoogleDrive) {
                navigate(CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH);
                return;
              }
              handleCreateProviderSelect(item.type as SourceType);
            }}
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
                  {isGoogleDrive
                    ? t("admin.dataSourceTypeGoogleDrive")
                    : getSourceTypeTitle(item.type as SourceType, t)}
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
                {isGoogleDrive
                  ? t("admin.dataSourceGoogleDriveSetupHint")
                  : isProviderLocked
                  ? isFeishu
                    ? t("admin.dataSourceCreateFeishuAuthRequiredHint")
                    : t("admin.dataSourceNotionSetupRequiredForCreate")
                  : getSourceTypeDescription(item.type as SourceType, t)}
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
