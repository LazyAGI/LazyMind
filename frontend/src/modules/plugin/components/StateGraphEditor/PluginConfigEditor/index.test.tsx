import { fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import PluginConfigEditor from "./index";
import type { PluginModel } from "../core/pluginModel";

function baseModel(): PluginModel {
  return {
    id: "my-plugin",
    name: "My Plugin",
    description: "does things",
    when_to_use: "when needed",
    steps: [],
    slots: [],
  };
}

describe("PluginConfigEditor", () => {
  it("renders the current model values in each field", () => {
    renderWithProviders(<PluginConfigEditor model={baseModel()} onChange={vi.fn()} />);
    expect(screen.getByDisplayValue("my-plugin")).toBeInTheDocument();
    expect(screen.getByDisplayValue("My Plugin")).toBeInTheDocument();
    expect(screen.getByDisplayValue("does things")).toBeInTheDocument();
    expect(screen.getByDisplayValue("when needed")).toBeInTheDocument();
  });

  it("emits a merged model when the plugin id changes", () => {
    const onChange = vi.fn();
    renderWithProviders(<PluginConfigEditor model={baseModel()} onChange={onChange} />);

    fireEvent.change(screen.getByDisplayValue("my-plugin"), {
      target: { value: "new-id" },
    });

    expect(onChange).toHaveBeenCalledWith({ ...baseModel(), id: "new-id" });
  });

  it("emits a merged model when the description changes", () => {
    const onChange = vi.fn();
    renderWithProviders(<PluginConfigEditor model={baseModel()} onChange={onChange} />);

    fireEvent.change(screen.getByDisplayValue("does things"), {
      target: { value: "does other things" },
    });

    expect(onChange).toHaveBeenCalledWith({ ...baseModel(), description: "does other things" });
  });

  it("renders empty text areas gracefully when optional fields are absent", () => {
    const model: PluginModel = { id: "p", name: "P", steps: [], slots: [] };
    renderWithProviders(<PluginConfigEditor model={model} onChange={vi.fn()} />);
    expect(
      screen.getByPlaceholderText("selfEvolutionRun.pluginInfoFieldDescriptionPlaceholder"),
    ).toHaveValue("");
  });
});
