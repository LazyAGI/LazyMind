import { describe, expect, it } from "vitest";
import { parsePluginYaml } from "./pluginParser";

describe("parsePluginYaml", () => {
  it("returns null on YAML syntax errors", () => {
    expect(parsePluginYaml("id: [invalid: : :")).toBeNull();
  });

  it("parses basic metadata fields and defaults missing steps/slots to empty arrays", () => {
    const model = parsePluginYaml(`
id: my-plugin
name: My Plugin
description: A test plugin
`);

    expect(model).toEqual(
      expect.objectContaining({
        id: "my-plugin",
        name: "My Plugin",
        description: "A test plugin",
        steps: [],
        slots: [],
      }),
    );
  });

  it("parses list-format slots including list cardinality and manual-add flags", () => {
    const model = parsePluginYaml(`
id: p
name: P
slots:
  - id: outline
    type: text
  - id: images
    type: image
    cardinality: list
    ordered: true
    allow_manual_add: false
`);

    expect(model?.slots).toEqual([
      { id: "outline", type: "text", label: undefined },
      {
        id: "images",
        type: "image",
        label: undefined,
        cardinality: "list",
        ordered: true,
        allow_manual_add: false,
      },
    ]);
  });

  it("parses legacy map-format slots keyed by slot id", () => {
    const model = parsePluginYaml(`
id: p
name: P
slots:
  outline:
    type: text
    label: Outline
`);

    expect(model?.slots).toEqual([{ id: "outline", type: "text", label: "Outline" }]);
  });

  it("falls back to 'text' type for unrecognized slot types", () => {
    const model = parsePluginYaml(`
id: p
name: P
slots:
  - id: weird
    type: unknown_type
`);

    expect(model?.slots[0].type).toBe("text");
  });

  it("parses tool_scripts entries and drops entries without a path", () => {
    const model = parsePluginYaml(`
id: p
name: P
tool_scripts:
  - path: scripts/tools.py
    functions: [search, summarize]
  - functions: [ignored]
`);

    expect(model?.tool_scripts).toEqual([{ path: "scripts/tools.py", functions: ["search", "summarize"] }]);
  });

  it("parses ui.tabs with slot id lists and migrates legacy per-slot widget config", () => {
    const model = parsePluginYaml(`
id: p
name: P
ui:
  tabs:
    - id: main
      layout: grid
      grid_cols: 2
      slots:
        - id: outline
          widget:
            widgetType: text-markdown
`);

    expect(model?.ui?.tabs).toEqual([
      expect.objectContaining({ id: "main", layout: "grid", gridCols: 2, slots: [{ id: "outline" }] }),
    ]);
    expect(model?.ui?.slots).toEqual({ outline: { widgetType: "text-markdown" } });
  });

  it("migrates legacy composite_layout array format (format A) into a CompositePanelNode tree", () => {
    const model = parsePluginYaml(`
id: p
name: P
ui:
  tabs:
    - id: main
      layout: composite
      composite_layout:
        - - slot: left
            weight: 1
          - slot: right
            weight: 2
`);

    const tab = model?.ui?.tabs[0];
    expect(tab?.composite_layout).toEqual({
      direction: "row",
      children: [
        { slot: "left", weight: 1 },
        { slot: "right", weight: 2 },
      ],
    });
  });

  it("parses composite_behavior with mutually_exclusive groups", () => {
    const model = parsePluginYaml(`
id: p
name: P
ui:
  tabs:
    - id: main
      layout: composite
      composite_behavior:
        hide_empty_columns: true
        empty_column_scope: tab
        mutually_exclusive:
          - slots: [a, b]
            prefer: [a]
`);

    expect(model?.ui?.tabs[0].composite_behavior).toEqual({
      hide_empty_columns: true,
      empty_column_scope: "tab",
      mutually_exclusive: [{ slots: ["a", "b"], prefer: ["a"] }],
    });
  });
});
