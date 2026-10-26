import { act, render, screen, waitFor } from "@testing-library/react";
import type { DragEndEvent } from "@dnd-kit/core";
import { beforeEach, expect, it, vi } from "vitest";
import { message } from "antd";
import ScopedConversationList from "./ScopedConversationList";

const mocks = vi.hoisted(() => ({ reorder: vi.fn(), end: (_event: DragEndEvent): void | Promise<void> => {} }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceReorder: mocks.reorder }) }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { name?: string }) => values?.name ? `${key} ${values.name}` : key }) }));
vi.mock("@dnd-kit/core", async original => {
  const actual = await original<typeof import("@dnd-kit/core")>();
  return { ...actual, DndContext: (props: React.ComponentProps<typeof actual.DndContext>) => { mocks.end = props.onDragEnd!; return <actual.DndContext {...props} />; } };
});
const items = [{ conversation_id: "a", display_name: "A" }, { conversation_id: "b", display_name: "B" }];
const event = { active: { id: "b" }, over: { id: "a" } } as DragEndEvent;
const result = { conversation_id: "b", is_pinned: false, pinned_at: null, history_order: 1, order_updates: [{ conversation_id: "b", history_order: 1 }, { conversation_id: "a", history_order: 2 }] };
const rows = () => [...document.querySelectorAll(".record-sortable")].map(item => item.getAttribute("data-conversation-id"));
beforeEach(() => { vi.resetAllMocks(); mocks.reorder.mockResolvedValue({ data: result }); });

it("updates immediately, prevents repeat saves, and commits the authoritative result", async () => {
  let finish!: (value: unknown) => void;
  mocks.reorder.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const commit = vi.fn(); const refresh = vi.fn();
  render(<ScopedConversationList items={items} scope="project:one" onCommitted={commit} onRefresh={refresh}>{item => <button data-conversation-title>{item.display_name}</button>}</ScopedConversationList>);
  await act(async () => { void mocks.end(event); void mocks.end(event); });
  expect(rows()).toEqual(["b", "a"]);
  expect(mocks.reorder).toHaveBeenCalledTimes(1);
  expect(mocks.reorder).toHaveBeenCalledWith("b", "a", "before");
  expect(commit).not.toHaveBeenCalled();
  expect(document.querySelector("[aria-busy=true]")).toBeInTheDocument();
  await act(async () => finish({ data: result }));
  expect(commit).toHaveBeenCalledWith({ movedId: "b", targetId: "a", position: "before" }, result);
  expect(refresh).toHaveBeenCalledTimes(1);
});

it("rolls back an unsuccessful move without losing refreshed metadata", async () => {
  const error = vi.spyOn(message, "error").mockImplementation(() => (() => {}) as never);
  let fail!: (reason: Error) => void;
  mocks.reorder.mockReturnValue(new Promise((_, reject) => { fail = reject; }));
  const commit = vi.fn(); const refresh = vi.fn();
  const view = render(<ScopedConversationList items={items} scope="children:one" onCommitted={commit} onRefresh={refresh}>{item => item.display_name}</ScopedConversationList>);
  await act(async () => { void mocks.end(event); });
  view.rerender(<ScopedConversationList items={[{ ...items[0], display_name: "Updated A" }, items[1]]} scope="children:one" onCommitted={commit} onRefresh={refresh}>{item => item.display_name}</ScopedConversationList>);
  await act(async () => fail(new Error("offline")));
  expect(rows()).toEqual(["a", "b"]);
  expect(screen.getByText("Updated A")).toBeInTheDocument();
  expect(commit).not.toHaveBeenCalled();
  expect(refresh).toHaveBeenCalledOnce();
  expect(error).toHaveBeenCalledWith("chat.reorderConversationFailed");
  error.mockRestore();
});

it("ignores completion after a route or source scope changes", async () => {
  let finish!: (value: unknown) => void;
  mocks.reorder.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const commit = vi.fn(); const refresh = vi.fn();
  const view = render(<ScopedConversationList items={items} scope="group:one" onCommitted={commit} onRefresh={refresh}>{item => item.display_name}</ScopedConversationList>);
  await act(async () => { void mocks.end(event); });
  view.rerender(<ScopedConversationList items={[{ conversation_id: "new", display_name: "New" }]} scope="group:two" onCommitted={commit} onRefresh={refresh}>{item => item.display_name}</ScopedConversationList>);
  await act(async () => finish({ data: result }));
  expect(rows()).toEqual(["new"]);
  expect(commit).not.toHaveBeenCalled();
  expect(refresh).not.toHaveBeenCalled();
});

it("rejects unavailable and locked drop targets", async () => {
  render(<ScopedConversationList items={items} scope="group:one" isItemDisabled={item => item.conversation_id === "a"} onCommitted={vi.fn()}>{item => item.display_name}</ScopedConversationList>);
  await act(async () => { await mocks.end(event); await mocks.end({ active: { id: "b" }, over: { id: "other" } } as DragEndEvent); });
  await waitFor(() => expect(mocks.reorder).not.toHaveBeenCalled());
  expect(rows()).toEqual(["a", "b"]);
});
