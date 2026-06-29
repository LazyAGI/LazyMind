import React, { useEffect, useState, useCallback, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { usePluginSession } from '@/modules/chat/hooks/usePlugin';
import { usePluginStore } from '@/modules/chat/store/pluginPanel';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import type {
  PluginSession,
  SlotRevision,
  TabDef,
  PluginUI,
  SlotDef,
  CompositeLayoutNode,
  CompositeColumnNode,
  InnerTabsNode,
} from '@/modules/chat/store/pluginPanel';
import { SlotRenderer, SlotEditingContext } from './SlotComponents';
import './PluginPanel.scss';

interface PluginPanelProps {
  conversationId: string;
  pollIntervalMs?: number;
  /** Called when the user clicks Continue or Retry — simulates sending a user message. */
  onSendMessage?: (text: string) => void;
  /** Called when the user clicks the reference button on a slot item. */
  onReference?: (slot: SlotRevision) => void;
}

/**
 * AutoSlotGrid renders all available slot revisions in a responsive grid,
 * without requiring a pre-defined UI spec.
 */
function AutoSlotGrid({
  session,
  onRefresh,
  onReference,
}: {
  session: PluginSession;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
}) {
  if (!session.slots || session.slots.length === 0) {
    return (
      <div className='plugin-panel__empty' role='status' aria-live='polite'>
        <span>Waiting for results…</span>
      </div>
    );
  }

  const bySlot: Record<string, SlotRevision[]> = {};
  for (const s of session.slots) {
    if (!s.selected) continue;
    if (!bySlot[s.slot_id]) bySlot[s.slot_id] = [];
    bySlot[s.slot_id].push(s);
  }

  return (
    <div className='plugin-panel__auto-grid'>
      {Object.entries(bySlot).map(([slotId, revisions]) => (
        <div key={slotId} className='plugin-panel__slot-group'>
          <span className='plugin-panel__slot-label'>{slotId}</span>
          <div className='plugin-panel__slot-items'>
            {revisions.map((rev) => (
              <SlotRenderer
                key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                slot={rev}
                sessionId={session.session_id}
                slotId={slotId}
                revisionCount={rev.revision_count}
                onRefresh={onRefresh}
                onReference={onReference}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

/**
 * CompositeSlotGrid renders a composite-layout tab where multiple slots are
 * aligned by sort_order. Each row corresponds to one sort_order value; within
 * a row, columns are laid out according to composite_layout.
 */

// ---------------------------------------------------------------------------
// Helpers for composite_layout parsing
// ---------------------------------------------------------------------------

function isInnerTabsNode(node: CompositeLayoutNode): node is InnerTabsNode {
  return typeof node === 'object' && node !== null && 'tabs' in node;
}

function isColumnNode(node: CompositeLayoutNode): node is CompositeColumnNode {
  return typeof node === 'object' && node !== null && 'slot' in node;
}

/** Resolve a leaf node to { slotId, weight }. Returns null for unknown shapes. */
function resolveColumnSlotId(
  node: CompositeLayoutNode,
): { slotId: string | InnerTabsNode; weight: number } | null {
  if (typeof node === 'string') {
    return { slotId: node, weight: 1 };
  }
  if (isColumnNode(node)) {
    if (node.slot === undefined) return null;
    return { slotId: node.slot, weight: node.weight ?? 1 };
  }
  return null;
}

/** Build the effective column list from composite_layout (or fall back to slot ids). */
function buildColumns(
  tab: TabDef,
): Array<{ slotId: string | InnerTabsNode; weight: number }> {
  const layout = tab.composite_layout;
  if (!layout || layout.length === 0) {
    // Fallback: all slots side-by-side with equal weight.
    return tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }
  // The top-level array may be a single [...] parallel node or an explicit list of columns.
  // Detect whether the first element is itself an array (parallel node).
  const first = layout[0];
  const cols =
    Array.isArray(first)
      ? (first as CompositeLayoutNode[])
      : layout;
  return cols
    .map((n) => resolveColumnSlotId(n))
    .filter((c): c is NonNullable<typeof c> => c !== null);
}

/** Get all distinct sort_orders present across the participating slots. */
function getCompositeRows(
  tab: TabDef,
  session: PluginSession,
): number[] {
  const participating = new Set(tab.slots.map((s) => s.artifact_key ?? s.id));
  const orders = new Set<number>();
  for (const slot of session.slots ?? []) {
    if (slot.selected && participating.has(slot.artifact_key ?? slot.slot_id)) {
      if (slot.sort_order !== undefined) {
        orders.add(slot.sort_order);
      }
    }
  }
  return Array.from(orders).sort((a, b) => a - b);
}

/** Find a slot revision for (artifact_key, sort_order). */
function findSlotRevision(
  session: PluginSession,
  artifactKey: string,
  sortOrder: number,
): SlotRevision | undefined {
  return (session.slots ?? []).find(
    (s) => s.selected && (s.artifact_key ?? s.slot_id) === artifactKey && s.sort_order === sortOrder,
  );
}

// ---------------------------------------------------------------------------
// InnerTabsCell: renders an {tabs: [...]} node for a single row
// ---------------------------------------------------------------------------

function InnerTabsCell({
  tabsNode,
  session,
  slotDefs,
  sortOrder,
  onRefresh,
  onReference,
}: {
  tabsNode: InnerTabsNode;
  session: PluginSession;
  slotDefs: SlotDef[];
  sortOrder: number;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
}) {
  const [activeIdx, setActiveIdx] = useState(0);

  const innerSlotIds = tabsNode.tabs
    .map((n) => (typeof n === 'string' ? n : isColumnNode(n) ? (typeof n.slot === 'string' ? n.slot : null) : null))
    .filter((id): id is string => id !== null);

  return (
    <div className='composite-cell__inner-tabs'>
      <div className='composite-cell__inner-tab-bar' role='tablist'>
        {innerSlotIds.map((slotId, i) => {
          const def = slotDefs.find((s) => s.id === slotId);
          return (
            <button
              key={slotId}
              role='tab'
              aria-selected={i === activeIdx}
              className={`composite-cell__inner-tab-btn${i === activeIdx ? ' composite-cell__inner-tab-btn--active' : ''}`}
              onClick={() => setActiveIdx(i)}
              type='button'
            >
              {def?.label ?? slotId}
            </button>
          );
        })}
      </div>
      {innerSlotIds.map((slotId, i) => {
        const def = slotDefs.find((s) => s.id === slotId);
        const artifactKey = def?.artifact_key ?? slotId;
        const rev = findSlotRevision(session, artifactKey, sortOrder);
        return (
          <div key={slotId} role='tabpanel' hidden={i !== activeIdx}>
            {rev ? (
              <SlotRenderer
                slot={rev}
                sessionId={session.session_id}
                slotId={slotId}
                revisionCount={rev.revision_count}
                onRefresh={onRefresh}
                onReference={onReference}
              />
            ) : (
              <div className='composite-cell__empty'>—</div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// CompositeSlotGrid
// ---------------------------------------------------------------------------

function CompositeSlotGrid({
  tab,
  session,
  onRefresh,
  onReference,
  onFocusSortOrder,
}: {
  tab: TabDef;
  session: PluginSession;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
}) {
  const rows = getCompositeRows(tab, session);
  const columns = buildColumns(tab);

  // Compute total weight for flex proportions.
  const totalWeight = columns.reduce((s, c) => s + c.weight, 0);

  if (rows.length === 0) {
    return (
      <div className='plugin-panel__empty' role='status' aria-live='polite'>
        <span>Waiting for results…</span>
      </div>
    );
  }

  return (
    <div className='composite-grid'>
      {rows.map((sortOrder) => (
        <div
          key={sortOrder}
          className='composite-grid__row'
          onClick={() => onFocusSortOrder?.(sortOrder)}
          role='button'
          tabIndex={0}
          aria-label={`行 ${sortOrder}`}
        >
          {columns.map((col, colIdx) => {
            const flexBasis = `${(col.weight / totalWeight) * 100}%`;
            if (isInnerTabsNode(col.slotId)) {
              return (
                <div
                  key={colIdx}
                  className='composite-grid__cell'
                  style={{ flexBasis, flexGrow: col.weight, flexShrink: 1 }}
                >
                  <InnerTabsCell
                    tabsNode={col.slotId}
                    session={session}
                    slotDefs={tab.slots}
                    sortOrder={sortOrder}
                    onRefresh={onRefresh}
                    onReference={onReference}
                  />
                </div>
              );
            }
            const slotId = col.slotId as string;
            const def = tab.slots.find((s) => s.id === slotId);
            const artifactKey = def?.artifact_key ?? slotId;
            const rev = findSlotRevision(session, artifactKey, sortOrder);
            return (
              <div
                key={slotId}
                className='composite-grid__cell'
                style={{ flexBasis, flexGrow: col.weight, flexShrink: 1 }}
              >
                {def?.label && (
                  <span className='composite-grid__cell-label'>{def.label}</span>
                )}
                {rev ? (
                  <SlotRenderer
                    slot={rev}
                    sessionId={session.session_id}
                    slotId={slotId}
                    revisionCount={rev.revision_count}
                    onRefresh={onRefresh}
                    onReference={onReference}
                  />
                ) : (
                  <div className='composite-grid__cell-empty'>—</div>
                )}
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}

/**
 * TabSlotGrid renders slots according to the plugin UI tab definition.
 * Passes sort_order, sessionId, slotId to each SlotRenderer for Phase 3 actions.
 */
// ---------------------------------------------------------------------------
// SortableImageList — drag-and-drop reordering for image list slots
// Uses HTML5 native drag events; no external library needed.
// Insert indicator is a vertical line between items, not a highlight on the item.
// ---------------------------------------------------------------------------

function SortableImageList({
  revisions,
  session,
  slotDef,
  isDraggable,
  onRefresh,
  onReference,
  onFocusSortOrder,
  onAddItem,
}: {
  revisions: SlotRevision[];
  session: PluginSession;
  slotDef: SlotDef;
  isDraggable: boolean;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
  onAddItem?: () => void;
}) {
  const reorderSlotItems = usePluginStore((s) => s.reorderSlotItems);
  // localOrder stores list_index values in display order.
  const [localOrder, setLocalOrder] = useState<number[]>(() =>
    revisions.map((r) => r.list_index ?? 0),
  );
  useEffect(() => {
    setLocalOrder(revisions.map((r) => r.list_index ?? 0));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revisions.map((r) => `${r.list_index}`).join(',')]);

  const dragSrcIdx = useRef<number | null>(null);
  // insertIdx is a gap index: 0 = before first item, n = after last item.
  const [insertIdx, setInsertIdx] = useState<number | null>(null);

  const handleDragStart = useCallback((idx: number, e: React.DragEvent) => {
    e.stopPropagation();
    // Mark as internal sort drag so outer file-upload listeners can ignore it.
    e.dataTransfer.setData('application/x-plugin-sort', String(idx));
    e.dataTransfer.effectAllowed = 'move';
    dragSrcIdx.current = idx;
  }, []);

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.stopPropagation();
  }, []);

  // Compute which gap the pointer is closest to based on the drag position
  // relative to the hovered item element.
  const computeInsertIdx = useCallback((e: React.DragEvent, itemIdx: number) => {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const midX = rect.left + rect.width / 2;
    return e.clientX < midX ? itemIdx : itemIdx + 1;
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent, itemIdx: number) => {
    e.preventDefault();
    e.stopPropagation();
    setInsertIdx(computeInsertIdx(e, itemIdx));
  }, [computeInsertIdx]);

  const handleContainerDragLeave = useCallback((e: React.DragEvent) => {
    e.stopPropagation();
    // Only clear when leaving the container entirely (not entering a child).
    if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node | null)) {
      setInsertIdx(null);
    }
  }, []);

  const handleDrop = useCallback(async (e: React.DragEvent, itemIdx: number) => {
    e.preventDefault();
    e.stopPropagation();
    const srcIdx = dragSrcIdx.current;
    const gapIdx = computeInsertIdx(e, itemIdx);
    dragSrcIdx.current = null;
    setInsertIdx(null);

    if (srcIdx === null) return;
    // Dropping back into same position is a no-op.
    if (gapIdx === srcIdx || gapIdx === srcIdx + 1) return;

    // next is the new list_index sequence after the move.
    const next = [...localOrder];
    const [moved] = next.splice(srcIdx, 1);
    // After removing srcIdx, adjust gap index if needed.
    const adjustedGap = gapIdx > srcIdx ? gapIdx - 1 : gapIdx;
    next.splice(adjustedGap, 0, moved);
    setLocalOrder(next);
    try {
      // order_version is carried on each revision; use the first available one.
      const orderVersion = revisions[0]?.order_version ?? 0;
      await reorderSlotItems(session.session_id, slotDef.id, next, orderVersion);
      onRefresh?.();
    } catch {
      setLocalOrder(revisions.map((r) => r.list_index ?? 0));
    }
  }, [localOrder, revisions, session.session_id, slotDef.id, reorderSlotItems, onRefresh, computeInsertIdx]);

  const handleDragEnd = useCallback(() => {
    dragSrcIdx.current = null;
    setInsertIdx(null);
  }, []);

  // Fallback handlers on the container so that dragging into the trailing
  // "Add item" card area (which has no per-item handlers) still works.
  const handleContainerDragOver = useCallback((e: React.DragEvent) => {
    // Only handle if we're not already over a child item (those call stopPropagation).
    e.preventDefault();
    // Show the insert indicator at the last position (after all items).
    setInsertIdx(localOrder.length);
  }, [localOrder.length]);

  const handleContainerDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault();
    const srcIdx = dragSrcIdx.current;
    dragSrcIdx.current = null;
    setInsertIdx(null);
    if (srcIdx === null) return;
    // Target gap is after all items.
    const gapIdx = localOrder.length;
    // No-op if already at the end.
    if (gapIdx === srcIdx + 1) return;
    const next = [...localOrder];
    const [moved] = next.splice(srcIdx, 1);
    next.push(moved);
    setLocalOrder(next);
    try {
      const orderVersion = revisions[0]?.order_version ?? 0;
      await reorderSlotItems(session.session_id, slotDef.id, next, orderVersion);
      onRefresh?.();
    } catch {
      setLocalOrder(revisions.map((r) => r.list_index ?? 0));
    }
  }, [localOrder, revisions, session.session_id, slotDef.id, reorderSlotItems, onRefresh]);

  const byListIndex: Record<number, SlotRevision> = {};
  for (const r of revisions) {
    if (r.list_index !== undefined) byListIndex[r.list_index] = r;
  }

  return (
    <div
      className={`plugin-panel__image-list${isDraggable ? ' plugin-panel__image-list--sortable' : ''}`}
      onDragLeave={isDraggable ? handleContainerDragLeave : undefined}
      onDragEnter={isDraggable ? handleDragEnter : undefined}
      onDragOver={isDraggable ? handleContainerDragOver : undefined}
      onDrop={isDraggable ? handleContainerDrop : undefined}
    >
      {/* Insert indicator before first item */}
      {isDraggable && (
        <div className={`plugin-panel__image-insert-gap${insertIdx === 0 ? ' plugin-panel__image-insert-gap--active' : ''}`} aria-hidden='true' />
      )}
      {localOrder.map((listIndex, idx) => {
        const rev = byListIndex[listIndex];
        if (!rev) return null;
        return (
          <React.Fragment key={`${rev.slot_id}-${rev.sort_order ?? rev.list_index ?? 0}`}>
            <div
              draggable={isDraggable}
              onDragStart={isDraggable ? (e) => handleDragStart(idx, e) : undefined}
              onDragEnter={isDraggable ? handleDragEnter : undefined}
              onDragOver={isDraggable ? (e) => handleDragOver(e, idx) : undefined}
              onDrop={isDraggable ? (e) => handleDrop(e, idx) : undefined}
              onDragEnd={isDraggable ? handleDragEnd : undefined}
              onClick={() => onFocusSortOrder?.(rev.sort_order)}
              role='button'
              tabIndex={0}
              aria-label={`图片 ${listIndex}`}
              className={`plugin-panel__image-list-item${dragSrcIdx.current === idx ? ' plugin-panel__image-list-item--dragging' : ''}`}
            >
              <SlotRenderer
                slot={rev}
                cardMode
                expectedType={slotDef.type}
                sessionId={session.session_id}
                slotId={slotDef.id}
                revisionCount={rev.revision_count}
                isDraggable={isDraggable}
                onRefresh={onRefresh}
                onReference={onReference}
              />
            </div>
            {/* Insert indicator after each item */}
            {isDraggable && (
              <div className={`plugin-panel__image-insert-gap${insertIdx === idx + 1 ? ' plugin-panel__image-insert-gap--active' : ''}`} aria-hidden='true' />
            )}
          </React.Fragment>
        );
      })}
      {/* Add new item card */}
      {onAddItem && (
        <button
          className='plugin-panel__image-add-card'
          onClick={onAddItem}
          title='新增附件'
          aria-label='新增附件'
          type='button'
        >
          <span className='plugin-panel__image-add-card-icon'>+</span>
          <span className='plugin-panel__image-add-card-label'>新增附件</span>
        </button>
      )}
    </div>
  );
}

function TabSlotGrid({
  tab,
  session,
  onRefresh,
  onReference,
  onFocusSortOrder,
}: {
  tab: TabDef;
  session: PluginSession;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
}) {
  const addFileInputRef = useRef<HTMLInputElement>(null);
  const addingSlotIdRef = useRef<string>('');
  const addingSlotTypeRef = useRef<string>('');
  const { createSlotItem } = usePluginStore();

  const handleAddItem = useCallback((slotId: string, slotType: string) => {
    addingSlotIdRef.current = slotId;
    addingSlotTypeRef.current = slotType;
    addFileInputRef.current?.click();
  }, []);

  const handleAddFileChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    const slotId = addingSlotIdRef.current;
    if (!slotId) return;
    const slotType = addingSlotTypeRef.current;
    const ct = slotType === 'image' ? 'image' : slotType === 'file' ? 'file' : undefined;
    try {
      const storedPath = await uploadFileInChunks(file);
      await createSlotItem(session.session_id, slotId, { path: storedPath }, file.name, undefined, ct);
      onRefresh?.();
    } catch {
      // upload failure — no-op
    }
  }, [session.session_id, createSlotItem, onRefresh]);
  if (tab.layout === 'composite') {
    return (
      <CompositeSlotGrid
        tab={tab}
        session={session}
        onRefresh={onRefresh}
        onReference={onReference}
        onFocusSortOrder={onFocusSortOrder}
      />
    );
  }
  return (
    <div className={`plugin-panel__tab-content plugin-panel__tab-content--${tab.layout ?? 'list'}`}>
      {/* Hidden file input for adding new items */}
      <input
        ref={addFileInputRef}
        type='file'
        accept='image/*'
        style={{ display: 'none' }}
        onChange={handleAddFileChange}
        aria-hidden='true'
      />
      {tab.slots.map((slotDef) => {
        const artifactKey = slotDef.artifact_key ?? slotDef.id;
        const revisions = (session.slots ?? []).filter(
          (s) => s.artifact_key === artifactKey && s.selected,
        );
        const isImageList = slotDef.type === 'image' && slotDef.cardinality === 'list';
        const isDraggable = Boolean(slotDef.ordered);
        return (
          <div key={slotDef.id} className='plugin-panel__named-slot'>
            {slotDef.label && (
              <span className='plugin-panel__slot-label'>{slotDef.label}</span>
            )}
            {revisions.length === 0 ? (
              <div
                className='plugin-panel__slot-placeholder'
                aria-label={`${slotDef.label} pending`}
              >
                <span>—</span>
              </div>
            ) : isImageList ? (
              <SortableImageList
                revisions={revisions}
                session={session}
                slotDef={slotDef}
                isDraggable={isDraggable}
                onRefresh={onRefresh}
                onReference={onReference}
                onFocusSortOrder={onFocusSortOrder}
                onAddItem={() => handleAddItem(slotDef.id, slotDef.type)}
              />
            ) : (
              revisions.map((rev) => (
                <div
                  key={`${rev.slot_id}-${rev.revision}-${rev.list_index ?? 0}`}
                  onClick={() => onFocusSortOrder?.(rev.sort_order)}
                  role='button'
                  tabIndex={0}
                  aria-label={`内容项 ${rev.sort_order ?? ''}`}
                >
                  <SlotRenderer
                    slot={rev}
                    expectedType={slotDef.type}
                    sessionId={session.session_id}
                    slotId={slotDef.id}
                    revisionCount={rev.revision_count}
                    onRefresh={onRefresh}
                    onReference={onReference}
                  />
                </div>
              ))
            )}
          </div>
        );
      })}
    </div>
  );
}

const STATUS_KEY: Record<string, string> = {
  active: 'chat.pluginStatusRunning',
  completed: 'chat.pluginStatusDone',
  waiting: 'chat.pluginStatusWaiting',
};

export function PluginPanel({
  conversationId,
  pollIntervalMs = 3000,
  onSendMessage,
  onReference,
}: PluginPanelProps) {
  const { t } = useTranslation();
  const { session, loading, refresh } = usePluginSession(conversationId);
  const autoRunning = usePluginStore((s) =>
    conversationId ? (s.autoRunningByConversation[conversationId] ?? false) : false,
  );
  const [activeTabIdx, setActiveTabIdx] = React.useState(0);
  const [collapsed, setCollapsed] = useState(false);
  const fetchPluginUI = usePluginStore((s) => s.fetchPluginUI);
  const pluginUIByPlugin = usePluginStore((s) => s.pluginUIByPlugin);
  const setFocusedTab = usePluginStore((s) => s.setFocusedTab);
  const setFocusedSortOrder = usePluginStore((s) => s.setFocusedSortOrder);
  const [ui, setUI] = useState<PluginUI>({});
  // Track which text slots are currently being edited; disable footer buttons while any are.
  const editingSlots = useRef<Set<string>>(new Set());
  const [anySlotEditing, setAnySlotEditing] = useState(false);

  const handleSlotEditingChange = useCallback((key: string, editing: boolean) => {
    if (editing) {
      editingSlots.current.add(key);
    } else {
      editingSlots.current.delete(key);
    }
    setAnySlotEditing(editingSlots.current.size > 0);
  }, []);

  useEffect(() => {
    if (!session?.plugin_id) return;
    const cached = pluginUIByPlugin[session.plugin_id];
    if (cached) { setUI(cached); return; }
    fetchPluginUI(session.plugin_id).then(setUI);
  }, [session?.plugin_id, fetchPluginUI, pluginUIByPlugin]);

  // Restore the previously focused tab when UI loads or session changes.
  useEffect(() => {
    const tabs: TabDef[] = ui.tabs ?? [];
    if (!tabs.length || !session?.focusedTab) return;
    const idx = tabs.findIndex((t) => t.id === session.focusedTab);
    if (idx !== -1) setActiveTabIdx(idx);
  }, [ui.tabs, session?.focusedTab]);

  useEffect(() => {
    if (!session || session.status !== 'active') return;
    const id = setInterval(refresh, pollIntervalMs);
    return () => clearInterval(id);
  }, [session, refresh, pollIntervalMs]);

  // Track focused tab changes.
  const handleTabChange = useCallback((idx: number, tabId: string) => {
    setActiveTabIdx(idx);
    setFocusedTab(conversationId, tabId);
    setFocusedSortOrder(conversationId, undefined);
  }, [conversationId, setFocusedTab, setFocusedSortOrder]);

  const handleFocusSortOrder = useCallback((sortOrder: number | undefined) => {
    setFocusedSortOrder(conversationId, sortOrder);
  }, [conversationId, setFocusedSortOrder]);

  if (loading && !session) {
    return (
      <div
        className='plugin-panel plugin-panel--loading'
        role='status'
        aria-label='Loading plugin panel'
      />
    );
  }

  if (!session) return null;

  const tabs: TabDef[] = ui.tabs ?? [];
  const hasTabs = tabs.length > 0;

  const showActions =
    session.status === 'waiting' ||
    session.status === 'active' ||
    session.status === 'completed';
  const displayStatus = autoRunning ? 'active' : session.status;
  const buttonsDisabled = displayStatus === 'active' || anySlotEditing || autoRunning;
  // "继续" is only shown in waiting/active; completed shows rollback step picker instead.
  const showContinue = displayStatus === 'waiting' || displayStatus === 'active';

  function handleContinue() {
    if (buttonsDisabled) return;
    onSendMessage?.(t('chat.pluginContinue'));
  }

  function handleRetry() {
    if (buttonsDisabled) return;
    onSendMessage?.(t('chat.pluginRetry'));
  }

  function handleRollback(stepId: string) {
    if (buttonsDisabled) return;
    onSendMessage?.(`${t('chat.pluginRollbackPrefix')}${stepId}`);
  }

  return (
    <SlotEditingContext.Provider value={{ setEditing: handleSlotEditingChange }}>
    <div
      className={`plugin-panel plugin-panel--${displayStatus}${collapsed ? ' plugin-panel--collapsed' : ''}`}
      data-session-id={session.session_id}
      aria-label='Plugin Panel'
    >
      {/* Header */}
      <div className='plugin-panel__header'>
        <div className='plugin-panel__header-left'>
          <span className='plugin-panel__title'>{session.plugin_id}</span>
          <span
            className={`plugin-panel__status plugin-panel__status--${displayStatus}`}
            aria-label={`Status: ${t(STATUS_KEY[displayStatus] ?? displayStatus)}`}
          >
            {t(STATUS_KEY[displayStatus] ?? displayStatus)}
          </span>
        </div>
        <div className='plugin-panel__header-right'>
          <button
            type='button'
            className='plugin-panel__collapse-btn'
            onClick={() => setCollapsed((c) => !c)}
            aria-label={collapsed ? 'Expand panel' : 'Collapse panel'}
            title={collapsed ? 'Expand' : 'Collapse'}
          >
            <svg
              width='12'
              height='12'
              viewBox='0 0 12 12'
              fill='none'
              xmlns='http://www.w3.org/2000/svg'
              className={`plugin-panel__collapse-icon${collapsed ? ' plugin-panel__collapse-icon--up' : ''}`}
            >
              <path d='M2 4L6 8L10 4' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
            </svg>
          </button>
        </div>
      </div>

      {/* Tabs — step navigator style */}
      {!collapsed && hasTabs && (
        <div className='plugin-panel__tabs' role='tablist'>
          {tabs.map((tab, idx) => (
            <React.Fragment key={tab.id}>
              <button
                role='tab'
                aria-selected={idx === activeTabIdx}
                aria-controls={`plugin-tab-panel-${tab.id}`}
                className={`plugin-panel__tab${idx === activeTabIdx ? ' plugin-panel__tab--active' : ''}${idx < activeTabIdx ? ' plugin-panel__tab--done' : ''}`}
                onClick={() => handleTabChange(idx, tab.id)}
                type='button'
              >
                <span className='plugin-panel__tab-badge'>{idx + 1}</span>
                <span className='plugin-panel__tab-label'>{tab.label}</span>
              </button>
              {idx < tabs.length - 1 && (
                <span className={`plugin-panel__tab-connector${idx < activeTabIdx ? ' plugin-panel__tab-connector--done' : ''}`} aria-hidden='true' />
              )}
            </React.Fragment>
          ))}
        </div>
      )}

      {/* Body */}
      {!collapsed && (
        <div className='plugin-panel__body'>
          {hasTabs ? (
            tabs.map((tab, idx) => (
              <div
                key={tab.id}
                id={`plugin-tab-panel-${tab.id}`}
                role='tabpanel'
                hidden={idx !== activeTabIdx}
              >
                <TabSlotGrid
                  tab={tab}
                  session={session}
                  onRefresh={refresh}
                  onReference={onReference}
                  onFocusSortOrder={handleFocusSortOrder}
                />
              </div>
            ))
          ) : (
            <AutoSlotGrid
              session={session}
              onRefresh={refresh}
              onReference={onReference}
            />
          )}
        </div>
      )}

      {/* Footer */}
      {!collapsed && showActions && (
        <div className='plugin-panel__footer' role='group' aria-label='Session controls'>
          <button
            type='button'
            className='plugin-panel__action-btn plugin-panel__action-btn--secondary'
            disabled={buttonsDisabled}
            aria-disabled={buttonsDisabled}
            onClick={handleRetry}
            title={buttonsDisabled ? t('chat.pluginBtnDisabledHint') : t('chat.pluginRetry')}
          >
            {t('chat.pluginRetry')}
          </button>
          {showContinue && (
            <button
              type='button'
              className='plugin-panel__action-btn plugin-panel__action-btn--primary'
              disabled={buttonsDisabled}
              aria-disabled={buttonsDisabled}
              onClick={handleContinue}
              title={buttonsDisabled ? t('chat.pluginBtnDisabledHint') : t('chat.pluginContinue')}
            >
              {t('chat.pluginContinue')}
            </button>
          )}
          {session.status === 'completed' && session.steps && session.steps.length > 0 && (
            <div className='plugin-panel__rollback'>
              <span className='plugin-panel__rollback-label'>{t('chat.pluginRollbackLabel')}</span>
              <div className='plugin-panel__rollback-steps'>
                {session.steps.map((step) => (
                  <button
                    key={`${step.step_id}-${step.attempt}`}
                    type='button'
                    className='plugin-panel__rollback-step-btn'
                    onClick={() => handleRollback(step.step_id)}
                    title={`${t('chat.pluginRollbackPrefix')}${step.step_id}`}
                  >
                    {step.step_id}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
    </SlotEditingContext.Provider>
  );
}
