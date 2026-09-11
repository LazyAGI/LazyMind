import type { ReactNode } from "react";
import { HolderOutlined } from "@ant-design/icons";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useTranslation } from "react-i18next";

export default function SortableConversationRow({
  id, title, pinned, disabled, children,
}: {
  id: string;
  title: string;
  pinned: boolean;
  disabled: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id, disabled, data: { pinned },
  });
  return (
    <div
      ref={setNodeRef}
      className={`record-sortable${isDragging ? " record-sortable--dragging" : ""}`}
      style={{ transform: CSS.Transform.toString(transform), transition }}
    >
      <button
        ref={setActivatorNodeRef}
        {...attributes}
        {...listeners}
        type="button"
        className="record-drag-handle"
        aria-disabled={disabled}
        aria-label={t("chat.reorderConversation", { name: title })}
        title={t("chat.reorderConversationHint")}
        onClick={(event) => event.stopPropagation()}
      ><HolderOutlined /></button>
      {children}
    </div>
  );
}
