import { useId, type ReactNode } from "react";
import { Dropdown } from "antd";
import { HolderOutlined } from "@ant-design/icons";
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent, type Modifier } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useTranslation } from "react-i18next";
import { getProviderBrandDisplayName, getProviderDisplayName } from "../providerDisplayNames";
import { type OrderedSupplier } from "../supplierOrdering";
import "./SortableSupplierList.scss";

const restrictToVerticalAxis: Modifier = ({ transform }) => ({ ...transform, x: 0 });

function SortableSupplierRow({ item, disabled, children, onVisibilityChange, restoring }: {
  item: OrderedSupplier; disabled: boolean; children: ReactNode; onVisibilityChange?: (id: string) => void; restoring?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id: item.id, disabled });
  return <div
    ref={setNodeRef}
    className={`provider-workspace-sortable-row${isDragging ? " is-dragging" : ""}`}
    data-provider-id={item.id}
    style={{ transform: CSS.Transform.toString(transform), transition }}
  >
    {!disabled ? <Dropdown trigger={onVisibilityChange ? ["click"] : []} disabled={isDragging} menu={{ items: [{ key: "visibility", label: t(`modelProvider.workspace.${restoring ? "restoreSupplier" : "hideSupplier"}`), onClick: () => onVisibilityChange?.(item.id) }] }}>
    <button
      ref={setActivatorNodeRef}
      {...attributes}
      {...listeners}
      type="button"
      className="provider-workspace-drag-handle"
      aria-label={t("modelProvider.workspace.reorderSupplier", { name: (item.isBrandGroup ? getProviderBrandDisplayName : getProviderDisplayName)(item, i18n?.resolvedLanguage || i18n?.language) })}
      onClick={(event) => { if (isDragging) event.preventDefault(); event.stopPropagation(); }}
    ><HolderOutlined aria-hidden="true" /></button></Dropdown> : null}
    {children}
  </div>;
}

export function SortableSupplierList<T extends OrderedSupplier>({ items, disabled, onReorder, children, onVisibilityChange, restoring }: {
  items: T[];
  disabled: boolean;
  onReorder: (visibleIds: string[], movedId: string, targetId: string) => void;
  children: (item: T) => ReactNode;
  onVisibilityChange?: (id: string) => void;
  restoring?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const contextId = useId();
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const ids = items.map((item) => item.id);
  const displayName = (id: string | number) => {
    const item = items.find((candidate) => candidate.id === id);
    return item ? (item.isBrandGroup ? getProviderBrandDisplayName : getProviderDisplayName)(item, i18n?.resolvedLanguage || i18n?.language) : "";
  };
  const dragEnd = ({ active, over }: DragEndEvent) => {
    if (!disabled && over) onReorder(ids, String(active.id), String(over.id));
  };
  return <DndContext id={contextId} sensors={sensors} modifiers={[restrictToVerticalAxis]} collisionDetection={closestCenter} onDragEnd={dragEnd}
    accessibility={{
      screenReaderInstructions: { draggable: t("modelProvider.workspace.reorderSupplierHint") },
      announcements: {
        onDragStart: ({ active }) => t("modelProvider.workspace.reorderSupplierStarted", { name: displayName(active.id) }),
        onDragOver: ({ over }) => over ? t("modelProvider.workspace.reorderSupplierOver", { name: displayName(over.id) }) : undefined,
        onDragEnd: () => t("modelProvider.workspace.reorderSupplierEnded"),
        onDragCancel: () => t("modelProvider.workspace.reorderSupplierCanceled"),
      },
    }}>
    <div className="provider-workspace-sortable-list">
    <SortableContext items={ids} strategy={verticalListSortingStrategy}>
      {items.map((item) => <SortableSupplierRow key={item.id} item={item}
        disabled={disabled} onVisibilityChange={onVisibilityChange} restoring={restoring}>
        {children(item)}
      </SortableSupplierRow>)}
    </SortableContext>
    </div>
  </DndContext>;
}
