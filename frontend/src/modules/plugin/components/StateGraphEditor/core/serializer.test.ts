import jsYaml from "js-yaml";
import { describe, expect, it } from "vitest";
import { createEmptyModel, type GraphModel, type StepNode } from "./model";
import { serializeModel } from "./serializer";

const buildStep = (overrides: Partial<StepNode>): StepNode => ({
  id: "step",
  label: "Step",
  mode: "human",
  inputs: [],
  outputs: [],
  transitions: [],
  ...overrides,
});

describe("serializeModel", () => {
  it("omits the x-layout block when includeLayout is false, even if layout data exists", () => {
    const model: GraphModel = {
      ...createEmptyModel(),
      layout: { a: { x: 1, y: 2 } },
      nodes: [buildStep({ id: "a" })],
    };

    const parsed = jsYaml.load(serializeModel(model, false)) as Record<string, unknown>;
    expect(parsed["x-layout"]).toBeUndefined();
  });

  it("includes the x-layout block with rounded coordinates when includeLayout is true", () => {
    const model: GraphModel = {
      ...createEmptyModel(),
      layout: { a: { x: 1.6, y: 2.4, width: 100.9 } },
      nodes: [buildStep({ id: "a" })],
    };

    const parsed = jsYaml.load(serializeModel(model, true)) as Record<string, unknown>;
    expect(parsed["x-layout"]).toEqual({ a: { x: 2, y: 2, w: 101 } });
  });

  it("serializes start transitions under transitions.__start__ and per-node transitions by id", () => {
    const model: GraphModel = {
      ...createEmptyModel(),
      startTransitions: [{ to: "a", when: "always" }],
      nodes: [buildStep({ id: "a", transitions: [{ to: "__end__" }] })],
    };

    const parsed = jsYaml.load(serializeModel(model)) as Record<string, unknown>;
    expect(parsed.transitions).toEqual({
      __start__: [{ to: "a", when: "always" }],
      a: [{ to: "__end__" }],
    });
  });

  it("includes start_route only when it differs from the default 'all'", () => {
    const withChoice: GraphModel = {
      ...createEmptyModel(),
      startTransitions: [{ to: "a" }],
      startRoute: "choice",
      nodes: [buildStep({ id: "a" })],
    };
    const parsedChoice = jsYaml.load(serializeModel(withChoice)) as Record<string, unknown>;
    expect(parsedChoice.start_route).toBe("choice");

    const withAll: GraphModel = {
      ...createEmptyModel(),
      startTransitions: [{ to: "a" }],
      startRoute: "all",
      nodes: [buildStep({ id: "a" })],
    };
    const parsedAll = jsYaml.load(serializeModel(withAll)) as Record<string, unknown>;
    expect(parsedAll.start_route).toBeUndefined();
  });

  it("serializes step fields (route, skip_if, prompt, tools, inputs, outputs) only when present", () => {
    const model: GraphModel = {
      ...createEmptyModel(),
      nodes: [
        buildStep({
          id: "a",
          route: "choice",
          skipIf: { material: "done" },
          prompt: "Write the draft",
          tools: ["search"],
          acceptanceCriteria: "must be concise",
          inputs: [{ material: "outline", required: true, alternatives: ["draft"] }],
          outputs: [{ material: "body" }, { material: "summary", required: false }],
        }),
      ],
    };

    const parsed = jsYaml.load(serializeModel(model)) as { steps: Array<Record<string, unknown>> };
    expect(parsed.steps[0]).toEqual({
      id: "a",
      label: "Step",
      mode: "human",
      route: "choice",
      skip_if: { material: "done" },
      prompt: "Write the draft",
      tools: ["search"],
      acceptance_criteria: "must be concise",
      inputs: [{ material: "outline", required: true, alternatives: [{ material: "draft" }] }],
      outputs: [{ material: "body" }, { material: "summary", required: false }],
    });
  });

  it("falls back to legacySkipIf as a string when skipIf is absent", () => {
    const model: GraphModel = {
      ...createEmptyModel(),
      nodes: [buildStep({ id: "a", legacySkipIf: "user said skip" })],
    };

    const parsed = jsYaml.load(serializeModel(model)) as { steps: Array<Record<string, unknown>> };
    expect(parsed.steps[0].skip_if).toBe("user said skip");
  });
});
