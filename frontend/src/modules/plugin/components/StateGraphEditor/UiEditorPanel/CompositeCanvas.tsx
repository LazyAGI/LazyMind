import { useCallback, useRef, useState, Fragment } from 'react';
import type { CSSProperties } from 'react';
import { PlusOutlined, CloseOutlined, SettingOutlined, SplitCellsOutlined, EditOutlined } from '@ant-design/icons';
import { Button, Input, Modal, Popconfirm, Popover, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import type { CompositePanelNode, CompositeTab, WidgetConfig } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import WidgetPlaceholder from './WidgetPlaceholder';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type PageBarPosition = 'top' | 'bottom' | 'left' | 'right';

interface Props {
  node: CompositePanelNode;
  slotMap: Record<string, SlotDef>;
  usedSlotIds?: Set<string>;
  uiSlots?: Record<string, WidgetConfig>;
  pageBarPosition?: PageBarPosition;
  onPageBarPositionChange?: (pos: PageBarPosition) => void;
  onChange: (updated: CompositePanelNode) => void;
}

// ---------------------------------------------------------------------------
// DividerHandle
// ---------------------------------------------------------------------------

interface DividerHandleProps {
  direction: 'row' | 'column';
  onDrag: (delta: number) => void;
}

function DividerHandle({ direction, onDrag }: DividerHandleProps) {
  const startPos = useRef<number>(0);
  const onDragRef = useRef(onDrag);
  onDragRef.current = onDrag;

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      startPos.current = direction === 'row' ? e.clientX : e.clientY;

      const handleMouseMove = (ev: MouseEvent) => {
        const pos = direction === 'row' ? ev.clientX : ev.clientY;
        onDragRef.current(pos - startPos.current);
        startPos.current = pos;
      };

      const handleMouseUp = () => {
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
      };

      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [direction],
  );

  return (
    <div
      className={`cc-divider cc-divider--${direction}`}
      onMouseDown={handleMouseDown}
      role='separator'
      aria-label='drag-resize'
    />
  );
}

// ---------------------------------------------------------------------------
// PageBar
// ---------------------------------------------------------------------------

interface PageBarProps {
  position: PageBarPosition;
  onPositionChange: (pos: PageBarPosition) => void;
}

