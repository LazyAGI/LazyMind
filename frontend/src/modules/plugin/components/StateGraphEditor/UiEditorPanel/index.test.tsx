import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import UiEditorPanel from "./index";
import { createEmptyModel } from "../core/model";
import { createEmptyPluginModel } from "../core/pluginModel";
import type { GraphModel } from "../core/model";
import type { PluginModel, PluginUiTab } from "../core/pluginModel";

function makeGraphModel(overrides: Partial<GraphModel> = {}): GraphModel {
  return {
    ...createEmptyModel(),
    slots: { outline: { id: "outline", type: "text", label: "Outline" } },
    ...overrides,
  };
}

function makePluginModel(tabs: PluginUiTab[]): PluginModel {
  return { ...createEmptyPluginModel(), ui: { tabs } };
}

describe("UiEditorPanel", () => {
  it("renders the sidebar artifact panel and the wysiwyg canvas", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [{ id: "outline" }] }];
    const { container } = renderWithProviders(
      <UiEditorPanel
        graphModel={makeGraphModel()}
        pluginModel={makePluginModel(tabs)}
        onGraphModelChange={vi.fn()}
        onPluginModelChange={vi.fn()}
        activeTabId="tab1"
        onActiveTabChange={vi.fn()}
      />,
    );
    expect(container.querySelector(".uep-sidebar")).toBeInTheDocument();
    expect(container.querySelector(".uep-canvas-area")).toBeInTheDocument();
    expect(container.querySelector(".uep-widget-label")?.textContent).toBe("Outline");
  });

  it("shows the properties panel with widget selector when a slot card is selected", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [{ id: "outline" }] }];
    const { container } = renderWithProviders(
      <UiEditorPanel
        graphModel={makeGraphModel()}
        pluginModel={makePluginModel(tabs)}
        onGraphModelChange={vi.fn()}
        onPluginModelChange={vi.fn()}
        activeTabId="tab1"
        onActiveTabChange={vi.fn()}
      />,
    );
    fireEvent.click(container.querySelector(".uep-widget-card")!);
    expect(container.querySelector(".uep-props-panel")).toBeInTheDocument();
  });

  it("toggles fullscreen class when the expand button is clicked", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [] }];
    const { container } = renderWithProviders(
      <UiEditorPanel
        graphModel={makeGraphModel()}
        pluginModel={makePluginModel(tabs)}
        onGraphModelChange={vi.fn()}
        onPluginModelChange={vi.fn()}
        activeTabId="tab1"
        onActiveTabChange={vi.fn()}
      />,
    );
    expect(container.querySelector(".uep-root--fullscreen")).not.toBeInTheDocument();
    fireEvent.click(container.querySelector(".uep-expand-btn")!);
    expect(container.querySelector(".uep-root--fullscreen")).toBeInTheDocument();
  });

  it("does not open the properties panel when a slot card is clicked in readonly mode", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [{ id: "outline" }] }];
    const { container } = renderWithProviders(
      <UiEditorPanel
        graphModel={makeGraphModel()}
        pluginModel={makePluginModel(tabs)}
        onGraphModelChange={vi.fn()}
        onPluginModelChange={vi.fn()}
        activeTabId="tab1"
        onActiveTabChange={vi.fn()}
        readonly
      />,
    );
    fireEvent.click(container.querySelector(".uep-widget-card")!);
    expect(container.querySelector(".uep-props-panel")).not.toBeInTheDocument();
  });
});
