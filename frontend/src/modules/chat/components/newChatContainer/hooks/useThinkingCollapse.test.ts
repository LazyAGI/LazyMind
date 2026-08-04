import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useThinkingCollapse } from "./useThinkingCollapse";

describe("useThinkingCollapse", () => {
  it("starts with an empty collapse map", () => {
    const { result } = renderHook(() => useThinkingCollapse());
    expect(result.current.thinkingCollapseMap.size).toBe(0);
  });

  it("isThinkingCollapsed returns the provided default when the key is unset", () => {
    const { result } = renderHook(() => useThinkingCollapse());
    expect(result.current.isThinkingCollapsed("a")).toBe(false);
    expect(result.current.isThinkingCollapsed("a", true)).toBe(true);
  });

  it("toggleThinkingCollapse flips the current collapsed state for a key", () => {
    const { result } = renderHook(() => useThinkingCollapse());

    act(() => {
      result.current.toggleThinkingCollapse("a", false);
    });
    expect(result.current.isThinkingCollapsed("a")).toBe(true);

    act(() => {
      result.current.toggleThinkingCollapse("a", true);
    });
    expect(result.current.isThinkingCollapsed("a")).toBe(false);
  });

  it("collapseAllThinking sets every existing key to collapsed", () => {
    const { result } = renderHook(() => useThinkingCollapse());

    act(() => {
      result.current.toggleThinkingCollapse("a", true);
      result.current.toggleThinkingCollapse("b", true);
    });
    expect(result.current.isThinkingCollapsed("a")).toBe(false);
    expect(result.current.isThinkingCollapsed("b")).toBe(false);

    act(() => {
      result.current.collapseAllThinking();
    });

    expect(result.current.isThinkingCollapsed("a")).toBe(true);
    expect(result.current.isThinkingCollapsed("b")).toBe(true);
  });

  it("collapseAllThinking does not add keys that were never set", () => {
    const { result } = renderHook(() => useThinkingCollapse());

    act(() => {
      result.current.collapseAllThinking();
    });

    expect(result.current.thinkingCollapseMap.size).toBe(0);
    expect(result.current.isThinkingCollapsed("never-set")).toBe(false);
  });
});