function PageBar({ position, onPositionChange }: PageBarProps) {
  const { t } = useTranslation();
  const isCol = position === 'left' || position === 'right';
  const [popoverOpen, setPopoverOpen] = useState(false);
  const [hovered, setHovered] = useState(false);

  const totalPages = 8;
  const currentPage = 4;
  const COLLAPSED_RADIUS = 2;
  const MAX_EXPANDED = 10;

  const directions: Array<{ value: PageBarPosition; label: string }> = [
    { value: 'top', label: t('selfEvolutionRun.compositePageBarTop') },
    { value: 'bottom', label: t('selfEvolutionRun.compositePageBarBottom') },
    { value: 'left', label: t('selfEvolutionRun.compositePageBarLeft') },
    { value: 'right', label: t('selfEvolutionRun.compositePageBarRight') },
  ];

  const allPages = Array.from({ length: totalPages }, (_, i) => i + 1);

  const expandedPages = (() => {
    if (totalPages <= MAX_EXPANDED) return allPages;
    const half = Math.floor(MAX_EXPANDED / 2);
    const start = Math.max(1, Math.min(currentPage - half, totalPages - MAX_EXPANDED + 1));
    const end = Math.min(totalPages, start + MAX_EXPANDED - 1);
    return allPages.slice(start - 1, end);
  })();

  const visiblePages = hovered
    ? expandedPages
    : allPages.filter((p) => Math.abs(p - currentPage) <= COLLAPSED_RADIUS);

  const displayedFirst = visiblePages[0] ?? 1;
  const displayedLast = visiblePages[visiblePages.length - 1] ?? totalPages;
  const showTopEllipsis = displayedFirst > 1;
  const showBottomEllipsis = displayedLast < totalPages;

  const popoverContent = (
    <div className='cc-pagebar-popover'>
      <div className='cc-pagebar-popover-title'>Dock side</div>
      <div className='cc-pagebar-popover-btns'>
        {directions.map((d) => (
          <button
            key={d.value}
            type='button'
            className={`cc-pagebar-dock-btn${position === d.value ? ' cc-pagebar-dock-btn--active' : ''}`}
            onClick={() => { onPositionChange(d.value); setPopoverOpen(false); }}
          >
            {d.label}
          </button>
        ))}
      </div>
    </div>
  );

  return (
    <div
      className={`cc-pagebar cc-pagebar--${isCol ? 'col' : 'row'}`}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <button type='button' className='cc-pagebar-arrow' aria-label='prev-page'>
        {isCol ? '∧' : '‹'}
      </button>
      <div className={`cc-pagebar-pages cc-pagebar-pages--${isCol ? 'col' : 'row'}`}>
        {showTopEllipsis && <span className='cc-pagebar-ellipsis'>{isCol ? '⋮' : '…'}</span>}
        {visiblePages.map((p) => (
          <button
            key={p}
            type='button'
            className={`cc-pagebar-page${p === currentPage ? ' cc-pagebar-page--active' : ''}`}
          >
            {p}
          </button>
        ))}
        {showBottomEllipsis && <span className='cc-pagebar-ellipsis'>{isCol ? '⋮' : '…'}</span>}
      </div>
      <button type='button' className='cc-pagebar-arrow' aria-label='next-page'>
        {isCol ? '∨' : '›'}
      </button>
      <Popover
        content={popoverContent}
        trigger='click'
        open={popoverOpen}
        onOpenChange={setPopoverOpen}
        placement={isCol ? 'right' : 'top'}
      >
        <button type='button' className='cc-pagebar-setting' aria-label='pagebar-position-setting'>
          <SettingOutlined />
        </button>
      </Popover>
    </div>
  );
}

// ---------------------------------------------------------------------------
// LeafPane
// ---------------------------------------------------------------------------

interface LeafPaneProps {
  node: CompositePanelNode;
  slotMap: Record<string, SlotDef>;
  uiSlots: Record<string, WidgetConfig>;
  usedSlotIds: Set<string>;
  /** Cardinality constraint from sibling slots already bound in this composite. */
  listConstraint?: 'list' | 'single';
  onChange: (updated: CompositePanelNode) => void;
  onRemove?: () => void;
  onSplitRow?: () => void;
  onSplitCol?: () => void;
  style?: CSSProperties;
}

