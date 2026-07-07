import { useState } from 'react';
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import type { DragEndEvent } from '@dnd-kit/core';
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  horizontalListSortingStrategy,
  rectSortingStrategy,
  arrayMove,
} from '@dnd-kit/sortable';
import { Empty } from 'antd';
import type { PluginUiTab, WidgetConfig } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import UiWidgetCard from './UiWidgetCard';
import CompositeLayoutEditor from './CompositeLayoutEditor';

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onSlotsChange: (slots: Array<{ id: string; widget?: WidgetConfig }>) => void;
  onCompositeLayoutChange: (value: unknown) => void;
}

export default function UiEditorCanvas({ tab, slotMap, onSlotsChange, onCompositeLayoutChange }: Props) {
  const [isDragOver, setIsDragOver] = useState(false);

  // activationConstraint: distance 8px to avoid triggering drag on config panel clicks
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const tabSlots = tab.slots;
  const slotIds = tabSlots.map((s) => s.id);

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = slotIds.indexOf(active.id as string);
      const newIndex = slotIds.indexOf(over.id as string);
      onSlotsChange(arrayMove(tabSlots, oldIndex, newIndex));
    }
  };

  const handleRemove = (slotId: string) => {
    onSlotsChange(tabSlots.filter((s) => s.id !== slotId));
  };

  const handleWidgetChange = (slotId: string, widget: WidgetConfig) => {
    onSlotsChange(tabSlots.map((s) => s.id === slotId ? { ...s, widget } : s));
  };

  // HTML5 drag-drop: accept slot dragged from ArtifactPanel
  const handleDragOver = (e: React.DragEvent) => {
    if (e.dataTransfer.types.includes('application/x-slot-id')) {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
      setIsDragOver(true);
    }
  };

  const handleDragLeave = (e: React.DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setIsDragOver(false);
    }
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setIsDragOver(false);
    const slotId = e.dataTransfer.getData('application/x-slot-id');
    const widgetTypeStr = e.dataTransfer.getData('application/x-widget-type');
    if (!slotId) return;

    const widget: WidgetConfig | undefined = widgetTypeStr
      ? ({ widgetType: widgetTypeStr } as WidgetConfig)
      : undefined;

    if (tab.layout === 'composite') {
      const currentLayout = Array.isArray(tab.composite_layout) ? tab.composite_layout : [];
      if (!currentLayout.some((c) => {
        if (typeof c === 'string') return c === slotId;
        if (typeof c === 'object' && c !== null && 'slot' in c) return (c as { slot?: unknown }).slot === slotId;
        return false;
      })) {
        onCompositeLayoutChange([...currentLayout, { slot: slotId, weight: 1 }]);
      }
      if (!slotIds.includes(slotId)) {
        onSlotsChange([...tabSlots, { id: slotId, widget }]);
      }
    } else {
      if (!slotIds.includes(slotId)) {
        onSlotsChange([...tabSlots, { id: slotId, widget }]);
      }
    }
  };

  const dropClass = isDragOver ? ' uep-canvas--drop-active' : '';

  // Choose sorting strategy based on tab layout
  const sortingStrategy =
    tab.layout === 'horizontal' ? horizontalListSortingStrategy
    : tab.layout === 'grid' ? rectSortingStrategy
    : verticalListSortingStrategy;

  if (tab.layout === 'composite') {
    return (
      <div
        className={`uep-canvas uep-canvas--composite${dropClass}`}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        <CompositeLayoutEditor
          tab={tab}
          slotMap={slotMap}
          onChange={onCompositeLayoutChange}
        />
      </div>
    );
  }

  if (slotIds.length === 0) {
    return (
      <div
        className={`uep-canvas uep-canvas--empty${dropClass}`}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="将左侧素材拖入此处，或点击素材行「加入 Tab」"
        />
      </div>
    );
  }

  return (
    <div
      className={`uep-canvas uep-canvas--${tab.layout ?? 'vertical'}${dropClass}`}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={slotIds} strategy={sortingStrategy}>
          <div className={`uep-canvas-slots uep-canvas-slots--${tab.layout ?? 'vertical'}`}>
            {tabSlots.map((s) => (
              <UiWidgetCard
                key={s.id}
                slotId={s.id}
                slotDef={slotMap[s.id]}
                widget={s.widget}
                onRemove={handleRemove}
                onWidgetChange={handleWidgetChange}
              />
            ))}
          </div>
        </SortableContext>
      </DndContext>
    </div>
  );
}
