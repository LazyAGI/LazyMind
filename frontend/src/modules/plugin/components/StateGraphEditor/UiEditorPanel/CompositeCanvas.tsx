import { useCallback, useRef, useState, Fragment } from 'react';
import type { CSSProperties } from 'react';
import { PlusOutlined, CloseOutlined } from '@ant-design/icons';
import { Button, Select, Tooltip } from 'antd';
import type { CompositePanelNode } from '../core/pluginModel';
import type { SlotDef } from '../core/model';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Props {
  node: CompositePanelNode;
  slotMap: Record<string, SlotDef>;
  /** All slot ids that are already used somewhere in the tree (to prevent duplicates). */
  usedSlotIds: Set<string>;
  onChange: (updated: CompositePanelNode) => void;
}

// ---------------------------------------------------------------------------
// DividerHandle — draggable splitter between siblings
// ---------------------------------------------------------------------------

interface DividerHandleProps {
  direction: 'row' | 'column';
  onDrag: (delta: number) => void;
}

function DividerHandle({ direction, onDrag }: DividerHandleProps) {
  const startPos = useRef<number>(0);

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      startPos.current = direction === 'row' ? e.clientX : e.clientY;

      const handleMouseMove = (ev: MouseEvent) => {
        const pos = direction === 'row' ? ev.clientX : ev.clientY;
        onDrag(pos - startPos.current);
        startPos.current = pos;
      };

      const handleMouseUp = () => {
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
      };

      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [direction, onDrag],
  );

  return (
    <div
      className={`cc-divider cc-divider--${direction}`}
      onMouseDown={handleMouseDown}
      role='separator'
      aria-label='拖拽调整比例'
    />
  );
}

// ---------------------------------------------------------------------------
// LeafPane — a single pane that can hold a slot or a tabs group
// ---------------------------------------------------------------------------

interface LeafPaneProps {
  node: CompositePanelNode;
  slotMap: Record<string, SlotDef>;
  usedSlotIds: Set<string>;
  onChange: (updated: CompositePanelNode) => void;
  onRemove?: () => void;
  style?: CSSProperties;
}

