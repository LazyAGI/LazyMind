import type { ComponentProps } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { applyModifiers, type DragEndEvent, type Modifiers } from "@dnd-kit/core";
import { SortableSupplierList } from "./SortableSupplierList";

const state = vi.hoisted(() => ({ end: undefined as ((event: DragEndEvent) => void) | undefined, modifiers: undefined as Modifiers | undefined }));
vi.mock("@dnd-kit/core", async (original) => {
  const actual = await original<typeof import("@dnd-kit/core")>();
  return { ...actual, DndContext: (props: ComponentProps<typeof actual.DndContext>) => {
    state.end = props.onDragEnd;
    state.modifiers = props.modifiers;
    return <actual.DndContext {...props} />;
  } };
});
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "en-US" }, t: (key: string, options?: { name?: string }) =>
    key.endsWith("reorderSupplier") ? `Move ${options?.name}` : key }),
}));

const items = [
  { id: "sense", name: "SenseNova" },
  { id: "claude", name: "Claude" },
  { id: "openai", name: "OpenAI" },
];

describe("supplier drag handles", () => {
  it("uses separate keyboard-focusable handles without taking over normal supplier selection", () => {
    const select = vi.fn(); const reorder = vi.fn();
    render(<SortableSupplierList items={items} disabled={false} onReorder={reorder}>
      {(item) => <button onClick={() => select(item.id)}>{item.name}</button>}
    </SortableSupplierList>);
    expect(screen.queryByRole("button", { name: "Move SenseNova" })).not.toBeInTheDocument();
    const handle = screen.getByRole("button", { name: "Move OpenAI" });
    expect(handle).toHaveAttribute("tabindex", "0");
    expect(handle).toHaveAttribute("aria-describedby");
    expect(handle.parentElement?.firstElementChild).toBe(handle);
    expect(handle.querySelector("button")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "OpenAI" }));
    expect(select).toHaveBeenCalledWith("openai");
    expect(reorder).not.toHaveBeenCalled();
    fireEvent.click(handle);
    expect(select).toHaveBeenCalledTimes(1);
    act(() => state.end?.({ active: { id: "openai" }, over: { id: "claude" } } as DragEndEvent));
    expect(reorder).toHaveBeenCalledWith(["sense", "claude", "openai"], "openai", "claude");
  });

  it.each([100, -100])("locks horizontal drag movement at %s while retaining vertical movement", (x) => {
    render(<SortableSupplierList items={items} disabled={false} onReorder={vi.fn()}>
      {(item) => <button>{item.name}</button>}
    </SortableSupplierList>);
    expect(applyModifiers(state.modifiers, {
      activatorEvent: null, active: null, activeNodeRect: null,
      draggingNodeRect: null, containerNodeRect: null, over: null,
      overlayNodeRect: null, scrollableAncestors: [], scrollableAncestorRects: [],
      windowRect: null, transform: { x, y: 72, scaleX: 1, scaleY: 1 },
    })).toEqual({ x: 0, y: 72, scaleX: 1, scaleY: 1 });
  });

  it("does not reorder suppliers while actions are disabled", () => {
    const reorder = vi.fn();
    render(<SortableSupplierList items={items} disabled onReorder={reorder}>
      {(item) => <button>{item.name}</button>}
    </SortableSupplierList>);
    expect(screen.queryByRole("button", { name: /Move/ })).not.toBeInTheDocument();
    act(() => state.end?.({ active: { id: "openai" }, over: { id: "claude" } } as DragEndEvent));
    expect(reorder).not.toHaveBeenCalled();
  });
});
