import type { DragEndEvent, DragMoveEvent } from "@dnd-kit/core";

export type ConversationPlacement = { movedId: string; targetId: string; position: "before" | "after" };

export function moveConversationItems<T extends { conversation_id?: string }>(items: T[], move: ConversationPlacement): T[] {
  const moved = items.find(item => item.conversation_id === move.movedId);
  if (!moved || move.movedId === move.targetId || !items.some(item => item.conversation_id === move.targetId)) return items;
  const next = items.filter(item => item !== moved);
  const index = next.findIndex(item => item.conversation_id === move.targetId);
  next.splice(index + (move.position === "after" ? 1 : 0), 0, moved);
  return next;
}

export function conversationDropPlacement(event: DragEndEvent | DragMoveEvent, ids: string[]): ConversationPlacement | null {
  const { active, over } = event;
  if (!over || active.id === over.id || !ids.includes(String(active.id)) || !ids.includes(String(over.id))) return null;
  const rect = active.rect?.current.translated;
  const pointer = event.activatorEvent && "clientY" in event.activatorEvent && event.delta
    ? Number(event.activatorEvent.clientY) + event.delta.y : undefined;
  const offset = over.rect && (pointer !== undefined || rect)
    ? (pointer ?? (rect!.top + rect!.height / 2)) - (over.rect.top + over.rect.height / 2) : 0;
  // Keyboard movement targets a row, including when a parent block is taller.
  const position = event.activatorEvent?.type === "keydown" || Math.abs(offset) <= 0.5
    ? ids.indexOf(String(active.id)) < ids.indexOf(String(over.id)) ? "after" : "before"
    : offset > 0 ? "after" : "before";
  return { movedId: String(active.id), targetId: String(over.id), position };
}
