import { useState } from 'react';
import {
  DndContext,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import type { DragEndEvent } from '@dnd-kit/core';
import {
  SortableContext,
  sortableKeyboardCoordinates,
  horizontalListSortingStrategy,
  arrayMove,
  useSortable,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Button, InputNumber, Select, Tooltip } from 'antd';
import { PlusOutlined, DeleteOutlined, HolderOutlined } from '@ant-design/icons';
import type { PluginUiTab } from '../core/pluginModel';
import type { SlotDef } from '../core/model';

interface CompositeColumn {
  id: string; // internal key for DnD
  slot: string; // slot id or '' if unselected
  weight: number;
}

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onChange: (value: unknown) => void;
}

function parseCompositeLayout(raw: unknown, slotIds: string[]): CompositeColumn[] {
  if (!Array.isArray(raw) || raw.length === 0) {
    return slotIds.map((id, i) => ({ id: `col_${i}`, slot: id, weight: 1 }));
  }
  return raw.map((node, i) => {
    if (typeof node === 'string') {
      return { id: `col_${i}`, slot: node, weight: 1 };
    }
    if (typeof node === 'object' && node !== null && 'slot' in node) {
      const n = node as { slot?: unknown; weight?: unknown };
      return {
        id: `col_${i}`,
        slot: typeof n.slot === 'string' ? n.slot : '',
        weight: typeof n.weight === 'number' ? n.weight : 1,
      };
    }
    return { id: `col_${i}`, slot: '', weight: 1 };
  });
}

function newColId() {
  return `col_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
}

function serializeColumns(cols: CompositeColumn[]): unknown {
  return cols.map((c) => ({ slot: c.slot, weight: c.weight }));
}

interface ColumnCardProps {
  col: CompositeColumn;
  slotOptions: { value: string; label: string }[];
  onSlotChange: (id: string, slot: string) => void;
  onWeightChange: (id: string, weight: number) => void;
  onDelete: (id: string) => void;
}

function ColumnCard({ col, slotOptions, onSlotChange, onWeightChange, onDelete }: ColumnCardProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: col.id });

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  };

  return (
    <div ref={setNodeRef} style={style} className="cle-col-card">
      <div className="cle-col-drag" {...attributes} {...listeners} aria-label="拖拽调整列顺序">
        <HolderOutlined />
      </div>
      <div className="cle-col-fields">
        <div className="cle-col-field">
          <span className="cle-col-label">素材</span>
          <Select
            size="small"
            value={col.slot || undefined}
            placeholder="选择素材…"
            options={slotOptions}
            onChange={(v) => onSlotChange(col.id, v ?? '')}
            allowClear
            style={{ width: 140 }}
          />
        </div>
        <div className="cle-col-field">
          <span className="cle-col-label">权重</span>
          <InputNumber
            size="small"
            min={1}
            max={99}
            value={col.weight}
            onChange={(v) => onWeightChange(col.id, v ?? 1)}
            style={{ width: 64 }}
          />
        </div>
      </div>
      <Tooltip title="删除此列">
        <Button
          type="text"
          danger
          size="small"
          icon={<DeleteOutlined />}
          onClick={() => onDelete(col.id)}
          className="cle-col-delete"
        />
      </Tooltip>
    </div>
  );
}

export default function CompositeLayoutEditor({ tab, slotMap, onChange }: Props) {
  const slotIds = tab.slots.map((s) => s.id);
  const [columns, setColumns] = useState<CompositeColumn[]>(() =>
    parseCompositeLayout(tab.composite_layout, slotIds),
  );
  const [showAdvanced, setShowAdvanced] = useState(false);

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const slotOptions = slotIds.map((id) => ({
    value: id,
    label: slotMap[id]?.label ?? id,
  }));

  const commit = (cols: CompositeColumn[]) => {
    setColumns(cols);
    onChange(serializeColumns(cols));
  };

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = columns.findIndex((c) => c.id === active.id);
      const newIndex = columns.findIndex((c) => c.id === over.id);
      commit(arrayMove(columns, oldIndex, newIndex));
    }
  };

  const handleSlotChange = (id: string, slot: string) => {
    commit(columns.map((c) => (c.id === id ? { ...c, slot } : c)));
  };

  const handleWeightChange = (id: string, weight: number) => {
    commit(columns.map((c) => (c.id === id ? { ...c, weight } : c)));
  };

  const handleDelete = (id: string) => {
    commit(columns.filter((c) => c.id !== id));
  };

  const handleAddColumn = () => {
    commit([...columns, { id: newColId(), slot: '', weight: 1 }]);
  };

  const totalWeight = columns.reduce((s, c) => s + c.weight, 0);

  return (
    <div className="cle-root">
      <div className="cle-header">
        <span className="cle-title">Composite 列布局</span>
        <Button size="small" icon={<PlusOutlined />} onClick={handleAddColumn}>
          添加列
        </Button>
      </div>

      {columns.length === 0 ? (
        <div className="cle-empty">
          暂无列，点击「添加列」或将素材从左侧拖入画布
        </div>
      ) : (
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <SortableContext items={columns.map((c) => c.id)} strategy={horizontalListSortingStrategy}>
            <div className="cle-cols">
              {columns.map((col) => (
                <ColumnCard
                  key={col.id}
                  col={col}
                  slotOptions={slotOptions}
                  onSlotChange={handleSlotChange}
                  onWeightChange={handleWeightChange}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>
      )}

      {columns.length > 0 && (
        <div className="cle-preview-bar">
          {columns.map((col, i) => {
            const pct = totalWeight > 0 ? (col.weight / totalWeight) * 100 : 100 / columns.length;
              const label = (slotMap[col.slot]?.label ?? col.slot) || '（未选）';
            return (
              <div
                key={col.id}
                className="cle-preview-segment"
                style={{ flexBasis: `${pct}%` }}
                title={`${label} (${Math.round(pct)}%)`}
              >
                <span className="cle-preview-label">{i + 1}. {label}</span>
              </div>
            );
          })}
        </div>
      )}

      <button
        type="button"
        className="cle-advanced-toggle"
        onClick={() => setShowAdvanced((v) => !v)}
      >
        {showAdvanced ? '▲ 收起高级编辑' : '▼ 高级 JSON 编辑'}
      </button>

      {showAdvanced && (
        <textarea
          className="cle-advanced-editor"
          rows={8}
          value={JSON.stringify(serializeColumns(columns), null, 2)}
          onChange={(e) => {
            try {
              const parsed = JSON.parse(e.target.value);
              if (Array.isArray(parsed)) {
                const cols = parsed.map((n, i) => ({
                  id: `col_adv_${i}`,
                  slot: typeof n.slot === 'string' ? n.slot : '',
                  weight: typeof n.weight === 'number' ? n.weight : 1,
                }));
                commit(cols);
              }
            } catch {
              // ignore parse errors while typing
            }
          }}
        />
      )}
    </div>
  );
}
