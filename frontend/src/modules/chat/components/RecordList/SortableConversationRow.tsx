import type { ReactNode } from "react";
import { HolderOutlined } from "@ant-design/icons";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useTranslation } from "react-i18next";

export default function SortableConversationRow({
  id, title, disabled, hideDragHandle = false, scope, dropPosition, children,
}: {
  id: string;
  title: string;
  disabled: boolean;
  hideDragHandle?: boolean;
  scope: string;
  dropPosition?: "before" | "after";
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id, disabled, data: { scope, label: title },
  });
  return (
    <div
      ref={setNodeRef}
      className={`record-sortable${isDragging ? " record-sortable--dragging" : ""}`}
      data-conversation-id={id}
      data-drop-position={dropPosition}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      onPointerDown={event => {
        const target = event.target as HTMLElement;
        if (target.closest(".record-sortable") !== event.currentTarget) return;
        if (disabled || target.closest(".record-drag-handle,input,textarea,select,[contenteditable],.ant-dropdown-trigger")) return;
        const button = target.closest("button");
        if (button && !button.hasAttribute("data-conversation-title")) return;
        listeners?.onPointerDown?.(event);
      }}
    >
      {!hideDragHandle && <button
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        type="button"
        className="record-drag-handle"
        aria-disabled={disabled}
        aria-label={t("chat.reorderConversation", { name: title })}
        title={t("chat.reorderConversationHint")}
        onClick={(event) => event.stopPropagation()}
      ><HolderOutlined /></button>}
      {children}
    </div>
  );
}
