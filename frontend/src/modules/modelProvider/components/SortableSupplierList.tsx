import { useId, useLayoutEffect, useRef, type ReactNode } from "react";
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
  const container = useRef<HTMLDivElement>(null);
  const positions = useRef(new Map<string, number>());
  const orderKey = JSON.stringify(ids);
  const previousOrder = useRef(orderKey);
  useLayoutEffect(() => {
    const changed = previousOrder.current !== orderKey;
    previousOrder.current = orderKey;
    const rows = container.current?.querySelectorAll<HTMLElement>(".provider-workspace-sortable-row");
    const origin = container.current?.getBoundingClientRect().top || 0;
    const next = new Map<string, number>();
    rows?.forEach(row => {
      const id = row.dataset.providerId!;
      const top = row.getBoundingClientRect().top - origin;
      next.set(id, top);
      const previous = positions.current.get(id);
      if (changed && previous !== undefined && previous !== top && !row.classList.contains("is-dragging") &&
          !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        row.animate?.([{ transform: `translateY(${previous - top}px)` }, { transform: "translateY(0)" }],
          { duration: 360, easing: "cubic-bezier(.22, 1, .36, 1)" });
      }
    });
    positions.current = next;
  });
  const displayName = (id: string | number) => {
    const item = items.find((candidate) => candidate.id === id);
    return item ? (item.isBrandGroup ? getProviderBrandDisplayName : getProviderDisplayName)(item, i18n?.resolvedLanguage || i18n?.language) : "";
  };
  const dragEnd = ({ active, over }: DragEndEvent) => {
    if (!disabled && over) onReorder(ids, String(active.id), String(over.id));
  };
  return <DndContext id={contextId} sensors={sensors} modifiers={[restrictToVerticalAxis]} collisionDetection={closestCenter} onDragEnd={dragEnd}
    onDragStart={() => container.current?.querySelectorAll<HTMLElement>(".provider-workspace-sortable-row").forEach(row => row.getAnimations?.().forEach(animation => animation.cancel()))}
    accessibility={{
      screenReaderInstructions: { draggable: t("modelProvider.workspace.reorderSupplierHint") },
      announcements: {
        onDragStart: ({ active }) => t("modelProvider.workspace.reorderSupplierStarted", { name: displayName(active.id) }),
        onDragOver: ({ over }) => over ? t("modelProvider.workspace.reorderSupplierOver", { name: displayName(over.id) }) : undefined,
        onDragEnd: () => t("modelProvider.workspace.reorderSupplierEnded"),
        onDragCancel: () => t("modelProvider.workspace.reorderSupplierCanceled"),
      },
    }}>
    <div ref={container} className="provider-workspace-sortable-list">
    <SortableContext items={ids} strategy={verticalListSortingStrategy}>
      {items.map((item) => <SortableSupplierRow key={item.id} item={item}
        disabled={disabled} onVisibilityChange={onVisibilityChange} restoring={restoring}>
        {children(item)}
      </SortableSupplierRow>)}
    </SortableContext>
    </div>
  </DndContext>;
}
