import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Node } from "@xyflow/react";
import { useAlignmentGuides } from "./useAlignmentGuides";

function makeNode(id: string, x: number, y: number, width = 148, height = 80): Node {
  return { id, position: { x, y }, data: {}, width, height };
}

describe("useAlignmentGuides", () => {
  it("starts with no guides", () => {
    const { result } = renderHook(() => useAlignmentGuides());
    expect(result.current.guides).toEqual([]);
  });

  it("snaps the dragging node to a nearby node's top edge and produces a horizontal guide", () => {
    const { result } = renderHook(() => useAlignmentGuides());
    const other = makeNode("a", 100, 200);
    const dragging = makeNode("b", 400, 203);

    let snap: { x: number; y: number } | null = null;
    act(() => {
      snap = result.current.onNodeDrag(dragging, [dragging, other]);
    });

    expect(snap).toEqual({ x: dragging.position.x, y: 200 });
    expect(result.current.guides.some((g) => g.type === "horizontal")).toBe(true);
  });

  it("returns null and produces no guides when nothing is within the snap threshold", () => {
    const { result } = renderHook(() => useAlignmentGuides());
    const other = makeNode("a", 100, 200);
    const dragging = makeNode("b", 400, 500);

    let snap: { x: number; y: number } | null = null;
    act(() => {
      snap = result.current.onNodeDrag(dragging, [dragging, other]);
    });

    expect(snap).toBeNull();
    expect(result.current.guides).toEqual([]);
  });

  it("clears guides on drag stop", () => {
    const { result } = renderHook(() => useAlignmentGuides());
    const other = makeNode("a", 100, 200);
    const dragging = makeNode("b", 400, 201);

    act(() => {
      result.current.onNodeDrag(dragging, [dragging, other]);
    });
    expect(result.current.guides.length).toBeGreaterThan(0);

    act(() => {
      result.current.onNodeDragStop();
    });
    expect(result.current.guides).toEqual([]);
  });

  it("snaps resize width to match another node's width when within threshold", () => {
    const { result } = renderHook(() => useAlignmentGuides());
    const target = makeNode("target", 0, 0, 200, 80);
    const resizing = makeNode("resizing", 0, 150, 150, 80);

    let snapped: { width: number; height?: number } = { width: 0 };
    act(() => {
      snapped = result.current.onNodeResize("resizing", 203, undefined, [target, resizing]);
    });

    expect(snapped.width).toBe(200);
  });

  it("leaves the resize width unsnapped when no candidate is close enough", () => {
    const { result } = renderHook(() => useAlignmentGuides());
    const target = makeNode("target", 0, 0, 200, 80);
    const resizing = makeNode("resizing", 0, 150, 150, 80);

    let snapped: { width: number; height?: number } = { width: 0 };
    act(() => {
      snapped = result.current.onNodeResize("resizing", 170, undefined, [target, resizing]);
    });

    expect(snapped.width).toBe(170);
  });
});
