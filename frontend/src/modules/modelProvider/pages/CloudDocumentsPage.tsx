import { Skeleton } from "antd";
import { useTranslation } from "react-i18next";
import CloudDocumentProviderPanel, {
  CloudDocumentModals,
} from "../components/CloudDocumentProviderPanel";
import { useCloudDocumentProviders } from "../hooks/useCloudDocumentProviders";

export default function CloudDocumentsPage() {
  const { t } = useTranslation();
  const vm = useCloudDocumentProviders();
  const providerTotal = vm.canCreateLocalSource ? 3 : 2;
  const providerReadyCount =
    (vm.canCreateLocalSource && vm.localSourceCount > 0 ? 1 : 0) +
    (vm.isFeishuAuthValid ? 1 : 0) +
    (vm.isNotionAuthValid ? 1 : 0);

  return (
    <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-hub">
      <header className="model-provider-cloud-doc-page-header">
        <div className="model-provider-cloud-doc-page-heading">
          <h1>{t("modelProvider.cloudDocuments.title")}</h1>
          <p>{t("modelProvider.cloudDocuments.subtitle")}</p>
        </div>
        <div
          className="model-provider-cloud-doc-overview"
          aria-label={t("modelProvider.cloudDocuments.connectedProviderSummary", {
            connected: providerReadyCount,
            total: providerTotal,
          })}
        >
          <span>{t("modelProvider.cloudDocuments.overview")}</span>
          {vm.loading ? (
            <Skeleton.Button active className="model-provider-cloud-doc-overview-skeleton" />
          ) : (
            <strong>{providerReadyCount} / {providerTotal}</strong>
          )}
          <small>{t("modelProvider.cloudDocuments.providerReadySuffix")}</small>
        </div>
      </header>

      <CloudDocumentProviderPanel vm={vm} />
      <CloudDocumentModals vm={vm} />
    </div>
  );
}
