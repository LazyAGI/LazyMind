import { type SelfEvolutionPageView } from "./shared";
import { HistorySessionModal } from "./components/HistorySessions";
import { SelfEvolutionHomeView } from "./components/LaunchViews";
import { SelfEvolutionObservationPage as ObservationPage } from "./components/ObservationPage";
import { SelfEvolutionPageController } from "./components/SelfEvolutionPage";
import { SelfEvolutionWorkbenchView } from "./components/WorkbenchView";

function SelfEvolutionPage({ view }: { view: SelfEvolutionPageView }) {
  return (
    <SelfEvolutionPageController view={view}>
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
  return <ObservationPage />;
}

export default SelfEvolutionHomePage;
