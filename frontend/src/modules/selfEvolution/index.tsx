import { useSyncExternalStore } from "react";
import { useParams } from "react-router-dom";
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from "@/components/auth";
import { type SelfEvolutionPageView } from "./shared";
import { HistorySessionModal } from "./components/HistorySessions";
import { AlgorithmVersionManagementPage as AlgorithmManagementPage } from "./components/AlgorithmVersionManagementPage";
import { RoutingStrategyManagementPage } from "./components/RoutingStrategyManagementPage";
import { RouterTrafficStatsPage } from "./components/RouterTrafficStatsPanel";
import { SelfEvolutionHomeView } from "./components/LaunchViews";
import { SelfEvolutionObservationPage as ObservationPage } from "./components/ObservationPage";
import { SelfEvolutionPageController } from "./components/SelfEvolutionPage";
import { SelfEvolutionWorkbenchView } from "./components/WorkbenchView";

const subscribeAccount = (callback: () => void) => {
  window.addEventListener(AUTH_USER_CHANGE_EVENT, callback);
  window.addEventListener("storage", callback);
  return () => { window.removeEventListener(AUTH_USER_CHANGE_EVENT, callback); window.removeEventListener("storage", callback); };
};
const getAccountKey = () => {
  const user = AgentAppsAuth.getUserInfo();
  return `${user?.userId || ""}:${user?.tenantId || user?.tenant_id || ""}`;
};

function SelfEvolutionPage({ view }: { view: SelfEvolutionPageView }) {
  const account = useSyncExternalStore(subscribeAccount, getAccountKey);
  const { threadId } = useParams();
  return (
    <SelfEvolutionPageController key={`${account}:${threadId || view}`} view={view}>
      {({
        isWorkbenchVisible,
        homeViewProps,
        homeHistoryModalProps,
        workbenchViewProps,
      }) => {
        if (isWorkbenchVisible) {
          return <SelfEvolutionWorkbenchView {...workbenchViewProps} />;
        }

        return (
          <>
            <SelfEvolutionHomeView {...homeViewProps} />
            <HistorySessionModal {...homeHistoryModalProps} />
          </>
        );
      }}
    </SelfEvolutionPageController>
  );
}

export function SelfEvolutionHomePage() {
  return <SelfEvolutionPage view="home" />;
}

export function SelfEvolutionDetailPage() {
  return <SelfEvolutionPage view="detail" />;
}

export function SelfEvolutionObservationPage() {
  const account = useSyncExternalStore(subscribeAccount, getAccountKey);
  const { threadId, kind } = useParams();
  return <ObservationPage key={`${account}:${threadId}:${kind}`} />;
}

export function SelfEvolutionAlgorithmManagementPage() {
  return <AlgorithmManagementPage />;
}

export function SelfEvolutionRoutingStrategyPage() {
  return <RoutingStrategyManagementPage />;
}

export function SelfEvolutionTrafficStatsPage() {
  return <RouterTrafficStatsPage />;
}

export default SelfEvolutionHomePage;
