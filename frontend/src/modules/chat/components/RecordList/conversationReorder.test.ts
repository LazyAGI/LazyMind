import { describe, expect, it } from "vitest";
import type { DragEndEvent } from "@dnd-kit/core";
import { conversationDropPlacement, moveConversationItems } from "./conversationReorder";

describe("conversation relative placement", () => {
  const items = [{ conversation_id: "a", title: "latest metadata" }, { conversation_id: "hidden" }, { conversation_id: "b" }];
  it("moves only the requested item and preserves metadata and unloaded-relative order", () => {
    const result = moveConversationItems(items, { movedId: "a", targetId: "b", position: "after" });
    expect(result.map(item => item.conversation_id)).toEqual(["hidden", "b", "a"]);
    expect(result[2]).toBe(items[0]);
    expect(items.map(item => item.conversation_id)).toEqual(["a", "hidden", "b"]);
  });
  it.each(["missing", "a"])("ignores unavailable/self targets: %s", targetId => {
    expect(moveConversationItems(items, { movedId: "a", targetId, position: "before" })).toBe(items);
  });
  it.each([[105, "before"], [130, "after"]] as const)("uses the same visible insertion edge at %i", (top, position) => {
    const event = { active: { id: "a", rect: { current: { translated: { top, height: 20 } } } }, over: { id: "b", rect: { top: 100, height: 50 } } } as DragEndEvent;
    expect(conversationDropPlacement(event, ["a", "b"])).toEqual({ movedId: "a", targetId: "b", position });
  });
  it("rejects drops outside the scoped list", () => {
    expect(conversationDropPlacement({ active: { id: "a" }, over: { id: "foreign" } } as DragEndEvent, ["a", "b"])).toBeNull();
  });
  it.each([["a", "b", "after"], ["b", "a", "before"]] as const)("keeps keyboard %s → %s insertion direction", (movedId, targetId, position) => {
    const event = { active: { id: movedId, rect: { current: { translated: { top: 100, height: 30 } } } }, over: { id: targetId, rect: { top: 100, height: 30 } } } as DragEndEvent;
    expect(conversationDropPlacement(event, ["a", "b"])?.position).toBe(position);
  });
  it("moves a tall parent block above the preceding row with the keyboard", () => {
    const event = { activatorEvent: { type: "keydown" },
      active: { id: "parent", rect: { current: { translated: { top: 100, height: 120 } } } },
      over: { id: "a", rect: { top: 100, height: 30 } } } as DragEndEvent;
    expect(conversationDropPlacement(event, ["a", "parent"])?.position).toBe("before");
  });
});
