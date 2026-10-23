import { useId, type ReactNode } from "react";
import { HolderOutlined } from "@ant-design/icons";
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type CollisionDetection, type DragEndEvent, type Modifier } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useTranslation } from "react-i18next";
import { getProviderBrandDisplayName, getProviderDisplayName } from "../providerDisplayNames";
import { supplierOrderGroup, type OrderedSupplier } from "../supplierOrdering";
import "./SortableSupplierList.scss";

const restrictToVerticalAxis: Modifier = ({ transform }) => ({ ...transform, x: 0 });

function SortableSupplierRow({ item, disabled, children }: {
  item: OrderedSupplier; disabled: boolean; children: ReactNode;
}) {
  const { t, i18n } = useTranslation();
  const { setNodeRef, setActivatorNodeRef, attributes, listeners, transform, transition, isDragging } = useSortable({ id: item.id, disabled, data: { supplierGroup: supplierOrderGroup(item) } });
  return <div
    ref={setNodeRef}
    className={`provider-workspace-sortable-row${isDragging ? " is-dragging" : ""}`}
    data-provider-id={item.id}
    style={{ transform: CSS.Transform.toString(transform), transition }}
  >
    {!disabled ? <button
      ref={setActivatorNodeRef}
      {...attributes}
      {...listeners}
      type="button"
      className="provider-workspace-drag-handle"
      aria-label={t("modelProvider.workspace.reorderSupplier", { name: (item.isBrandGroup ? getProviderBrandDisplayName : getProviderDisplayName)(item, i18n?.resolvedLanguage || i18n?.language) })}
      title={t("modelProvider.workspace.reorderSupplierHint")}
      onClick={(event) => event.stopPropagation()}
    ><HolderOutlined aria-hidden="true" /></button> : null}
    {children}
  </div>;
}

export function SortableSupplierList<T extends OrderedSupplier>({ items, disabled, onReorder, children }: {
  items: T[];
  disabled: boolean;
  onReorder: (visibleIds: string[], movedId: string, targetId: string) => void;
  children: (item: T) => ReactNode;
}) {
  const { t, i18n } = useTranslation();
  const contextId = useId();
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
  const ids = items.map((item) => item.id);
  const counts = items.reduce((result, item) => result.set(supplierOrderGroup(item), (result.get(supplierOrderGroup(item)) || 0) + 1), new Map<number, number>());
  const sameSection: CollisionDetection = (args) => closestCenter({
    ...args,
    droppableContainers: args.droppableContainers.filter((candidate) => candidate.data.current?.supplierGroup === args.active.data.current?.supplierGroup),
  });
  const displayName = (id: string | number) => {
    const item = items.find((candidate) => candidate.id === id);
    return item ? (item.isBrandGroup ? getProviderBrandDisplayName : getProviderDisplayName)(item, i18n?.resolvedLanguage || i18n?.language) : "";
  };
  const dragEnd = ({ active, over }: DragEndEvent) => {
    if (!disabled && over) onReorder(ids, String(active.id), String(over.id));
  };
  return <DndContext id={contextId} sensors={sensors} modifiers={[restrictToVerticalAxis]} collisionDetection={sameSection} onDragEnd={dragEnd}
    accessibility={{
      screenReaderInstructions: { draggable: t("modelProvider.workspace.reorderSupplierHint") },
      announcements: {
        onDragStart: ({ active }) => t("modelProvider.workspace.reorderSupplierStarted", { name: displayName(active.id) }),
        onDragOver: ({ over }) => over ? t("modelProvider.workspace.reorderSupplierOver", { name: displayName(over.id) }) : undefined,
        onDragEnd: () => t("modelProvider.workspace.reorderSupplierEnded"),
        onDragCancel: () => t("modelProvider.workspace.reorderSupplierCanceled"),
      },
    }}>
    <SortableContext items={ids} strategy={verticalListSortingStrategy}>
      {items.map((item) => <SortableSupplierRow key={item.id} item={item}
        disabled={disabled || supplierOrderGroup(item) === 1 || (counts.get(supplierOrderGroup(item)) || 0) < 2}>
        {children(item)}
      </SortableSupplierRow>)}
    </SortableContext>
  </DndContext>;
}
