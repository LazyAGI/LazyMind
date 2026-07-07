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
import { Input, Empty } from 'antd';
import type { PluginUiTab } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import UiWidgetCard from './UiWidgetCard';

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onSlotsChange: (slotIds: string[]) => void;
  onCompositeLayoutChange: (value: string) => void;
}

export default function UiEditorCanvas({ tab, slotMap, onSlotsChange, onCompositeLayoutChange }: Props) {
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

  if (tab.layout === 'composite') {
    return (
      <div className="uep-canvas uep-canvas--composite">
        <p className="uep-canvas-composite-hint">
          composite 布局：直接编辑 <code>composite_layout</code> YAML 片段
        </p>
        <Input.TextArea
          rows={12}
          value={
            typeof tab.composite_layout === 'string'
              ? tab.composite_layout
              : JSON.stringify(tab.composite_layout ?? '', null, 2)
          }
          onChange={(e) => onCompositeLayoutChange(e.target.value)}
          className="uep-canvas-composite-editor"
          placeholder="在此输入 composite_layout 配置…"
        />
      </div>
    );
  }

  if (slotIds.length === 0) {
    return (
      <div className="uep-canvas uep-canvas--empty">
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="点击「素材」按钮，将素材加入此 Tab"
        />
      </div>
    );
  }

  return (
    <div className={`uep-canvas uep-canvas--${tab.layout ?? 'list'}`}>
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
