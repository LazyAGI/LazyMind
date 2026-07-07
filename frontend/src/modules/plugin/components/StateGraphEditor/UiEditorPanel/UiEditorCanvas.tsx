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
import { Button, Empty } from 'antd';
import { CloseOutlined, FileTextOutlined } from '@ant-design/icons';
import type { PluginUiTab, WidgetConfig, WidgetType } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import { SLOT_TYPE_ICONS } from './slotTypeIcon';
import UiWidgetCard from './UiWidgetCard';
import WidgetSelector from './WidgetSelector';
import WidgetConfigPanel from './WidgetConfigPanel';
import WidgetPlaceholder from './WidgetPlaceholder';
import CompositeLayoutEditor from './CompositeLayoutEditor';

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onSlotsChange: (slots: Array<{ id: string; widget?: WidgetConfig }>) => void;
  onCompositeLayoutChange: (value: unknown) => void;
}

export default function UiEditorCanvas({ tab, slotMap, onSlotsChange, onCompositeLayoutChange }: Props) {
  const [isDragOver, setIsDragOver] = useState(false);
  const [selectedSlotId, setSelectedSlotId] = useState<string | null>(null);
  // Track drag state for insert indicator
  const [activeDragId, setActiveDragId] = useState<string | null>(null);
  const [overDragId, setOverDragId] = useState<string | null>(null);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const tabSlots = tab.slots;
  const slotIds = tabSlots.map((s) => s.id);

  const handleDragStart = (event: DragStartEvent) => {
    setActiveDragId(event.active.id as string);
  };

  const handleDragOver = (event: DragOverEvent) => {
    setOverDragId(event.over ? (event.over.id as string) : null);
  };

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
    if (selectedSlotId === slotId) setSelectedSlotId(null);
  };

  const handleWidgetChange = (slotId: string, widget: WidgetConfig) => {
    onSlotsChange(tabSlots.map((s) => s.id === slotId ? { ...s, widget } : s));
  };

  // HTML5 drag-drop: accept slot dragged from ArtifactPanel
  const handleExternalDragOver = (e: React.DragEvent) => {
    if (e.dataTransfer.types.includes('application/x-slot-id')) {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
      setIsDragOver(true);
    }
  };

  const handleExternalDragLeave = (e: React.DragEvent) => {
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

  const sortingStrategy =
    tab.layout === 'horizontal' ? horizontalListSortingStrategy
    : tab.layout === 'grid' ? rectSortingStrategy
    : verticalListSortingStrategy;

  // Resolve selected slot info for properties panel
  const selectedSlotEntry = tabSlots.find((s) => s.id === selectedSlotId);
  const selectedSlotDef = selectedSlotId ? slotMap[selectedSlotId] : undefined;
  const selectedType = selectedSlotDef?.type ?? 'text';
  const selectedCardinality = selectedSlotDef?.cardinality;
  const selectedSlotKey = `${selectedType}/${selectedCardinality ?? 'single'}`;
  const selectedDefaultWidget = (SLOT_DEFAULT_WIDGET[selectedSlotKey] ?? 'text-single') as WidgetType;
  const selectedWidget: WidgetConfig = selectedSlotEntry?.widget ?? ({ widgetType: selectedDefaultWidget } as WidgetConfig);
  const selectedLabel = selectedSlotDef?.label ?? selectedSlotId ?? '';
  const selectedIcon = SLOT_TYPE_ICONS[selectedType] ?? <FileTextOutlined />;

  // Build drag overlay content
  const activeDragSlot = tabSlots.find((s) => s.id === activeDragId);
  const activeDragDef = activeDragId ? slotMap[activeDragId] : undefined;

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
          onChange={onCompositeLayoutChange}
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
          description="将左侧素材拖入此处，或点击素材行「加入 Tab」"
        />
      </div>
    );
  }

  const isHorizontal = tab.layout === 'horizontal';

  return (
    <div
      className={`uep-canvas-with-props${selectedSlotId ? ' uep-canvas-with-props--open' : ''}`}
    >
      <div
        className={`uep-canvas uep-canvas--${tab.layout ?? 'vertical'}${dropClass}`}
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
            <div className={`uep-canvas-slots uep-canvas-slots--${tab.layout ?? 'vertical'}`}>
              {tabSlots.map((s, idx) => {
                const isDragTarget = overDragId === s.id && activeDragId !== s.id;
                // Determine indicator position: before or after based on drag direction
                const activeIdx = slotIds.indexOf(activeDragId ?? '');
                const currentIdx = idx;
                const showBefore = isDragTarget && activeIdx > currentIdx;
                const showAfter = isDragTarget && activeIdx < currentIdx;

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
                      widget={s.widget}
                      isSelected={selectedSlotId === s.id}
                      onSelect={setSelectedSlotId}
                      onRemove={handleRemove}
                      onWidgetChange={handleWidgetChange}
                    />
                  </div>
                );
              })}
            </div>
          </SortableContext>

          <DragOverlay>
            {activeDragId && activeDragSlot ? (
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

      {/* Right-side properties panel */}
      {selectedSlotId && (
        <div className="uep-props-panel">
          <div className="uep-props-panel-header">
            <span className="uep-props-panel-icon">{selectedIcon}</span>
            <span className="uep-props-panel-title">{selectedLabel}</span>
            <WidgetSelector
              slotType={selectedType}
              cardinality={selectedCardinality}
              value={selectedWidget.widgetType}
              onChange={(newType) => {
                handleWidgetChange(selectedSlotId, { widgetType: newType } as WidgetConfig);
              }}
              size="small"
            />
            <Button
              type="text"
              size="small"
              icon={<CloseOutlined />}
              onClick={() => setSelectedSlotId(null)}
              className="uep-props-panel-close"
            />
          </div>
          <div className="uep-props-panel-body">
            <WidgetConfigPanel
              config={selectedWidget}
              onChange={(next) => handleWidgetChange(selectedSlotId, next)}
            />
          </div>
        </div>
      )}
    </div>
  );
}
