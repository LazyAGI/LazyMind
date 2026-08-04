import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { ProcessActivitySection } from "./ProcessActivitySection";
import type { EvoProcessDashboard, EvoCaseProgressItem } from "../../shared";

function makeDashboard(overrides: Partial<EvoProcessDashboard> = {}): EvoProcessDashboard {
  return {
    overview: [],
    recentActivities: [],
    recentActivityTotal: 0,
    cutoverActivities: [],
    cutoverCompleted: false,
    caseProgressGroups: [],
    ...overrides,
  };
}

function makeActivity(overrides: Partial<EvoProcessDashboard["recentActivities"][number]> = {}) {
  return {
    key: "act-1",
    title: "Started dataset generation",
    detail: "Generating cases",
    time: "10:00",
    tone: "normal" as const,
    ...overrides,
  };
}

function makeCaseItem(overrides: Partial<EvoCaseProgressItem> = {}): EvoCaseProgressItem {
  return {
    caseId: "case-1",
    title: "Case One",
    completed: 1,
    total: 2,
    status: "running",
    steps: [{ key: "s1", label: "step 1", status: "done" }],
    artifactKind: "datasets",
    artifactId: "artifact-1",
    artifactLabel: "view",
    ...overrides,
  };
}

const getStepStatusLabel = (status: string) => `label-${status}`;

describe("ProcessActivitySection", () => {
  it("renders the key events section title and shows the empty hint when there are no activities", () => {
    renderWithProviders(
      <ProcessActivitySection
        processDashboard={makeDashboard()}
        selectedStageActivities={[]}
        visibleKeyActivities={[]}
        activeStageLabel="Dataset"
        getStepStatusLabel={getStepStatusLabel}
        onOpenArtifact={vi.fn()}
        onOpenCaseArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.keyEventsSectionTitle")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.activityEmptyDefault")).toBeInTheDocument();
  });

  it("renders activity rows with an action button when the stage is done and an artifact kind is present", () => {
    const onOpenArtifact = vi.fn();
    const activity = makeActivity({ stage: "dataset", artifactKind: "datasets", artifactLabel: "View dataset" });
    renderWithProviders(
      <ProcessActivitySection
        processDashboard={makeDashboard({
          overview: [{ step: { id: "dataset", title: "Dataset", desc: "", status: "done" }, stage: "dataset", eventCount: 1 }],
        })}
        selectedStageActivities={[activity]}
        visibleKeyActivities={[activity]}
        activeStageLabel="Dataset"
        getStepStatusLabel={getStepStatusLabel}
        onOpenArtifact={onOpenArtifact}
        onOpenCaseArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("Started dataset generation")).toBeInTheDocument();
    fireEvent.click(screen.getByText("View dataset"));
    expect(onOpenArtifact).toHaveBeenCalledWith("datasets");
  });

  it("renders the case progress panel when activeCaseProgressGroup is provided", () => {
    renderWithProviders(
      <ProcessActivitySection
        processDashboard={makeDashboard()}
        activeCaseProgressGroup={{ stage: "dataset", title: "Dataset cases", pageSize: 10, cases: [makeCaseItem()] }}
        selectedStageActivities={[]}
        visibleKeyActivities={[]}
        activeStageLabel="Dataset"
        getStepStatusLabel={getStepStatusLabel}
        onOpenArtifact={vi.fn()}
        onOpenCaseArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("Case One")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.displayByCasePaged")).toBeInTheDocument();
  });

  it("calls onOpenCaseArtifact with the case details when the view detail button is clicked", () => {
    const onOpenCaseArtifact = vi.fn();
    renderWithProviders(
      <ProcessActivitySection
        processDashboard={makeDashboard()}
        activeCaseProgressGroup={{ stage: "dataset", title: "Dataset cases", pageSize: 10, cases: [makeCaseItem()] }}
        selectedStageActivities={[]}
        visibleKeyActivities={[]}
        activeStageLabel="Dataset"
        getStepStatusLabel={getStepStatusLabel}
        onOpenArtifact={vi.fn()}
        onOpenCaseArtifact={onOpenCaseArtifact}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.viewDetail"));
    expect(onOpenCaseArtifact).toHaveBeenCalledWith("datasets", "artifact-1", "Case One · view", "case-1");
  });

  it("renders the debug log details with the recent activity total count", () => {
    renderWithProviders(
      <ProcessActivitySection
        processDashboard={makeDashboard({ recentActivityTotal: 7 })}
        selectedStageActivities={[]}
        visibleKeyActivities={[]}
        activeStageLabel="Dataset"
        getStepStatusLabel={getStepStatusLabel}
        onOpenArtifact={vi.fn()}
        onOpenCaseArtifact={vi.fn()}
      />,
    );
    expect(
      screen.getByText("selfEvolutionRun.debugLogTitle"),
    ).toBeInTheDocument();
  });
});
