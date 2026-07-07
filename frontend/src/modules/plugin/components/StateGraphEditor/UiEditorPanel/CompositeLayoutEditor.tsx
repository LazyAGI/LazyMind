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
import { Button, InputNumber, Select, Switch, Tooltip } from 'antd';
import { PlusOutlined, DeleteOutlined, HolderOutlined } from '@ant-design/icons';
import type { PluginUiTab } from '../core/pluginModel';
import type { SlotDef } from '../core/model';

// ── Types ─────────────────────────────────────────────────────────────────────

interface CompositeColumn {
  id: string;
  slot: string;
  weight: number;
}

/** New tree-model node for composite_layout. */
export interface CompositePanelNode {
  slot?: string;
  weight?: number;
  direction?: 'row' | 'column';
  children?: CompositePanelNode[];
  tabs?: Array<{ label?: string; slot: string }>;
  tabs_position?: 'top' | 'bottom' | 'left' | 'right';
}

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onChange: (value: unknown) => void;
}

// ── Legacy (array) format helpers ────────────────────────────────────────────

function isLegacyFormat(raw: unknown): boolean {
  return Array.isArray(raw);
}

function parseLegacyLayout(raw: unknown, slotIds: string[]): CompositeColumn[] {
  if (!Array.isArray(raw) || raw.length === 0) {
    return slotIds.map((id, i) => ({ id: `col_${i}`, slot: id, weight: 1 }));
  }
  return raw.map((node, i) => {
    if (typeof node === 'string') return { id: `col_${i}`, slot: node, weight: 1 };
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

function serializeLegacyColumns(cols: CompositeColumn[]): unknown {
  return cols.map((c) => ({ slot: c.slot, weight: c.weight }));
}

// ── Legacy editor (column cards) ─────────────────────────────────────────────

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

// ── Tree editor node (new format) ────────────────────────────────────────────

interface TreeNodeEditorProps {
  node: CompositePanelNode;
  slotOptions: { value: string; label: string }[];
  depth: number;
  onUpdate: (updated: CompositePanelNode) => void;
  onDelete?: () => void;
}

function TreeNodeEditor({ node, slotOptions, depth, onUpdate, onDelete }: TreeNodeEditorProps) {
  const isLeaf = !node.direction && !node.children?.length;
  const isTabsNode = (node.tabs?.length ?? 0) > 0;

  const handleToggleTabs = (enabled: boolean) => {
    if (enabled) {
      onUpdate({ ...node, tabs: [{ slot: node.slot ?? '' }], slot: undefined });
    } else {
      const firstSlot = node.tabs?.[0]?.slot ?? '';
      onUpdate({ ...node, tabs: undefined, slot: firstSlot });
    }
  };

  const handleAddChild = (direction: 'row' | 'column') => {
    const children = node.children ?? [];
    onUpdate({
      ...node,
      direction: node.direction ?? direction,
      children: [...children, { slot: '', weight: 1 }],
    });
  };

  const handleUpdateChild = (idx: number, updated: CompositePanelNode) => {
    const children = [...(node.children ?? [])];
    children[idx] = updated;
    onUpdate({ ...node, children });
  };

  const handleDeleteChild = (idx: number) => {
    const children = (node.children ?? []).filter((_, i) => i !== idx);
    onUpdate({ ...node, children });
  };

  const handleAddTab = () => {
    onUpdate({ ...node, tabs: [...(node.tabs ?? []), { slot: '', label: '' }] });
  };

  const handleUpdateTab = (idx: number, patch: Partial<{ label: string; slot: string }>) => {
    const tabs = (node.tabs ?? []).map((t, i) => i === idx ? { ...t, ...patch } : t);
    onUpdate({ ...node, tabs });
  };

  const handleDeleteTab = (idx: number) => {
    onUpdate({ ...node, tabs: (node.tabs ?? []).filter((_, i) => i !== idx) });
  };

  return (
    <div className={`cle-tree-node cle-tree-node--depth-${depth}`}>
      <div className="cle-tree-node-header">
        {node.direction && (
          <span className="cle-tree-dir-badge">
            {node.direction === 'row' ? '← 横向 →' : '↕ 纵向 ↕'}
          </span>
        )}
        {isLeaf && !isTabsNode && (
          <Select
            size="small"
            value={node.slot || undefined}
            placeholder="选择素材…"
            options={slotOptions}
            onChange={(v) => onUpdate({ ...node, slot: v ?? '' })}
            allowClear
            style={{ width: 140 }}
          />
        )}
        <InputNumber
          size="small"
          min={1}
          max={99}
          value={node.weight ?? 1}
          onChange={(v) => onUpdate({ ...node, weight: v ?? 1 })}
          style={{ width: 64 }}
          addonBefore="权重"
        />
        <div className="cle-tree-node-actions">
          {isLeaf && (
            <Tooltip title="启用 Tab 切换">
              <Switch
                size="small"
                checked={isTabsNode}
                onChange={handleToggleTabs}
              />
            </Tooltip>
          )}
          {!node.direction && !isTabsNode && (
            <>
              <Tooltip title="横向分割（添加子列）">
                <Button size="small" onClick={() => handleAddChild('row')}>+ 横向</Button>
              </Tooltip>
              <Tooltip title="纵向分割（添加子行）">
                <Button size="small" onClick={() => handleAddChild('column')}>+ 纵向</Button>
              </Tooltip>
            </>
          )}
          {node.direction && (
            <Tooltip title="添加子节点">
              <Button size="small" icon={<PlusOutlined />} onClick={() => handleAddChild(node.direction!)}>添加</Button>
            </Tooltip>
          )}
          {onDelete && (
            <Tooltip title="删除节点">
              <Button type="text" danger size="small" icon={<DeleteOutlined />} onClick={onDelete} />
            </Tooltip>
          )}
        </div>
      </div>

      {/* Tab editor */}
      {isTabsNode && (
        <div className="cle-tree-tabs">
          <div className="cle-tree-tabs-bar">
            <span className="cle-tree-tabs-label">Tab 位置:</span>
            <Select
              size="small"
              value={node.tabs_position ?? 'top'}
              options={[
                { value: 'top', label: '顶部' },
                { value: 'bottom', label: '底部' },
                { value: 'left', label: '左侧' },
                { value: 'right', label: '右侧' },
              ]}
              onChange={(v) => onUpdate({ ...node, tabs_position: v })}
              style={{ width: 80 }}
            />
          </div>
          {(node.tabs ?? []).map((tab, idx) => (
            <div key={idx} className="cle-tree-tab-row">
              <Select
                size="small"
                value={tab.slot || undefined}
                placeholder="素材…"
                options={slotOptions}
                onChange={(v) => handleUpdateTab(idx, { slot: v ?? '' })}
                style={{ width: 130 }}
              />
              <input
                className="cle-tree-tab-label-input"
                type="text"
                placeholder="Tab 标签（可选）"
                value={tab.label ?? ''}
                onChange={(e) => handleUpdateTab(idx, { label: e.target.value })}
              />
              <Button
                type="text"
                danger
                size="small"
                icon={<DeleteOutlined />}
                onClick={() => handleDeleteTab(idx)}
              />
            </div>
          ))}
          <Button size="small" icon={<PlusOutlined />} onClick={handleAddTab}>添加 Tab</Button>
        </div>
      )}

      {/* Children */}
      {node.children && node.children.length > 0 && (
        <div className={`cle-tree-children cle-tree-children--${node.direction ?? 'row'}`}>
          {node.children.map((child, idx) => (
            <TreeNodeEditor
              key={idx}
              node={child}
              slotOptions={slotOptions}
              depth={depth + 1}
              onUpdate={(updated) => handleUpdateChild(idx, updated)}
              onDelete={() => handleDeleteChild(idx)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ── Layout template picker ────────────────────────────────────────────────────

interface TemplatePickerProps {
  onSelect: (node: CompositePanelNode) => void;
}

const TEMPLATES: Array<{ label: string; node: CompositePanelNode; icon: string }> = [
  {
    label: '单列',
    icon: '▭',
    node: { direction: 'row', children: [{ slot: '', weight: 1 }] },
  },
  {
    label: '双列',
    icon: '▭▭',
    node: { direction: 'row', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  },
  {
    label: '上下',
    icon: '▬/▬',
    node: { direction: 'column', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  },
  {
    label: 'T形',
    icon: '▬/▭▭',
    node: {
      direction: 'column',
      children: [
        { slot: '', weight: 2 },
        { direction: 'row', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
      ],
    },
  },
  {
    label: '倒T形',
    icon: '▭▭/▬',
    node: {
      direction: 'column',
      children: [
        { direction: 'row', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
        { slot: '', weight: 2 },
      ],
    },
  },
  {
    label: 'L形',
    icon: '▭/▭▭',
    node: {
      direction: 'row',
      children: [
        { direction: 'column', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
        { slot: '', weight: 1 },
      ],
    },
  },
];

function TemplatePicker({ onSelect }: TemplatePickerProps) {
  return (
    <div className="cle-templates">
      <div className="cle-templates-title">选择初始布局</div>
      <div className="cle-templates-grid">
        {TEMPLATES.map((tpl) => (
          <button
            key={tpl.label}
            type="button"
            className="cle-template-btn"
            onClick={() => onSelect(tpl.node)}
          >
            <span className="cle-template-icon">{tpl.icon}</span>
            <span className="cle-template-label">{tpl.label}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export default function CompositeLayoutEditor({ tab, slotMap, onChange }: Props) {
  const slotIds = tab.slots.map((s) => s.id);
  const useLegacy = isLegacyFormat(tab.composite_layout);

  const [mode, setMode] = useState<'legacy' | 'tree'>(useLegacy ? 'legacy' : 'tree');

  // Legacy state
  const [columns, setColumns] = useState<CompositeColumn[]>(() =>
    parseLegacyLayout(tab.composite_layout, slotIds),
  );
  const [showAdvanced, setShowAdvanced] = useState(false);

  // Tree state
  const [treeRoot, setTreeRoot] = useState<CompositePanelNode | null>(() => {
    if (!useLegacy && tab.composite_layout && typeof tab.composite_layout === 'object' && !Array.isArray(tab.composite_layout)) {
      return tab.composite_layout as CompositePanelNode;
    }
    return null;
  });

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const slotOptions = slotIds.map((id) => ({
    value: id,
    label: slotMap[id]?.label ?? id,
  }));

  // ── Legacy handlers ─────────────────────────────────────────────────────────

  const commitLegacy = (cols: CompositeColumn[]) => {
    setColumns(cols);
    onChange(serializeLegacyColumns(cols));
  };

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = columns.findIndex((c) => c.id === active.id);
      const newIndex = columns.findIndex((c) => c.id === over.id);
      commitLegacy(arrayMove(columns, oldIndex, newIndex));
    }
  };

  const handleSlotChange = (id: string, slot: string) => {
    commitLegacy(columns.map((c) => (c.id === id ? { ...c, slot } : c)));
  };

  const handleWeightChange = (id: string, weight: number) => {
    commitLegacy(columns.map((c) => (c.id === id ? { ...c, weight } : c)));
  };

  const handleDeleteCol = (id: string) => {
    commitLegacy(columns.filter((c) => c.id !== id));
  };

  const handleAddColumn = () => {
    commitLegacy([...columns, { id: newColId(), slot: '', weight: 1 }]);
  };

  // ── Tree handlers ────────────────────────────────────────────────────────────

  const handleSelectTemplate = (node: CompositePanelNode) => {
    setTreeRoot(node);
    onChange(node);
  };

  const handleUpdateRoot = (updated: CompositePanelNode) => {
    setTreeRoot(updated);
    onChange(updated);
  };

  const handleSwitchToTree = () => {
    // Convert legacy columns to tree format
    const node: CompositePanelNode = {
      direction: 'row',
      children: columns.map((c) => ({ slot: c.slot, weight: c.weight })),
    };
    setTreeRoot(node);
    onChange(node);
    setMode('tree');
  };

  const handleSwitchToLegacy = () => {
    // Convert tree root to legacy format
    if (treeRoot?.children) {
      const cols = treeRoot.children.map((child, i) => ({
        id: `col_${i}`,
        slot: child.slot ?? '',
        weight: child.weight ?? 1,
      }));
      setColumns(cols);
      onChange(serializeLegacyColumns(cols));
    }
    setMode('legacy');
  };

  const totalWeight = columns.reduce((s, c) => s + c.weight, 0);

  return (
    <div className="cle-root">
      <div className="cle-header">
        <span className="cle-title">Composite 布局</span>
        <div className="cle-mode-switch">
          <span className="cle-mode-label">编辑模式:</span>
          <Button
            size="small"
            type={mode === 'legacy' ? 'primary' : 'default'}
            onClick={() => mode === 'tree' ? handleSwitchToLegacy() : undefined}
          >
            列编排
          </Button>
          <Button
            size="small"
            type={mode === 'tree' ? 'primary' : 'default'}
            onClick={() => mode === 'legacy' ? handleSwitchToTree() : undefined}
          >
            分割面板
          </Button>
        </div>
        {mode === 'legacy' && (
          <Button size="small" icon={<PlusOutlined />} onClick={handleAddColumn}>添加列</Button>
        )}
      </div>

      {/* ── Legacy mode ── */}
      {mode === 'legacy' && (
        <>
          {columns.length === 0 ? (
            <div className="cle-empty">暂无列，点击「添加列」或将素材从左侧拖入画布</div>
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
                      onDelete={handleDeleteCol}
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
              value={JSON.stringify(serializeLegacyColumns(columns), null, 2)}
              onChange={(e) => {
                try {
                  const parsed = JSON.parse(e.target.value);
                  if (Array.isArray(parsed)) {
                    const cols = parsed.map((n, i) => ({
                      id: `col_adv_${i}`,
                      slot: typeof n.slot === 'string' ? n.slot : '',
                      weight: typeof n.weight === 'number' ? n.weight : 1,
                    }));
                    commitLegacy(cols);
                  }
                } catch {
                  // ignore parse errors while typing
                }
              }}
            />
          )}
        </>
      )}

      {/* ── Tree mode ── */}
      {mode === 'tree' && (
        <>
          {!treeRoot ? (
            <TemplatePicker onSelect={handleSelectTemplate} />
          ) : (
            <div className="cle-tree-editor">
              <TreeNodeEditor
                node={treeRoot}
                slotOptions={slotOptions}
                depth={0}
                onUpdate={handleUpdateRoot}
              />
              <Button
                size="small"
                type="link"
                danger
                style={{ marginTop: 8 }}
                onClick={() => { setTreeRoot(null); onChange({}); }}
              >
                重置布局
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
