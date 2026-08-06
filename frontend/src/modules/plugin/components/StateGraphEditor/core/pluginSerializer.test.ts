import jsYaml from "js-yaml";
import { describe, expect, it } from "vitest";
import { createEmptyModel, type GraphModel } from "./model";
import { createEmptyPluginModel, type PluginModel } from "./pluginModel";
import { serializePluginModel } from "./pluginSerializer";

describe("serializePluginModel", () => {
  it("serializes required id/name and optional description/when_to_use only when present", () => {
    const model: PluginModel = { ...createEmptyPluginModel(), id: "p1", name: "Plugin One" };
    const parsed = jsYaml.load(serializePluginModel(model)) as Record<string, unknown>;

    expect(parsed).toEqual({ id: "p1", name: "Plugin One" });
  });

  it("prefers the graphModel's node list over model.steps for the steps block", () => {
    const model: PluginModel = {
      ...createEmptyPluginModel(),
      id: "p1",
      name: "Plugin One",
      steps: [{ id: "stale", label: "Stale" }],
    };
    const graphModel: GraphModel = {
      ...createEmptyModel(),
      nodes: [
        { id: "fresh", label: "Fresh", mode: "human", inputs: [], outputs: [], transitions: [] },
      ],
    };

    const parsed = jsYaml.load(serializePluginModel(model, graphModel)) as { steps: unknown };
    expect(parsed.steps).toEqual([{ id: "fresh", label: "Fresh" }]);
  });

  it("prefers graphModel.slots over model.slots and serializes list-cardinality fields", () => {
    const model: PluginModel = {
      ...createEmptyPluginModel(),
      id: "p1",
      name: "Plugin One",
      slots: [{ id: "stale", type: "text" }],
    };
    const graphModel: GraphModel = {
      ...createEmptyModel(),
      slots: {
        images: { id: "images", type: "image", cardinality: "list", ordered: true, allow_manual_add: false },
      },
    };

    const parsed = jsYaml.load(serializePluginModel(model, graphModel)) as { slots: Array<Record<string, unknown>> };
    expect(parsed.slots).toEqual([
      { id: "images", type: "image", cardinality: "list", ordered: true, allow_manual_add: false },
    ]);
  });

  it("falls back to model.slots when no graphModel is provided", () => {
    const model: PluginModel = {
      ...createEmptyPluginModel(),
      id: "p1",
      name: "Plugin One",
      slots: [{ id: "outline", type: "text", external: true }],
    };

    const parsed = jsYaml.load(serializePluginModel(model)) as { slots: Array<Record<string, unknown>> };
    expect(parsed.slots).toEqual([{ id: "outline", type: "text", external: true }]);
  });

  it("serializes ui.tabs with only slot id lists and omits the ui block when there are no tabs", () => {
    const withUi: PluginModel = {
      ...createEmptyPluginModel(),
      id: "p1",
      name: "Plugin One",
      ui: {
        tabs: [
          { id: "main", label: "Main", layout: "grid", gridCols: 2, slots: [{ id: "outline" }] },
        ],
        slots: { outline: { widgetType: "text-markdown" } },
      },
    };
    const parsedWithUi = jsYaml.load(serializePluginModel(withUi)) as { ui: Record<string, unknown> };
    expect(parsedWithUi.ui).toEqual({
      slots: { outline: { widgetType: "text-markdown" } },
      tabs: [{ id: "main", label: "Main", layout: "grid", grid_cols: 2, slots: [{ id: "outline" }] }],
    });

    const withoutUi: PluginModel = { ...createEmptyPluginModel(), id: "p1", name: "Plugin One" };
    const parsedWithoutUi = jsYaml.load(serializePluginModel(withoutUi)) as Record<string, unknown>;
    expect(parsedWithoutUi.ui).toBeUndefined();
  });

  it("preserves the i18n block as-is when present", () => {
    const model: PluginModel = {
      ...createEmptyPluginModel(),
      id: "p1",
      name: "Plugin One",
      i18n: { en: { greeting: "hi" } },
    };

    const parsed = jsYaml.load(serializePluginModel(model)) as Record<string, unknown>;
    expect(parsed.i18n).toEqual({ en: { greeting: "hi" } });
  });
});
