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
    expect(screen.getByRole("button", { name: /Move SenseNova/ })).toBeInTheDocument();
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

  it("opens a hide action from the drag handle, including a single supplier", async () => {
    const hide = vi.fn();
    render(<SortableSupplierList items={[items[0]]} disabled={false} onReorder={vi.fn()} onVisibilityChange={hide}>
      {item => <button>{item.name}</button>}
    </SortableSupplierList>);
    fireEvent.click(screen.getByRole("button", { name: /Move SenseNova/ }));
    fireEvent.click(await screen.findByText("modelProvider.workspace.hideSupplier"));
    expect(hide).toHaveBeenCalledWith("sense");
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

  it("does not animate neighboring rows after the order is committed", () => {
    const originalAnimate = HTMLElement.prototype.animate;
    const animate = vi.fn();
    HTMLElement.prototype.animate = animate;
    const rect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const index = this.classList.contains("provider-workspace-sortable-row") ? [...this.parentElement!.children].indexOf(this) : 0;
      return { top: index * 44, left: 0, width: 200, height: 44 } as DOMRect;
    });
    try {
      const child = (item: typeof items[number]) => <button>{item.name}</button>;
      const view = render(<SortableSupplierList items={items} disabled={false} onReorder={vi.fn()}>{child}</SortableSupplierList>);
      expect(animate).not.toHaveBeenCalled();
      view.rerender(<SortableSupplierList items={[items[2], items[0], items[1]]} disabled={false} onReorder={vi.fn()}>{child}</SortableSupplierList>);
      expect(animate).not.toHaveBeenCalled();
    } finally {
      HTMLElement.prototype.animate = originalAnimate;
      rect.mockRestore();
    }
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
