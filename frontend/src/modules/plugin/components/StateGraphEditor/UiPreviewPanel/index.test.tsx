import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import UiPreviewPanel from "./index";
import type { PluginModel } from "../core/pluginModel";

function baseModel(overrides: Partial<PluginModel> = {}): PluginModel {
  return { id: "p", name: "P", steps: [], slots: [], ...overrides };
}

describe("UiPreviewPanel", () => {
  it("shows an empty state when the model has no ui tabs", () => {
    renderWithProviders(<UiPreviewPanel model={baseModel()} />);
    expect(screen.getByText("selfEvolutionRun.uiPreviewNoLayout")).toBeInTheDocument();
  });

  it("shows an empty state when ui.tabs is an empty array", () => {
    renderWithProviders(<UiPreviewPanel model={baseModel({ ui: { tabs: [] } })} />);
    expect(screen.getByText("selfEvolutionRun.uiPreviewNoLayout")).toBeInTheDocument();
  });

  it("renders tab labels and a hint when a tab has no slots", () => {
    const model = baseModel({
      ui: { tabs: [{ id: "tab1", label: "Overview", slots: [] }] },
    });
    renderWithProviders(<UiPreviewPanel model={model} />);
    expect(screen.getByText("Overview")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.uiPreviewNoSlots")).toBeInTheDocument();
  });

  it("renders a slot card with its label, type, and list marker", () => {
    const model = baseModel({
      slots: [{ id: "summary", label: "Summary", type: "text", cardinality: "list" }],
      ui: { tabs: [{ id: "tab1", label: "Overview", slots: [{ id: "summary" }] }] },
    });
    renderWithProviders(<UiPreviewPanel model={model} />);
    expect(screen.getByText("Summary")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.uiPreviewTypeText")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.uiPreviewSlotList")).toBeInTheDocument();
  });

  it("falls back to the slot id when the slot definition is missing", () => {
    const model = baseModel({
      slots: [],
      ui: { tabs: [{ id: "tab1", label: "Overview", slots: [{ id: "missing-slot" }] }] },
    });
    renderWithProviders(<UiPreviewPanel model={model} />);
    // Both the label and the (missing) type fall back to the slot id.
    expect(screen.getAllByText("missing-slot")).toHaveLength(2);
  });
});
