import { ArrowLeftOutlined, GithubOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router-dom";
import GitHubConnectionSection from "../components/GitHubConnectionSection";
import { useCloudDocumentProviders } from "../hooks/useCloudDocumentProviders";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

export default function GitHubAccountPage() {
  const navigate = useNavigate();
  const vm = useCloudDocumentProviders();
  return <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-feishu-page">
    <button type="button" className="model-provider-cloud-doc-breadcrumb" onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}>
      <ArrowLeftOutlined />
      <span>{vm.t("modelProvider.cloudDocuments.backToProviders")}</span>
    </button>
    <GitHubConnectionSection vm={vm} management
      logo={<span className="model-provider-cloud-doc-resource-logo"><GithubOutlined /></span>} />
  </div>;
}
