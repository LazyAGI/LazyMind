import { describe, expect, it } from "vitest";
import type { GraphModel, StepNode } from "./model";
import { createEmptyModel } from "./model";
import { validateStateGraph } from "./validator";

const buildStep = (overrides: Partial<StepNode>): StepNode => ({
  id: "step",
  label: "Step",
  mode: "human",
  inputs: [],
  outputs: [],
  transitions: [],
  ...overrides,
});

const buildModel = (overrides: Partial<GraphModel>): GraphModel => ({
  ...createEmptyModel(),
  ...overrides,
});

describe("validateStateGraph", () => {
  it("returns no errors for a well-formed model", () => {
    const model = buildModel({
      nodes: [
        buildStep({ id: "a", transitions: [{ to: "__end__" }] }),
      ],
    });

    expect(validateStateGraph(model)).toEqual([]);
  });

  it("flags duplicate node ids colliding with virtual start/end", () => {
    const model = buildModel({
      nodes: [buildStep({ id: "__start__" })],
    });

    const errors = validateStateGraph(model);
    expect(errors).toContainEqual(
      expect.objectContaining({ code: "LOCAL_DUPLICATE_NODE", nodeId: "__start__" }),
    );
  });

  it("flags transitions that point to a non-existent node", () => {
    const model = buildModel({
      nodes: [buildStep({ id: "a", transitions: [{ to: "missing" }] })],
    });

    const errors = validateStateGraph(model);
    expect(errors).toContainEqual(
      expect.objectContaining({ code: "LOCAL_DANGLING_EDGE", edgeKey: "a->missing" }),
    );
  });

  it("detects a directed cycle between nodes", () => {
    const model = buildModel({
      nodes: [
        buildStep({ id: "a", transitions: [{ to: "b" }] }),
        buildStep({ id: "b", transitions: [{ to: "a" }] }),
      ],
    });

    const errors = validateStateGraph(model);
    const cycleErrors = errors.filter((e) => e.code === "LOCAL_CYCLE");
    expect(cycleErrors.length).toBeGreaterThan(0);
    expect(cycleErrors.map((e) => e.nodeId)).toEqual(expect.arrayContaining(["a"]));
  });

  it("flags materials referenced by a node but missing from slots when slots is non-empty", () => {
    const model = buildModel({
      slots: { outline: { id: "outline", type: "text" } },
      nodes: [
        buildStep({
          id: "a",
          inputs: [{ material: "unknown_material", required: true }],
        }),
      ],
    });

    const errors = validateStateGraph(model);
    expect(errors).toContainEqual(
      expect.objectContaining({ code: "LOCAL_UNKNOWN_MATERIAL", materialId: "unknown_material" }),
    );
  });

  it("skips the unknown-material check entirely when no slots are defined", () => {
    const model = buildModel({
      nodes: [
        buildStep({
          id: "a",
          inputs: [{ material: "anything", required: true }],
        }),
      ],
    });

    const errors = validateStateGraph(model);
    expect(errors.some((e) => e.code === "LOCAL_UNKNOWN_MATERIAL")).toBe(false);
  });
});
