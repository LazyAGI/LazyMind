import { describe, expect, it } from "vitest";
import { parseYaml } from "./parser";

describe("parseYaml", () => {
  it("returns null on YAML syntax errors", () => {
    expect(parseYaml("steps: [invalid: : :")).toBeNull();
  });

  it("returns an empty-ish model for an empty document", () => {
    const model = parseYaml("");

    expect(model).toEqual({
      nodes: [],
      slots: {},
      layout: {},
      edgeLayout: {},
      startTransitions: [],
      startRoute: undefined,
    });
  });

  it("parses dict-format steps and injects the dict key as id when missing", () => {
    const yaml = `
steps:
  write_outline:
    label: Write Outline
    mode: human
    inputs:
      - material: topic
        required: true
transitions:
  write_outline:
    - to: __end__
`;
    const model = parseYaml(yaml);

    expect(model?.nodes).toHaveLength(1);
    expect(model?.nodes[0]).toEqual(
      expect.objectContaining({
        id: "write_outline",
        label: "Write Outline",
        mode: "human",
        inputs: [{ material: "topic", required: true }],
        transitions: [{ to: "__end__" }],
      }),
    );
  });

  it("parses array-format steps (AI-generated drafts) and skips virtual terminal ids", () => {
    const yaml = `
steps:
  - id: __start__
  - id: write_body
    label: Write Body
    mode: auto
`;
    const model = parseYaml(yaml);

    expect(model?.nodes).toHaveLength(1);
    expect(model?.nodes[0].id).toBe("write_body");
    expect(model?.nodes[0].mode).toBe("auto");
  });

  it("resolves start transitions with canonical, legacy, and initial-field priority", () => {
    const canonical = parseYaml(`
transitions:
  __start__:
    - to: a
start_transitions:
  - to: b
initial: c
`);
    expect(canonical?.startTransitions).toEqual([{ to: "a" }]);

    const legacy = parseYaml(`
start_transitions:
  - to: b
initial: c
`);
    expect(legacy?.startTransitions).toEqual([{ to: "b" }]);

    const initialOnly = parseYaml("initial: c");
    expect(initialOnly?.startTransitions).toEqual([{ to: "c" }]);
  });

  it("migrates split input_expression/optional_inputs into the canonical inputs list", () => {
    const yaml = `
steps:
  - id: a
    input_expression:
      any:
        - material: outline
        - material: draft
    optional_inputs:
      - notes
`;
    const model = parseYaml(yaml);

    expect(model?.nodes[0].inputs).toEqual([
      { material: "outline", required: true, alternatives: ["draft"] },
      { material: "notes", required: false },
    ]);
  });

  it("parses x-layout including edge visuals with clamped values", () => {
    const yaml = `
x-layout:
  a:
    x: 10
    y: 20
    w: 50
  $edges:
    a->b:
      showArrow: true
      arrowSize: 999
      stroke:
        color: "#ff0000"
        width: 999
        style: dashed
steps:
  - id: a
`;
    const model = parseYaml(yaml);

    expect(model?.layout.a).toEqual(expect.objectContaining({ x: 10, y: 20, width: 90 }));
    expect(model?.edgeLayout["a->b"]).toEqual({
      showArrow: true,
      arrowSize: 24,
      stroke: { color: "#ff0000", width: 12, style: "dashed" },
    });
  });
});
