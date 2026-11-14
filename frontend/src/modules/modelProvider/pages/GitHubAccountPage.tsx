import { ArrowLeftOutlined, GithubOutlined } from "@ant-design/icons";
import { useLocation, useNavigate } from "react-router-dom";
import { useEffect, useRef, useState } from 'react';
import { AgentAppsAuth } from '@/components/auth';
import { starReturnIntent } from '@/modules/githubStar/intent';
import GitHubConnectionSection from "../components/GitHubConnectionSection";
import { useCloudDocumentProviders } from "../hooks/useCloudDocumentProviders";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

export default function GitHubAccountPage() {
  const navigate = useNavigate();
  const vm = useCloudDocumentProviders();
  const location = useLocation();
  const intent = starReturnIntent(location.state?.githubStarReturn, AgentAppsAuth.getUserInfo()?.userId || '');
  const [cancelled, setCancelled] = useState(false);
  const returned = useRef(false);
  useEffect(() => {
    const session = vm.github.session;
    if (!intent || cancelled || returned.current || session?.status !== 'COMPLETED' || !session.connection_id
      || !vm.github.accounts.some(account => account.connection_id === session.connection_id && account.status === 'ACTIVE')) return;
    returned.current = true;
    navigate(intent.returnTo, { replace: true, state: { githubStarResume: {
      userId: intent.userId, opportunity: intent.opportunity, connectionId: session.connection_id,
    } } });
  }, [vm.github.session, vm.github.accounts, intent, cancelled, navigate]);
  return <div className="model-provider-page-content model-provider-service-page model-provider-cloud-doc-feishu-page">
    <button type="button" className="model-provider-cloud-doc-breadcrumb" onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}>
      <ArrowLeftOutlined />
      <span>{vm.t("modelProvider.cloudDocuments.backToProviders")}</span>
    </button>
    <GitHubConnectionSection vm={vm} management
      preferExistingCredentials={Boolean(intent && !cancelled)} onConnectCancelled={() => setCancelled(true)}
      logo={<span className="model-provider-cloud-doc-resource-logo"><GithubOutlined /></span>} />
  </div>;
}
