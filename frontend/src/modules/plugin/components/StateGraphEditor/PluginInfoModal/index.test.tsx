import { fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import PluginInfoModal from "./index";
import { polishPluginInfo } from "../../../pluginDraftApi";
import type { PluginModel } from "../core/pluginModel";
import type { ScenarioData } from "../ScenarioEditor";

vi.mock("../../../pluginDraftApi", () => ({
  polishPluginInfo: vi.fn(),
}));

const polishPluginInfoMock = polishPluginInfo as ReturnType<typeof vi.fn>;

const basePlugin: PluginModel = {
  id: "my-plugin",
  name: "My Plugin",
  description: "does things",
  when_to_use: "when needed",
  steps: [],
  slots: [],
};

const baseScenario: ScenarioData = {
  overview: "overview text",
  stepDescriptions: {},
  notes: "some notes",
};

describe("PluginInfoModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    polishPluginInfoMock.mockResolvedValue({});
  });

  it("initializes fields from the given plugin model and scenario data", () => {
    renderWithProviders(
      <PluginInfoModal
        open
        onCancel={vi.fn()}
        pluginModel={basePlugin}
        scenarioData={baseScenario}
      />,
    );

    expect(screen.getByDisplayValue("my-plugin")).toBeInTheDocument();
    expect(screen.getByDisplayValue("My Plugin")).toBeInTheDocument();
    expect(screen.getByDisplayValue("does things")).toBeInTheDocument();
    expect(screen.getByDisplayValue("overview text")).toBeInTheDocument();
    expect(screen.getByDisplayValue("some notes")).toBeInTheDocument();
  });

  it("shows an id validation error and blocks save for an invalid id", async () => {
    const onSave = vi.fn();
    renderWithProviders(
      <PluginInfoModal
        open
        onCancel={vi.fn()}
        pluginModel={basePlugin}
        scenarioData={baseScenario}
        onSave={onSave}
      />,
    );

    fireEvent.change(screen.getByDisplayValue("my-plugin"), {
      target: { value: "1-bad" },
    });
    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.pluginInfoSaveBtn" }));

    expect(
      await screen.findByText("selfEvolutionRun.pluginInfoIdInvalid"),
    ).toBeInTheDocument();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("saves the trimmed plugin info and scenario data", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const onCancel = vi.fn();
    renderWithProviders(
      <PluginInfoModal
        open
        onCancel={onCancel}
        pluginModel={basePlugin}
        scenarioData={baseScenario}
        onSave={onSave}
      />,
    );

    fireEvent.change(screen.getByDisplayValue("My Plugin"), {
      target: { value: "  New Name  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.pluginInfoSaveBtn" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: "my-plugin", name: "New Name" }),
      expect.objectContaining({ overview: "overview text", notes: "some notes" }),
    ));
    await waitFor(() => expect(onCancel).toHaveBeenCalled());
  });

  it("renders readonly inputs and only a close button when readonly", () => {
    renderWithProviders(
      <PluginInfoModal
        open
        onCancel={vi.fn()}
        pluginModel={basePlugin}
        scenarioData={baseScenario}
        readonly
      />,
    );

    expect(screen.getByDisplayValue("my-plugin")).toHaveAttribute("readonly");
    expect(
      screen.getByRole("button", { name: "selfEvolutionRun.pluginInfoCloseBtn" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "selfEvolutionRun.pluginInfoSaveBtn" }),
    ).not.toBeInTheDocument();
  });

  it("polishes a single field and applies the returned text", async () => {
    polishPluginInfoMock.mockResolvedValueOnce({ description: "polished description" });
    renderWithProviders(
      <PluginInfoModal
        open
        onCancel={vi.fn()}
        pluginModel={basePlugin}
        scenarioData={baseScenario}
      />,
    );

    const polishButtons = screen.getAllByRole("button", {
      name: "selfEvolutionRun.pluginInfoPolishTooltip",
    });
    fireEvent.click(polishButtons[0]);

    await waitFor(() => expect(polishPluginInfoMock).toHaveBeenCalledWith({
      fields: { description: "does things", when_to_use: "when needed", overview: "overview text", notes: "some notes" },
      target_fields: ["description"],
    }));
    expect(await screen.findByDisplayValue("polished description")).toBeInTheDocument();
  });
});
