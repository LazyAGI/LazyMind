import { CloudOutlined } from "@ant-design/icons";
import { Spin } from "antd";
import { useTranslation } from "react-i18next";
import CloudDocumentProviderPanel, {
  CloudDocumentModals,
} from "../components/CloudDocumentProviderPanel";
import DatabaseProviderPanel from "../components/DatabaseProviderPanel";
import { useCloudDocumentProviders } from "../hooks/useCloudDocumentProviders";

export default function CloudDocumentsPage() {
  const { t } = useTranslation();
  const vm = useCloudDocumentProviders();

  return (
    <div className="model-provider-page-content model-provider-service-page">
      <Spin spinning={vm.loading}>
        <section className="model-provider-service-category">
          <div className="model-provider-service-category-top">
            <div className="model-provider-service-category-head">
              <span>
                <CloudOutlined />
              </span>
              <div>
                <h3>{t("modelProvider.cloudDocuments.title")}</h3>
                <p>{t("modelProvider.cloudDocuments.subtitle")}</p>
              </div>
            </div>
          </div>
          <CloudDocumentProviderPanel vm={vm} />
        </section>

        <DatabaseProviderPanel />
      </Spin>
      <CloudDocumentModals vm={vm} />
    </div>
  );
}
