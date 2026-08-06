import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import { CutoverDecisionCard } from "./CutoverDecisionCard";
import type { EvoProcessDashboard } from "../../shared";

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

describe("CutoverDecisionCard", () => {
  it("shows the ab-test passed state and confirm action when cutover is not completed", () => {
    renderWithProviders(
      <CutoverDecisionCard
        processDashboard={makeDashboard()}
        checkpointDecisionPrompt={{ message: "please confirm", command: "confirm-cutover" }}
        cutoverDecisionEvidence={[]}
        isSendingMessage={false}
        onSend={vi.fn()}
        onOpenArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.abtestPassed")).toBeInTheDocument();
    expect(screen.getByText("confirm-cutover")).toBeInTheDocument();
  });

  it("shows the cutover done state and hides the confirm action when cutover is completed", () => {
    renderWithProviders(
      <CutoverDecisionCard
        processDashboard={makeDashboard({ cutoverCompleted: true })}
        cutoverDecisionEvidence={[]}
        isSendingMessage={false}
        onSend={vi.fn()}
        onOpenArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.candidateCutoverDone")).toBeInTheDocument();
    expect(screen.queryByText("selfEvolutionRun.confirmCutover")).not.toBeInTheDocument();
  });

  it("renders cutover decision evidence items when present", () => {
    renderWithProviders(
      <CutoverDecisionCard
        processDashboard={makeDashboard({
          cutoverActivities: [{ key: "e1", title: "Evidence title", detail: "Evidence detail", time: "10:00", tone: "normal" }],
        })}
        cutoverDecisionEvidence={[
          { key: "e1", title: "Evidence title", detail: "Evidence detail", time: "10:00", tone: "normal" },
        ]}
        isSendingMessage={false}
        onSend={vi.fn()}
        onOpenArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("Evidence title")).toBeInTheDocument();
    expect(screen.getByText("Evidence detail")).toBeInTheDocument();
  });

  it("disables the confirm button when there is no pending command", () => {
    renderWithProviders(
      <CutoverDecisionCard
        processDashboard={makeDashboard()}
        cutoverDecisionEvidence={[]}
        isSendingMessage={false}
        onSend={vi.fn()}
        onOpenArtifact={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.confirmCutover")).toBeDisabled();
  });

  it("calls onOpenArtifact with abtests when clicking the view detail button", () => {
    const onOpenArtifact = vi.fn();
    renderWithProviders(
      <CutoverDecisionCard
        processDashboard={makeDashboard()}
        cutoverDecisionEvidence={[]}
        isSendingMessage={false}
        onSend={vi.fn()}
        onOpenArtifact={onOpenArtifact}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.viewABTestDetail"));
    expect(onOpenArtifact).toHaveBeenCalledWith("abtests");
  });

  it("calls onSend with the pending command after confirming the popconfirm", async () => {
    const onSend = vi.fn();
    renderWithProviders(
      <CutoverDecisionCard
        processDashboard={makeDashboard()}
        checkpointDecisionPrompt={{ message: "please confirm", command: "confirm-cutover" }}
        cutoverDecisionEvidence={[]}
        isSendingMessage={false}
        onSend={onSend}
        onOpenArtifact={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText("confirm-cutover"));
    const okButton = await screen.findByText("selfEvolutionRun.confirmCutover");
    fireEvent.click(okButton);
    await waitFor(() => expect(onSend).toHaveBeenCalledWith("confirm-cutover"));
  });
});
