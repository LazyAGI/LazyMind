import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../test/testUtils";
import StateGraphEditor from "./index";

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssets: vi.fn().mockResolvedValue([]),
}));

describe("StateGraphEditor", () => {
  it("renders the empty-state hint for a fresh statemachine tab", async () => {
    renderWithProviders(<StateGraphEditor />);
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.sgeEmptyStateTitle")).toBeInTheDocument();
    });
  });

  it("calls onClose when the back button is clicked", async () => {
    const onClose = vi.fn();
    renderWithProviders(<StateGraphEditor onClose={onClose} />);
    fireEvent.click(screen.getByLabelText("selfEvolutionRun.sgeBackAriaLabel"));
    expect(onClose).toHaveBeenCalled();
  });

  it("switches to the UI tab and renders the UiEditorPanel", async () => {
    renderWithProviders(<StateGraphEditor />);
    await waitFor(() => expect(screen.getByText("selfEvolutionRun.sgeEmptyStateTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("selfEvolutionRun.sgeTabUi"));
    expect(screen.getByText("selfEvolutionRun.uiWysiwygEmptyHint")).toBeInTheDocument();
  });

  it("switches to the scenario tab and renders the empty scenario hint", async () => {
    renderWithProviders(<StateGraphEditor />);
    await waitFor(() => expect(screen.getByText("selfEvolutionRun.sgeEmptyStateTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("selfEvolutionRun.sgeTabScenario"));
    expect(document.querySelector(".sge-scenario-preview")).toBeInTheDocument();
  });

  it("enters code mode and shows the core file tree", async () => {
    renderWithProviders(<StateGraphEditor />);
    await waitFor(() => expect(screen.getByText("selfEvolutionRun.sgeEmptyStateTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("selfEvolutionRun.sgeViewCode"));
    expect(screen.getByText("state.yml")).toBeInTheDocument();
    expect(screen.getByText("plugin.yaml")).toBeInTheDocument();
    expect(screen.getByText("scenario.md")).toBeInTheDocument();
  });

  it("shows the readonly badge and hides the add-step button when readonly", async () => {
    renderWithProviders(<StateGraphEditor readonly />);
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.sgeReadonlyBadge")).toBeInTheDocument();
    });
    expect(screen.queryByText("selfEvolutionRun.sgeAddStepBtn")).not.toBeInTheDocument();
  });

  it("opens the plugin info modal when the config button is clicked", async () => {
    renderWithProviders(<StateGraphEditor />);
    await waitFor(() => expect(screen.getByText("selfEvolutionRun.sgeEmptyStateTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("selfEvolutionRun.sgePluginConfigBtn"));
    await waitFor(() => {
      expect(document.querySelector(".ant-modal")).toBeInTheDocument();
    });
  });

  it("toggles the artifacts panel via the artifacts button", async () => {
    renderWithProviders(<StateGraphEditor />);
    await waitFor(() => expect(screen.getByText("selfEvolutionRun.sgeEmptyStateTitle")).toBeInTheDocument());
    fireEvent.click(screen.getByText("selfEvolutionRun.sgeArtifactsBtn"));
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.artifactPanelTitle")).toBeInTheDocument();
    });
  });
});