function LeafPane({ node, slotMap, usedSlotIds, onChange, onRemove, style }: LeafPaneProps) {
  const [isDragOver, setIsDragOver] = useState(false);
  const isTabsNode = Array.isArray(node.tabs);
  const hasContent = isTabsNode ? (node.tabs!.length > 0) : !!node.slot;

  const slotOptions = Object.values(slotMap)
    .filter((s) => !usedSlotIds.has(s.id) || (node.slot === s.id))
    .map((s) => ({ value: s.id, label: s.label ?? s.id }));

  const handleDragOver = (e: React.DragEvent) => {
    if (e.dataTransfer.types.includes('application/x-slot-id')) {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
      setIsDragOver(true);
    }
  };

  const handleDragLeave = (e: React.DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsDragOver(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setIsDragOver(false);
    const slotId = e.dataTransfer.getData('application/x-slot-id');
    if (!slotId) return;
    if (isTabsNode) {
      if (!node.tabs!.includes(slotId)) onChange({ ...node, tabs: [...node.tabs!, slotId] });
    } else {
      onChange({ ...node, slot: slotId });
    }
  };

  const handleRemoveTab = (slotId: string) => {
    onChange({ ...node, tabs: node.tabs!.filter((t) => t !== slotId) });
  };

  const handleToggleTabs = (enabled: boolean) => {
    if (enabled) {
      onChange({ ...node, tabs: node.slot ? [node.slot] : [], slot: undefined });
    } else {
      onChange({ ...node, tabs: undefined, slot: node.tabs?.[0] ?? '' });
    }
  };

  return (
    <div
      className={`cc-leaf${isDragOver ? ' cc-leaf--drag-over' : ''}${!hasContent ? ' cc-leaf--empty' : ''}`}
      style={style}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div className='cc-leaf-toolbar'>
        <Tooltip title={isTabsNode ? '关闭 Tab 模式' : '启用 Tab 模式'}>
          <Button
            size='small'
            type={isTabsNode ? 'primary' : 'text'}
            onClick={() => handleToggleTabs(!isTabsNode)}
            className='cc-leaf-tab-toggle'
          >Tab</Button>
        </Tooltip>
        {onRemove && (
          <Tooltip title='移除此分块'>
            <Button size='small' type='text' danger icon={<CloseOutlined />} onClick={onRemove} />
          </Tooltip>
        )}
      </div>

      {!isTabsNode && (
        <div className='cc-leaf-slot-select'>
          <Select
            size='small'
            value={node.slot || undefined}
            placeholder='拖入素材或选择...'
            options={slotOptions}
            onChange={(v) => onChange({ ...node, slot: v ?? '' })}
            allowClear
            style={{ width: '100%' }}
          />
        </div>
      )}

      {isTabsNode && (
        <div className='cc-leaf-tabs'>
          {node.tabs!.map((slotId) => (
            <div key={slotId} className='cc-leaf-tab-chip'>
              <span className='cc-leaf-tab-chip-label'>{slotMap[slotId]?.label ?? slotId}</span>
              <Button size='small' type='text' icon={<CloseOutlined />} onClick={() => handleRemoveTab(slotId)} />
            </div>
          ))}
          <Select
            size='small'
            value={undefined}
            placeholder='添加 Tab...'
            options={slotOptions.filter((o) => !node.tabs!.includes(o.value))}
            onChange={(v) => { if (v) onChange({ ...node, tabs: [...(node.tabs ?? []), v] }); }}
            style={{ width: 120 }}
          />
        </div>
      )}

      {!hasContent && (
        <div className='cc-leaf-placeholder'>
          <PlusOutlined /><span>拖入素材</span>
        </div>
      )}

      {!isTabsNode && node.slot && (
        <div className='cc-leaf-slot-badge'>{slotMap[node.slot]?.label ?? node.slot}</div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Collect all used slot ids from a tree node
// ---------------------------------------------------------------------------

function collectUsedSlotIds(node: CompositePanelNode): Set<string> {
  const ids = new Set<string>();
  function walk(n: CompositePanelNode) {
    if (n.slot) ids.add(n.slot);
    if (n.tabs) n.tabs.forEach((t) => ids.add(t));
    if (n.children) n.children.forEach(walk);
  }
  walk(node);
  return ids;
}

// ---------------------------------------------------------------------------
// CanvasNode — recursive renderer
// ---------------------------------------------------------------------------

interface CanvasNodeProps {
  node: CompositePanelNode;
  parentDirection?: 'row' | 'column';
  slotMap: Record<string, SlotDef>;
  rootUsedSlotIds: Set<string>;
  onUpdate: (updated: CompositePanelNode) => void;
  onDelete?: () => void;
}

function CanvasNode({ node, parentDirection, slotMap, rootUsedSlotIds, onUpdate, onDelete }: CanvasNodeProps) {
  const elRef = useRef<HTMLDivElement>(null);
  const isLeaf = !node.direction && !node.children?.length;

  if (isLeaf) {
    return (
      <LeafPane
        node={node}
        slotMap={slotMap}
        usedSlotIds={rootUsedSlotIds}
        onChange={onUpdate}
        onRemove={onDelete}
        style={parentDirection ? { flex: node.weight ?? 1, minWidth: 0, minHeight: 0 } : undefined}
      />
    );
  }

  const dir = node.direction ?? 'row';
  const children = node.children ?? [];

  const handleWeightChange = (idx: number, delta: number) => {
    if (!elRef.current || children.length < 2) return;
    const containerSize = dir === 'row' ? elRef.current.clientWidth : elRef.current.clientHeight;
    if (!containerSize) return;

    const left = children[idx];
    const right = children[idx + 1];
    if (!left || !right) return;

    const totalW = (left.weight ?? 1) + (right.weight ?? 1);
    const pixPerWeight = containerSize / children.reduce((s, c) => s + (c.weight ?? 1), 0);
    const deltaWeight = delta / pixPerWeight;
    const newLeftW = Math.max(0.1, (left.weight ?? 1) + deltaWeight);
    const newRightW = Math.max(0.1, totalW - newLeftW);

    onUpdate({
      ...node,
      children: children.map((c, i) =>
        i === idx ? { ...c, weight: Math.round(newLeftW * 10) / 10 }
        : i === idx + 1 ? { ...c, weight: Math.round(newRightW * 10) / 10 }
        : c,
      ),
    });
  };

  const handleUpdateChild = (idx: number, updated: CompositePanelNode) => {
    onUpdate({ ...node, children: children.map((c, i) => (i === idx ? updated : c)) });
  };

  const handleDeleteChild = (idx: number) => {
    const next = children.filter((_, i) => i !== idx);
    onUpdate(next.length === 1 ? { ...next[0], weight: node.weight } : { ...node, children: next });
  };

  return (
    <div
      ref={elRef}
      className={`cc-container cc-container--${dir}`}
      style={parentDirection ? { flex: node.weight ?? 1 } : undefined}
    >
      {children.map((child, idx) => (
        <Fragment key={idx}>
          <CanvasNode
            node={child}
            parentDirection={dir}
            slotMap={slotMap}
            rootUsedSlotIds={rootUsedSlotIds}
            onUpdate={(u) => handleUpdateChild(idx, u)}
            onDelete={children.length > 1 ? () => handleDeleteChild(idx) : undefined}
          />
          {idx < children.length - 1 && (
            <DividerHandle direction={dir} onDrag={(delta) => handleWeightChange(idx, delta)} />
          )}
        </Fragment>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// CompositeCanvas — main exported component
// ---------------------------------------------------------------------------

export default function CompositeCanvas({ node, slotMap, onChange }: Props) {
  const usedSlotIds = collectUsedSlotIds(node);
  return (
    <div className='cc-root'>
      <CanvasNode node={node} slotMap={slotMap} rootUsedSlotIds={usedSlotIds} onUpdate={onChange} />
    </div>
  );
}
