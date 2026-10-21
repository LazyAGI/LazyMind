import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent, type DragMoveEvent, type DragStartEvent, type DragOverEvent } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";
import { ChatServiceApi, type ConversationOrderResult } from "@/modules/chat/utils/request";
import SortableConversationRow from "./SortableConversationRow";
import { conversationDropPlacement, moveConversationItems, type ConversationPlacement } from "./conversationReorder";
import "./index.scss";

export default function ScopedConversationList<T extends { conversation_id: string; display_name?: string }>({
  items, scope, disabled = false, isItemDisabled, onCommitted, onRefresh, onSavingChange, children,
}: {
  items: T[];
  scope: string;
  disabled?: boolean;
  isItemDisabled?: (item: T) => boolean;
  onCommitted: (move: ConversationPlacement, result: ConversationOrderResult) => void;
  onRefresh?: () => void | Promise<void>;
  onSavingChange?: (saving: boolean) => void;
  children: (item: T) => ReactNode;
}) {
  const { t } = useTranslation();
  const [pending, setPending] = useState<ConversationPlacement | null>(null);
  const [drop, setDrop] = useState<ConversationPlacement | null>(null);
  const [saving, setSaving] = useState(false);
  const request = useRef(0);
  const inFlight = useRef(false);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const callbacks = useRef({ onCommitted, onRefresh, onSavingChange });
  callbacks.current = { onCommitted, onRefresh, onSavingChange };
  useEffect(() => {
    ++request.current;
    inFlight.current = false;
    setPending(null); setDrop(null); setSaving(false);
    return () => {
      ++request.current;
      if (inFlight.current) { inFlight.current = false; callbacks.current.onSavingChange?.(false); }
    };
  }, [scope]);
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));
  const ids = items.map(item => item.conversation_id);
  const updateDrop = (event: DragMoveEvent) => setDrop(conversationDropPlacement(event, ids));
  const reorder = async (event: DragEndEvent) => {
    setDrop(null);
    const move = conversationDropPlacement(event, ids);
    if (!move || disabled || inFlight.current || items.some(item => (item.conversation_id === move.movedId || item.conversation_id === move.targetId) && isItemDisabled?.(item))) return;
    const token = ++request.current;
    inFlight.current = true; setSaving(true); setPending(move);
    callbacks.current.onSavingChange?.(true);
    try {
      const response = await ChatServiceApi().conversationServiceReorder(move.movedId, move.targetId, move.position);
      if (token !== request.current || scope !== currentScope.current) return;
      callbacks.current.onCommitted(move, response.data);
    } catch {
      if (token === request.current && scope === currentScope.current) message.error(t("chat.reorderConversationFailed"));
    } finally {
      if (token === request.current && scope === currentScope.current) {
        inFlight.current = false; setSaving(false); setPending(null);
        callbacks.current.onSavingChange?.(false);
        void callbacks.current.onRefresh?.();
      }
    }
  };
  const visible = pending ? moveConversationItems(items, pending) : items;
  return <div className="conversation-sort-list" aria-busy={saving}>
    <DndContext id={scope} sensors={sensors} collisionDetection={closestCenter} onDragMove={updateDrop} onDragOver={updateDrop} onDragCancel={() => setDrop(null)} onDragEnd={reorder}
      accessibility={{ screenReaderInstructions: { draggable: t("chat.reorderConversationHint") }, announcements: {
        onDragStart: ({ active }: DragStartEvent) => t("chat.reorderConversationStarted", { name: items.find(item => item.conversation_id === active.id)?.display_name }),
        onDragOver: ({ over }: DragOverEvent) => over ? t("chat.reorderConversationOver", { name: items.find(item => item.conversation_id === over.id)?.display_name }) : undefined,
        onDragEnd: () => t("chat.reorderConversationEnded"), onDragCancel: () => t("chat.reorderConversationCanceled"),
      } }}>
      <SortableContext items={visible.map(item => item.conversation_id)} strategy={verticalListSortingStrategy}>
        {visible.map(item => <SortableConversationRow key={item.conversation_id} id={item.conversation_id} title={item.display_name || item.conversation_id}
          scope={scope} disabled={disabled || saving || Boolean(isItemDisabled?.(item))}
          dropPosition={drop?.targetId === item.conversation_id ? drop.position : undefined}>
          {children(item)}
        </SortableConversationRow>)}
      </SortableContext>
    </DndContext>
  </div>;
}
