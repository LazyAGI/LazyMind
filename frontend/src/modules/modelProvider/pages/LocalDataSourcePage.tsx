import { Button, Spin, Switch, Typography } from "antd";
import { ArrowLeftOutlined, FolderOpenOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import { useLocalDataSourceSettings } from "../hooks/useLocalDataSourceSettings";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

const { Paragraph, Text } = Typography;

export default function LocalDataSourcePage() {
  const navigate = useNavigate();
  const {
    t,
    loading,
    localScanChatEnabled,
    localScanChatSaving,
    localSourceCount,
    handleToggleLocalScanChat,
  } = useLocalDataSourceSettings();

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-local-page">
      <button
        type="button"
        className="model-provider-cloud-doc-breadcrumb"
        onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
      >
        <ArrowLeftOutlined />
        <span>{t("modelProvider.cloudDocuments.backToProviders")}</span>
      </button>

      <Spin spinning={loading}>
        <section className="model-provider-service-category model-provider-cloud-doc-local-section">
          <div className="model-provider-service-category-top">
            <div className="model-provider-service-category-head">
              <span className="model-provider-cloud-doc-local-logo">
                <FolderOpenOutlined />
              </span>
              <div>
                <h3>{t("modelProvider.cloudDocuments.localDetailTitle")}</h3>
                <p>{t("modelProvider.cloudDocuments.localDetailSubtitle")}</p>
              </div>
            </div>
            <Button onClick={() => navigate("/data-sources")}>
              {t("modelProvider.cloudDocuments.localManageDataSources")}
            </Button>
          </div>

          <div className="model-provider-cloud-doc-local-stats">
            <div className="model-provider-cloud-doc-local-stat">
              <Text type="secondary">{t("modelProvider.cloudDocuments.localConnectedCountLabel")}</Text>
              <strong>{localSourceCount}</strong>
            </div>
          </div>

          <div className="model-provider-cloud-doc-setting-card">
            <div className="model-provider-cloud-doc-setting-copy">
              <h4>{t("modelProvider.cloudDocuments.localScanChatSettingTitle")}</h4>
              <Paragraph>{t("modelProvider.cloudDocuments.localScanChatSwitchHint")}</Paragraph>
            </div>
            <Switch
              checked={localScanChatEnabled}
              loading={localScanChatSaving}
              onChange={(checked) => {
                void handleToggleLocalScanChat(checked);
              }}
            />
          </div>
        </section>
      </Spin>
    </div>
  );
}
