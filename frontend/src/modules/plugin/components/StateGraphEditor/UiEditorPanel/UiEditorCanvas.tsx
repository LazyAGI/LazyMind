import { useState } from 'react';
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  DragOverlay,
} from '@dnd-kit/core';
import type { DragEndEvent, DragStartEvent, DragOverEvent } from '@dnd-kit/core';
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  horizontalListSortingStrategy,
  rectSortingStrategy,
  arrayMove,
} from '@dnd-kit/sortable';
import { useTranslation } from 'react-i18next';
import { Empty } from 'antd';
import { FileTextOutlined } from '@ant-design/icons';
import type { PluginUiTab, WidgetConfig, CompositePanelNode } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import { SLOT_TYPE_ICONS } from './slotTypeIcon';
import UiWidgetCard from './UiWidgetCard';
import CompositeLayoutEditor from './CompositeLayoutEditor';

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  /** ui.slots map from PluginModel. */
  uiSlots: Record<string, WidgetConfig>;
  gridCols?: number;
  selectedSlotId: string | null;
  onSelectSlot: (slotId: string | null) => void;
  onSlotsChange: (slots: Array<{ id: string }>) => void;
  onCompositeLayoutChange: (value: CompositePanelNode) => void;
  onCompositeTabPositionChange: (pos: PluginUiTab['composite_tab_position']) => void;
}

export default function UiEditorCanvas({
  tab,
  slotMap,
  uiSlots,
  gridCols,
  selectedSlotId,
  onSelectSlot,
  onSlotsChange,
  onCompositeLayoutChange,
  onCompositeTabPositionChange,
}: Props) {
  const { t } = useTranslation();
  const [isDragOver, setIsDragOver] = useState(false);
  const [activeDragId, setActiveDragId] = useState<string | null>(null);
  const [overDragId, setOverDragId] = useState<string | null>(null);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const tabSlots = tab.slots;
  const slotIds = tabSlots.map((s) => s.id);

  const handleDragStart = (event: DragStartEvent) => setActiveDragId(event.active.id as string);
  const handleDragOver = (event: DragOverEvent) => setOverDragId(event.over ? (event.over.id as string) : null);

  const handleDragEnd = (event: DragEndEvent) => {
    setActiveDragId(null);
    setOverDragId(null);
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = slotIds.indexOf(active.id as string);
      const newIndex = slotIds.indexOf(over.id as string);
      onSlotsChange(arrayMove(tabSlots, oldIndex, newIndex));
    }
  };

  const handleRemove = (slotId: string) => {
    onSlotsChange(tabSlots.filter((s) => s.id !== slotId));
    if (selectedSlotId === slotId) onSelectSlot(null);
  };

  const handleExternalDragOver = (e: React.DragEvent) => {
    if (e.dataTransfer.types.includes('application/x-slot-id')) {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
      setIsDragOver(true);
    }
  };

  const handleExternalDragLeave = (e: React.DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsDragOver(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setIsDragOver(false);
    const slotId = e.dataTransfer.getData('application/x-slot-id');
    if (!slotId) return;

    if (tab.layout === 'composite') {
      // Only do bookkeeping here; the inner LeafPane already handled the actual drop
      if (!e.defaultPrevented && !slotIds.includes(slotId)) {
        onSlotsChange([...tabSlots, { id: slotId }]);
      } else if (!slotIds.includes(slotId)) {
        onSlotsChange([...tabSlots, { id: slotId }]);
      }
    } else {
      if (!slotIds.includes(slotId)) {
        onSlotsChange([...tabSlots, { id: slotId }]);
      }
    }
  };

  const dropClass = isDragOver ? ' uep-canvas--drop-active' : '';

  const sortingStrategy =
    tab.layout === 'horizontal' ? horizontalListSortingStrategy
    : tab.layout === 'grid' ? rectSortingStrategy
    : verticalListSortingStrategy;

  const activeDragDef = activeDragId ? slotMap[activeDragId] : undefined;
  const isHorizontal = tab.layout === 'horizontal';

  if (tab.layout === 'composite') {
    return (
      <div
        className={`uep-canvas uep-canvas--composite${dropClass}`}
        onDragOver={handleExternalDragOver}
        onDragLeave={handleExternalDragLeave}
        onDrop={handleDrop}
      >
        <CompositeLayoutEditor
          key={`${tab.id}-composite`}
          tab={tab}
          slotMap={slotMap}
          uiSlots={uiSlots}
          onChange={onCompositeLayoutChange}
          onPageBarPositionChange={onCompositeTabPositionChange}
        />
      </div>
    );
  }

  if (slotIds.length === 0) {
    return (
      <div
        className={`uep-canvas uep-canvas--empty${dropClass}`}
        onDragOver={handleExternalDragOver}
        onDragLeave={handleExternalDragLeave}
        onDrop={handleDrop}
      >
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
           description={t('selfEvolutionRun.uiEditorCanvasEmptyDesc')}
        />
      </div>
    );
  }

  return (
    <div className={`uep-canvas uep-canvas--${tab.layout ?? 'vertical'}${dropClass}`}
      onDragOver={handleExternalDragOver}
      onDragLeave={handleExternalDragLeave}
      onDrop={handleDrop}
    >
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragStart={handleDragStart}
        onDragOver={handleDragOver}
        onDragEnd={handleDragEnd}
      >
        <SortableContext items={slotIds} strategy={sortingStrategy}>
          <div
            className={`uep-canvas-slots uep-canvas-slots--${tab.layout ?? 'vertical'}`}
            style={tab.layout === 'grid' && gridCols ? { '--uep-grid-cols': `repeat(${gridCols}, 1fr)` } as React.CSSProperties : undefined}
          >
            {tabSlots.map((s, idx) => {
              const isDragTarget = overDragId === s.id && activeDragId !== s.id;
              const activeIdx = slotIds.indexOf(activeDragId ?? '');
              const showBefore = isDragTarget && activeIdx > idx;
              const showAfter = isDragTarget && activeIdx < idx;

              return (
                <div
                  key={s.id}
                  className={`uep-widget-card-wrapper${isDragTarget ? ' uep-widget-card-wrapper--drag-over' : ''}`}
                  data-show-before={showBefore ? (isHorizontal ? 'left' : 'top') : undefined}
                  data-show-after={showAfter ? (isHorizontal ? 'right' : 'bottom') : undefined}
                >
                  <UiWidgetCard
                    slotId={s.id}
                    slotDef={slotMap[s.id]}
                    widget={uiSlots[s.id]}
                    isSelected={selectedSlotId === s.id}
                    onSelect={onSelectSlot}
                    onRemove={handleRemove}
                  />
                </div>
              );
            })}
          </div>
        </SortableContext>

        <DragOverlay>
          {activeDragId && tabSlots.find((s) => s.id === activeDragId) ? (
            <div className="uep-drag-ghost">
              <span className="uep-drag-ghost-icon">
                {SLOT_TYPE_ICONS[activeDragDef?.type ?? 'text'] ?? <FileTextOutlined />}
              </span>
              <span className="uep-drag-ghost-label">
                {activeDragDef?.label ?? activeDragId}
              </span>
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
