import { describe, expect, it } from "vitest";
import type { EdgeVisual, NodeLayout } from "./model";
import {
  deleteLayoutNode,
  edgeId,
  reconnectEdgeLayout,
  renameLayoutNode,
  serializeLayout,
} from "./layout";

describe("edgeId", () => {
  it("joins source and target with an arrow", () => {
    expect(edgeId("a", "b")).toBe("a->b");
  });
});

describe("serializeLayout", () => {
  it("serializes node layout entries directly by id", () => {
    const layout: Record<string, NodeLayout> = { a: { x: 1, y: 2 } };
    const result = JSON.parse(serializeLayout({ layout, edgeLayout: {} }));

    expect(result).toEqual({ a: { x: 1, y: 2 } });
  });

  it("includes $meta and $edges only when there are non-empty edge visuals", () => {
    const layout: Record<string, NodeLayout> = { a: { x: 0, y: 0 } };
    const edgeLayout: Record<string, EdgeVisual> = {
      "a->b": { showArrow: true },
      "b->c": {},
    };

    const result = JSON.parse(serializeLayout({ layout, edgeLayout }));

    expect(result.$meta).toEqual({ version: 2 });
    expect(result.$edges).toEqual({ "a->b": { showArrow: true } });
  });

  it("omits $meta/$edges entirely when all edge visuals are empty", () => {
    const result = JSON.parse(
      serializeLayout({ layout: {}, edgeLayout: { "a->b": {} } }),
    );

    expect(result.$meta).toBeUndefined();
    expect(result.$edges).toBeUndefined();
  });
});

describe("renameLayoutNode", () => {
  it("moves the layout entry from the old id to the new id", () => {
    const layout: Record<string, NodeLayout> = { old: { x: 1, y: 1 } };
    const { layout: nextLayout } = renameLayoutNode(layout, {}, "old", "new");

    expect(nextLayout).toEqual({ new: { x: 1, y: 1 } });
    expect(nextLayout.old).toBeUndefined();
  });

  it("rewrites edge keys referencing the renamed node on either side", () => {
    const edgeLayout: Record<string, EdgeVisual> = {
      "old->b": { showArrow: true },
      "c->old": { showLabel: true },
    };

    const { edgeLayout: nextEdges } = renameLayoutNode({}, edgeLayout, "old", "new");

    expect(nextEdges).toEqual({
      "new->b": { showArrow: true },
      "c->new": { showLabel: true },
    });
  });

  it("leaves layout untouched when the old id has no entry", () => {
    const { layout: nextLayout } = renameLayoutNode({ other: { x: 0, y: 0 } }, {}, "missing", "new");
    expect(nextLayout).toEqual({ other: { x: 0, y: 0 } });
  });
});

describe("deleteLayoutNode", () => {
  it("removes the node's own layout entry", () => {
    const layout: Record<string, NodeLayout> = { a: { x: 0, y: 0 }, b: { x: 1, y: 1 } };
    const { layout: nextLayout } = deleteLayoutNode(layout, {}, "a");

    expect(nextLayout).toEqual({ b: { x: 1, y: 1 } });
  });

  it("drops edges that reference the deleted node as source or target", () => {
    const edgeLayout: Record<string, EdgeVisual> = {
      "a->b": {},
      "b->a": {},
      "b->c": {},
    };

    const { edgeLayout: nextEdges } = deleteLayoutNode({}, edgeLayout, "a");

    expect(nextEdges).toEqual({ "b->c": {} });
  });
});

describe("reconnectEdgeLayout", () => {
  it("moves the edge style keyed by the old id to the new id", () => {
    const edgeLayout: Record<string, EdgeVisual> = { "a->b": { showArrow: true } };
    const result = reconnectEdgeLayout(edgeLayout, "a->b", "a->c");

    expect(result).toEqual({ "a->c": { showArrow: true } });
    expect(result["a->b"]).toBeUndefined();
  });

  it("returns an unchanged copy when the old key is not present", () => {
    const edgeLayout: Record<string, EdgeVisual> = { "x->y": {} };
    const result = reconnectEdgeLayout(edgeLayout, "a->b", "a->c");

    expect(result).toEqual({ "x->y": {} });
  });
});
