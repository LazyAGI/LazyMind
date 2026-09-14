import type { DragEvent } from "react";
export const CONVERSATION_DRAG = "application/x-lazymind-conversation";
export const GROUP_DRAG = "application/x-lazymind-group";
export function startConversationDrag(event: DragEvent, id: string, groupId?: string | null) {
  event.stopPropagation();
  event.dataTransfer.setData(CONVERSATION_DRAG, JSON.stringify({ id, groupId }));
  event.dataTransfer.effectAllowed = "move";
}
export function readConversationDrag(event: DragEvent): { id: string; groupId?: string } | null {
  try { return JSON.parse(event.dataTransfer.getData(CONVERSATION_DRAG)); } catch { return null; }
}