function LeafPane({ node, slotMap, uiSlots, onChange, onRemove, onSplitRow, onSplitCol, style, listConstraint }: LeafPaneProps) {
  const { t } = useTranslation();
  const [isDragOver, setIsDragOver] = useState(false);
  const [activeTabIdx, setActiveTabIdx] = useState(0);
  const [renamingTabIdx, setRenamingTabIdx] = useState<number | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [pendingDropSlotId, setPendingDropSlotId] = useState<string | null>(null);
  const [listConflictSlotId, setListConflictSlotId] = useState<string | null>(null);
  const [renamingBlock, setRenamingBlock] = useState(false);
  const [blockLabelValue, setBlockLabelValue] = useState('');
  const [actionsOpen, setActionsOpen] = useState(false);
  const [confirmRemoveTabIdx, setConfirmRemoveTabIdx] = useState<number | null>(null);

  const isTabsNode = Array.isArray(node.tabs);
  const tabs = node.tabs ?? [];
  const hasContent = isTabsNode ? tabs.length > 0 : !!node.slot;

  const handleDragOver = (e: React.DragEvent) => {
    if (e.dataTransfer.types.includes('application/x-slot-id')) {
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = 'copy';
      setIsDragOver(true);
    }
  };

  const handleDragLeave = (e: React.DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsDragOver(false);
  };

  const doAssignSlot = (slotId: string) => {
    if (isTabsNode) {
      const idx = Math.min(activeTabIdx, tabs.length - 1);
      if (idx < 0) return;
      const updated = tabs.map((t, i) => i === idx ? { ...t, slot: slotId } : t);
      onChange({ ...node, tabs: updated });
    } else {
      onChange({ ...node, slot: slotId });
    }
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragOver(false);
    const slotId = e.dataTransfer.getData('application/x-slot-id');
    if (!slotId) return;

    // Check list constraint
    if (listConstraint !== undefined) {
      const def = slotMap[slotId];
      const incomingCardinality: 'list' | 'single' = def?.cardinality === 'list' ? 'list' : 'single';
      if (incomingCardinality !== listConstraint) {
        setListConflictSlotId(slotId);
        return;
      }
    }

    // Check if the target already has a slot
    const targetHasSlot = isTabsNode
      ? (tabs[Math.min(activeTabIdx, tabs.length - 1)]?.slot ?? '') !== ''
      : !!node.slot;

    if (targetHasSlot) {
      setPendingDropSlotId(slotId);
    } else {
      doAssignSlot(slotId);
    }
  };

  const handleAddTab = () => {
    if (!isTabsNode) {
      // Convert to tabs mode: create 2 tabs, first inherits current slot
      const tab1: CompositeTab = { label: 'Tab 1', slot: node.slot ?? '' };
      const tab2: CompositeTab = { label: 'Tab 2', slot: '' };
      onChange({ ...node, tabs: [tab1, tab2], slot: undefined });
      setActiveTabIdx(1);
      // Auto-rename Tab 2
      setRenamingTabIdx(1);
      setRenameValue('Tab 2');
    } else {
      // Add one more tab
      const newLabel = `Tab ${tabs.length + 1}`;
      const newTab: CompositeTab = { label: newLabel, slot: '' };
      const newTabs = [...tabs, newTab];
      onChange({ ...node, tabs: newTabs });
      const newIdx = newTabs.length - 1;
      setActiveTabIdx(newIdx);
      setRenamingTabIdx(newIdx);
      setRenameValue(newLabel);
    }
  };

  const handleRemoveTab = (idx: number) => {
    const newTabs = tabs.filter((_, i) => i !== idx);
    if (newTabs.length <= 1) {
      // Collapse back to single-slot (keep the remaining tab's slot, or the one before)
      const remaining = newTabs[0];
      onChange({ ...node, tabs: undefined, slot: remaining?.slot ?? '' });
      setActiveTabIdx(0);
    } else {
      onChange({ ...node, tabs: newTabs });
      setActiveTabIdx((prev) => Math.min(prev, newTabs.length - 1));
    }
    if (renamingTabIdx === idx) setRenamingTabIdx(null);
  };

  const commitTabRename = (idx: number) => {
    const updated = tabs.map((t, i) => i === idx ? { ...t, label: renameValue.trim() || t.label } : t);
    onChange({ ...node, tabs: updated });
    setRenamingTabIdx(null);
  };

  const startBlockRename = () => {
    setBlockLabelValue(node.label ?? '');
    setRenamingBlock(true);
  };

  const commitBlockRename = () => {
    onChange({ ...node, label: blockLabelValue.trim() || undefined });
    setRenamingBlock(false);
  };

  const blockLabel = node.label ?? '';

  return (
    <div
      className={`cc-leaf${isDragOver ? ' cc-leaf--drag-over' : ''}${!hasContent ? ' cc-leaf--empty' : ''}`}
      style={style}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* Overwrite confirmation modal */}
      <Modal
        open={!!pendingDropSlotId}
        title={t('selfEvolutionRun.ccConfirmOverwrite')}
        okText={t('selfEvolutionRun.ccConfirmOverwrite')}
        cancelText={t('selfEvolutionRun.ccCancel')}
        okButtonProps={{ danger: true }}
        onOk={() => {
          if (pendingDropSlotId) doAssignSlot(pendingDropSlotId);
          setPendingDropSlotId(null);
        }}
        onCancel={() => setPendingDropSlotId(null)}
        centered
        width={360}
      >
        {t('selfEvolutionRun.ccConfirmOverwriteBody')}
      </Modal>

      {/* List constraint conflict modal */}
      <Modal
        open={!!listConflictSlotId}
        title={t('selfEvolutionRun.ccListConflictTitle')}
        okText={t('selfEvolutionRun.ccIUnderstand')}
        cancelButtonProps={{ style: { display: 'none' } }}
        onOk={() => setListConflictSlotId(null)}
        onCancel={() => setListConflictSlotId(null)}
        centered
        width={400}
      >
        {listConstraint === 'list'
          ? t('selfEvolutionRun.ccListConflictBodyList')
          : t('selfEvolutionRun.ccListConflictBodySingle')}
      </Modal>

      {/* Toolbar */}
      <div className='cc-leaf-toolbar'>
        {/* Block label / rename */}
        <div className='cc-leaf-block-label'>
          {renamingBlock ? (
            <Input
              size='small'
              autoFocus
              value={blockLabelValue}
              onChange={(e) => setBlockLabelValue(e.target.value)}
              onBlur={commitBlockRename}
              onPressEnter={commitBlockRename}
              className='cc-leaf-block-label-input'
              placeholder={t('selfEvolutionRun.ccBlockNamePlaceholder')}
            />
          ) : (
            <span
              className='cc-leaf-block-label-text'
              title={t('selfEvolutionRun.ccBlockRenameTooltip')}
              onDoubleClick={startBlockRename}
            >
              {blockLabel || <span className='cc-leaf-block-label-placeholder'>{t('selfEvolutionRun.ccBlockDefaultLabel')}</span>}
              <EditOutlined className='cc-leaf-block-label-edit-icon' onClick={startBlockRename} />
            </span>
          )}
        </div>

        <div
          className={`cc-leaf-toolbar-actions${actionsOpen ? ' cc-leaf-toolbar-actions--open' : ''}`}
          onMouseEnter={() => setActionsOpen(true)}
          onMouseLeave={() => setActionsOpen(false)}
        >
          {/* Expanded action buttons — slide in on hover */}
          <div className='cc-leaf-toolbar-expanded'>
            <Tooltip title={isTabsNode ? t('selfEvolutionRun.ccAddTabPage') : t('selfEvolutionRun.ccEnableTab')} placement='top'>
              <Button
                size='small'
                type='text'
                onClick={() => { handleAddTab(); setActionsOpen(false); }}
                className='cc-leaf-action-btn cc-leaf-action-btn--tab'
              >Tab</Button>
            </Tooltip>
            {onSplitRow && (
            <Tooltip title={t('selfEvolutionRun.ccSplitRight')} placement='top'>
                <Button
                  size='small'
                  type='text'
                  icon={<SplitCellsOutlined />}
                  onClick={() => { onSplitRow(); setActionsOpen(false); }}
                  className='cc-leaf-action-btn'
                />
              </Tooltip>
            )}
            {onSplitCol && (
            <Tooltip title={t('selfEvolutionRun.ccSplitDown')} placement='top'>
                <Button
                  size='small'
                  type='text'
                  icon={<SplitCellsOutlined rotate={90} />}
                  onClick={() => { onSplitCol(); setActionsOpen(false); }}
                  className='cc-leaf-action-btn'
                />
              </Tooltip>
            )}
          </div>
          {/* Always-visible + trigger */}
          <Button
            size='small'
            type='text'
            icon={<PlusOutlined />}
            className='cc-leaf-add-trigger'
          />
          {onRemove && (
            <Popconfirm
              title={t('selfEvolutionRun.ccRemoveBlockTitle')}
              description={t('selfEvolutionRun.ccRemoveBlockDesc')}
              onConfirm={onRemove}
              okText={t('selfEvolutionRun.ccRemoveBlockOk')}
              cancelText={t('selfEvolutionRun.ccCancel')}
              okButtonProps={{ danger: true }}
            >
              <Tooltip title={t('selfEvolutionRun.ccRemoveBlockTooltip')}>
                <Button size='small' type='text' danger icon={<CloseOutlined />} className='cc-leaf-remove-btn' />
              </Tooltip>
            </Popconfirm>
          )}
        </div>
      </div>

      {/* Tabs mode */}
      {isTabsNode && (
        <div className='cc-leaf-tabs'>
          <div className='cc-leaf-tab-bar'>
            {tabs.map((tab, idx) => (
              <div
                key={idx}
                className={`cc-leaf-tab-chip${activeTabIdx === idx ? ' cc-leaf-tab-chip--active' : ''}`}
                onClick={() => { if (renamingTabIdx !== idx) setActiveTabIdx(idx); }}
              >
                {renamingTabIdx === idx ? (
                  <Input
                    size='small'
                    autoFocus
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    onBlur={() => commitTabRename(idx)}
                    onPressEnter={() => commitTabRename(idx)}
                    onClick={(e) => e.stopPropagation()}
                    className='cc-leaf-tab-chip-input'
                  />
                ) : (
                  <span
                    className='cc-leaf-tab-chip-label'
                    onDoubleClick={(e) => { e.stopPropagation(); setRenamingTabIdx(idx); setRenameValue(tab.label); }}
                  >
                    {tab.label}
                  </span>
                )}
                <Popconfirm
                  title={t('selfEvolutionRun.ccDeleteTabTitle')}
                  description={tabs.length <= 2 ? t('selfEvolutionRun.ccDeleteTabLastDesc') : undefined}
                  open={confirmRemoveTabIdx === idx}
                  onConfirm={(e) => { e?.stopPropagation(); handleRemoveTab(idx); setConfirmRemoveTabIdx(null); }}
                  onCancel={(e) => { e?.stopPropagation(); setConfirmRemoveTabIdx(null); }}
                  okText={t('selfEvolutionRun.ccDeleteTabOk')}
                  cancelText={t('selfEvolutionRun.ccCancel')}
                  okButtonProps={{ danger: true }}
                >
                  <Button
                    size='small'
                    type='text'
                    icon={<CloseOutlined />}
                    onClick={(e) => { e.stopPropagation(); setConfirmRemoveTabIdx(idx); }}
                    className='cc-leaf-tab-chip-close'
                  />
                </Popconfirm>
              </div>
            ))}
          </div>
          {tabs.length > 0 && (() => {
            const activeTab = tabs[Math.min(activeTabIdx, tabs.length - 1)];
            if (!activeTab?.slot) {
              return (
                <div className='cc-leaf-placeholder'>
                  <PlusOutlined /><span>{t('selfEvolutionRun.ccDropSlotHere')}</span>
                </div>
              );
            }
            const widget = uiSlots[activeTab.slot] ?? { widgetType: 'text-single' as const };
            const label = slotMap[activeTab.slot]?.label ?? activeTab.slot;
            return (
              <div className='cc-leaf-tab-preview'>
                <WidgetPlaceholder widgetConfig={widget} label={label} />
              </div>
            );
          })()}
        </div>
      )}

      {/* Single slot mode — no slot yet */}
      {!isTabsNode && !node.slot && (
        <div className='cc-leaf-placeholder'>
          <PlusOutlined /><span>{t('selfEvolutionRun.ccDropSlotHere')}</span>
        </div>
      )}

      {/* Single slot mode — has slot */}
      {!isTabsNode && node.slot && (
        <div className='cc-leaf-wysiwyg'>
          <WidgetPlaceholder
            widgetConfig={uiSlots[node.slot] ?? { widgetType: 'text-single' as const }}
            label={slotMap[node.slot]?.label ?? node.slot}
          />
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Collect list constraint from the tree (based on already-bound non-empty slots)
// ---------------------------------------------------------------------------

function collectListConstraint(node: CompositePanelNode, slotMap: Record<string, SlotDef>): 'list' | 'single' | undefined {
  function walk(n: CompositePanelNode): 'list' | 'single' | undefined {
    if (n.slot) {
      const def = slotMap[n.slot];
      if (def) return def.cardinality === 'list' ? 'list' : 'single';
    }
    if (n.tabs) {
      for (const t of n.tabs) {
        if (t.slot) {
          const def = slotMap[t.slot];
          if (def) return def.cardinality === 'list' ? 'list' : 'single';
        }
      }
    }
    if (n.children) {
      for (const c of n.children) {
        const result = walk(c);
        if (result !== undefined) return result;
      }
    }
    return undefined;
  }
  return walk(node);
}



function collectUsedSlotIds(node: CompositePanelNode): Set<string> {
  const ids = new Set<string>();
  function walk(n: CompositePanelNode) {
    if (n.slot) ids.add(n.slot);
    if (n.tabs) n.tabs.forEach((t) => { if (t.slot) ids.add(t.slot); });
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
  depth?: number;
  slotMap: Record<string, SlotDef>;
  uiSlots: Record<string, WidgetConfig>;
  rootUsedSlotIds: Set<string>;
  listConstraint?: 'list' | 'single';
  onUpdate: (updated: CompositePanelNode) => void;
  onDelete?: () => void;
}

function CanvasNode({ node, parentDirection, depth = 0, slotMap, uiSlots, rootUsedSlotIds, listConstraint, onUpdate, onDelete }: CanvasNodeProps) {
  const elRef = useRef<HTMLDivElement>(null);
  const isLeaf = !node.direction && !node.children?.length;

  if (isLeaf) {
    const handleSplit = (splitDir: 'row' | 'column') => {
      onUpdate({
        direction: splitDir,
        weight: node.weight,
        children: [
          { slot: node.slot, tabs: node.tabs, label: node.label, weight: 1 },
          { slot: '', weight: 1 },
        ],
      });
    };

    const canSplitRow = depth < 2 || parentDirection === 'row';
    const canSplitCol = depth < 2 || parentDirection === 'column';

    return (
      <LeafPane
        node={node}
        slotMap={slotMap}
        uiSlots={uiSlots}
        usedSlotIds={rootUsedSlotIds}
        listConstraint={listConstraint}
        onChange={onUpdate}
        onRemove={onDelete}
        onSplitRow={canSplitRow ? () => handleSplit('row') : undefined}
        onSplitCol={canSplitCol ? () => handleSplit('column') : undefined}
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

    const allTotalW = children.reduce((s, c) => s + (c.weight ?? 1), 0);
    const deltaWeight = (delta / containerSize) * allTotalW;

    const pairTotal = (left.weight ?? 1) + (right.weight ?? 1);
    const newLeftW = Math.max(0.1, Math.min(pairTotal - 0.1, (left.weight ?? 1) + deltaWeight));
    const newRightW = pairTotal - newLeftW;

    onUpdate({
      ...node,
      children: children.map((c, i) =>
        i === idx ? { ...c, weight: Math.round(newLeftW * 100) / 100 }
        : i === idx + 1 ? { ...c, weight: Math.round(newRightW * 100) / 100 }
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
            depth={depth + 1}
            slotMap={slotMap}
            uiSlots={uiSlots}
            rootUsedSlotIds={rootUsedSlotIds}
            listConstraint={listConstraint}
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

export default function CompositeCanvas({
  node,
  slotMap,
  usedSlotIds: externalUsedSlotIds,
  uiSlots = {},
  pageBarPosition = 'bottom',
  onPageBarPositionChange,
  onChange,
}: Props) {
  const computedUsedSlotIds = externalUsedSlotIds ?? collectUsedSlotIds(node);
  const listConstraint = slotMap ? collectListConstraint(node, slotMap) : undefined;
  const pageBarEl = onPageBarPositionChange ? (
    <PageBar position={pageBarPosition} onPositionChange={onPageBarPositionChange} />
  ) : null;

  return (
    <div className={`cc-with-pagebar cc-with-pagebar--${pageBarPosition}`}>
      {pageBarEl}
      <div className='cc-root'>
        <CanvasNode
          node={node}
          slotMap={slotMap}
          uiSlots={uiSlots}
          rootUsedSlotIds={computedUsedSlotIds}
          listConstraint={listConstraint}
          onUpdate={onChange}
        />
      </div>
    </div>
  );
}
