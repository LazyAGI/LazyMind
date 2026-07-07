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
  arrayMove,
} from '@dnd-kit/sortable';
import { Empty } from 'antd';
import type { PluginUiTab } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import UiWidgetCard from './UiWidgetCard';
import CompositeLayoutEditor from './CompositeLayoutEditor';

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onSlotsChange: (slotIds: string[]) => void;
  onCompositeLayoutChange: (value: unknown) => void;
}

export default function UiEditorCanvas({ tab, slotMap, onSlotsChange, onCompositeLayoutChange }: Props) {
  const [isDragOver, setIsDragOver] = useState(false);

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const slotIds = tab.slots.map((s) => s.id);

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = slotIds.indexOf(active.id as string);
      const newIndex = slotIds.indexOf(over.id as string);
      onSlotsChange(arrayMove(slotIds, oldIndex, newIndex));
    }
  };

  const handleRemove = (slotId: string) => {
    onSlotsChange(slotIds.filter((id) => id !== slotId));
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
    if (!slotId) return;

    if (tab.layout === 'composite') {
      // Append as a new column in composite_layout
      const currentLayout = Array.isArray(tab.composite_layout) ? tab.composite_layout : [];
      if (!currentLayout.some((c) => {
        if (typeof c === 'string') return c === slotId;
        if (typeof c === 'object' && c !== null && 'slot' in c) return (c as { slot?: unknown }).slot === slotId;
        return false;
      })) {
        onCompositeLayoutChange([...currentLayout, { slot: slotId, weight: 1 }]);
      }
      // Also ensure slot is in tab.slots
      if (!slotIds.includes(slotId)) {
        onSlotsChange([...slotIds, slotId]);
      }
    } else {
      if (!slotIds.includes(slotId)) {
        onSlotsChange([...slotIds, slotId]);
      }
    }
  };

  const dropClass = isDragOver ? ' uep-canvas--drop-active' : '';

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
      className={`uep-canvas uep-canvas--${tab.layout ?? 'list'}${dropClass}`}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={slotIds} strategy={verticalListSortingStrategy}>
          <div className="uep-canvas-slots">
            {slotIds.map((id) => (
              <UiWidgetCard
                key={id}
                slotId={id}
                slotDef={slotMap[id]}
                onRemove={handleRemove}
              />
            ))}
          </div>
        </SortableContext>
      </DndContext>
    </div>
  );
}
