import React, { useEffect, useState, useCallback, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { message, Popconfirm } from 'antd';
import { usePluginSession } from '@/modules/chat/hooks/usePlugin';
import { usePluginStore } from '@/modules/chat/store/pluginPanel';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { PluginSessionApi } from '@/modules/chat/utils/request';
import StateGraphModal from '@/components/StateGraphModal';
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

/** Parse a JSON intent_context string and return the text field, or '' if empty/invalid. */
function parseIntentText(raw?: string): string {
  if (!raw || raw === '{}') return '';
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    return (parsed as Record<string, unknown>).text as string ?? '';
  } catch {
    return '';
  }
}

/** Fallback: read latest selected text from a slot artifact. */
function parseSelectedSlotText(session: PluginSession, slotKey: string, includeUnselected = false): string {
  const candidates = (session.slots ?? [])
    .filter((s) => s.slot === slotKey && (includeUnselected || s.selected))
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
  const latest = candidates[0];
  if (!latest) return '';
  const raw = latest.artifact_value;
  if (raw === null || raw === undefined) return '';
  if (typeof raw === 'string') return raw;
  if (typeof raw === 'object') {
    const obj = raw as Record<string, unknown>;
    if (obj.text !== undefined) return String(obj.text);
    if (obj.value !== undefined) return String(obj.value);
  }
  return String(raw);
}

/** Latest _source_tool among selected image slots (newest first). */
function getLatestSelectedImageSourceTool(session: PluginSession): string {
  const selectedImageSlots = (session.slots ?? []).filter(
    (s) => s.selected && s.content_type === 'image',
  );
  if (!selectedImageSlots.length) {
    return '';
  }
  const latest = [...selectedImageSlots].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  )[0];
  return String(latest?.artifact_value?._source_tool ?? '').trim();
}

/** IntentPopover shows global intent + per-step intent inside a floating popover. */
function IntentPopover({
  session,
  tabs,
  onClose,
}: {
  session: PluginSession;
  tabs: TabDef[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const wrapRef = useRef<HTMLDivElement>(null);
  const globalText =
    parseIntentText(session.intent_context)
    || parseSelectedSlotText(session, 'user_intent_summary', true);
  const stepIntents = (session.steps ?? [])
    .filter((s) => !!parseIntentText(s.intent_context))
    .map((s, idx) => ({
      idx: idx + 1,
      stepId: s.step_id,
      text: parseIntentText(s.intent_context),
      tabLabel: tabs.find((t) => getTabStepId(t) === s.step_id)?.label ?? s.step_id,
    }));

  useEffect(() => {
    function handleMouseDown(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        onClose();
      }
    }
    document.addEventListener('mousedown', handleMouseDown);
    return () => document.removeEventListener('mousedown', handleMouseDown);
  }, [onClose]);

  return (
    <div className='plugin-panel__intent-popover' ref={wrapRef} role='dialog' aria-label={t('chat.pluginIntentBtn')}>
      <div className='plugin-panel__intent-popover-title'>{t('chat.pluginIntentBtn')}</div>
      {globalText && (
        <div className='plugin-panel__intent-section'>
          <div className='plugin-panel__intent-section-title'>{t('chat.pluginIntentGlobalTitle')}</div>
          <div className='plugin-panel__intent-section-text'>{globalText}</div>
        </div>
      )}
      {stepIntents.length > 0 && (
        <div className='plugin-panel__intent-section'>
          <div className='plugin-panel__intent-section-title'>{t('chat.pluginIntentStepTitle')}</div>
          <div className='plugin-panel__intent-step-list'>
            {stepIntents.map((si) => (
              <div key={si.stepId} className='plugin-panel__intent-step-row'>
                <span className='plugin-panel__intent-step-badge'>{si.idx}</span>
                <span className='plugin-panel__intent-step-text'>{si.text}</span>
                <span className='plugin-panel__intent-step-arrow'>→</span>
                <span className='plugin-panel__intent-step-tab'>{si.tabLabel}</span>
              </div>
            ))}
          </div>
          <div className='plugin-panel__intent-step-note'>{t('chat.pluginIntentStepMapNote')}</div>
        </div>
      )}
      {!globalText && stepIntents.length === 0 && (
        <div className='plugin-panel__intent-empty'>{t('chat.pluginIntentEmpty')}</div>
      )}
    </div>
  );
}

interface PluginPanelProps {
  conversationId: string;
  pollIntervalMs?: number;
  /** Called when the user clicks Continue or Retry — simulates sending a user message. */
  onSendMessage?: (text: string) => void;
  /** Called when the user clicks the reference button on a slot item. */
  onReference?: (slot: SlotRevision) => void;
  /** Called when the user clicks the Stop button during an active session. */
  onStop?: () => void;
  /** Called after a session is successfully dismissed. */
  onDismissed?: () => void;
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
  const { t } = useTranslation();
  if (!session.slots || session.slots.length === 0) {
    return (
      <div className='plugin-panel__empty' role='status' aria-live='polite'>
        <span>{t('chat.pluginWaitingForResults')}</span>
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

/**
 * Flatten a format-C CompositePanelNode tree into a flat column list.
 * For 'row' nodes, children become columns proportioned by weight.
 * For 'column' nodes at root, we treat the whole tree as one column (single slot fallback).
 * tabs[] leaf nodes become an InnerTabsNode for backward compat rendering.
 */
function flattenFormatCNode(
  node: import('@/modules/chat/store/pluginPanel').CompositePanelNode,
  weight: number,
): Array<{ slotId: string | InnerTabsNode; weight: number }> {
  if (node.slot) {
    return [{ slotId: node.slot, weight }];
  }
  if (node.tabs && node.tabs.length > 0) {
    // Convert format-C tabs (string[]) to legacy InnerTabsNode for rendering
    const innerTabsNode: InnerTabsNode = {
      tabs: node.tabs.map((slotId) => slotId as CompositeLayoutNode),
    };
    return [{ slotId: innerTabsNode, weight }];
  }
  if (node.direction === 'row' && node.children) {
    const childWeight = node.children.reduce((s, c) => s + (c.weight ?? 1), 0);
    return node.children.flatMap((child) =>
      flattenFormatCNode(child, ((child.weight ?? 1) / childWeight) * weight),
    );
  }
  // column direction or unknown: render as a single nested block — just flatten children
  if (node.direction === 'column' && node.children) {
    // For now, render only the first child in column containers (rows handle horizontal splitting)
    // A full nested column render would require CSS grid nesting, handled in the tree renderer.
    return node.children.flatMap((child) => flattenFormatCNode(child, child.weight ?? 1));
  }
  return [];
}

/** Build the effective column list from composite_layout (or fall back to slot ids). */
function buildColumns(
  tab: TabDef,
): Array<{ slotId: string | InnerTabsNode; weight: number }> {
  const layout = tab.composite_layout;
  if (!layout) {
    return tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }

  // Format C: { direction, children } tree
  if (!Array.isArray(layout) && typeof layout === 'object' && 'direction' in layout) {
    const result = flattenFormatCNode(
      layout as import('@/modules/chat/store/pluginPanel').CompositePanelNode,
      1,
    );
    return result.length > 0 ? result : tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }

  // Legacy array format
  if (!Array.isArray(layout) || layout.length === 0) {
    return tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }
  const first = layout[0];
  const cols =
    Array.isArray(first)
      ? (first as CompositeLayoutNode[])
      : layout as CompositeLayoutNode[];
  return cols
    .map((n) => resolveColumnSlotId(n))
    .filter((c): c is NonNullable<typeof c> => c !== null);
}

function getTabStepId(tab: TabDef): string | undefined {
  return tab.step_id ?? tab.id;
}

function getTabSlotRevisions(
  session: PluginSession,
  tab: TabDef,
  artifactKey: string,
): SlotRevision[] {
  const slots = session.slots ?? [];
  if (tab.step_id) {
    return slots.filter((s) => s.slot === artifactKey && s.step_id === tab.step_id);
  }
  const isStepTab = session.steps?.some((s) => s.step_id === tab.id);
  if (isStepTab) {
    return slots.filter((s) => s.slot === artifactKey && s.step_id === tab.id);
  }
  return slots.filter((s) => s.slot === artifactKey && s.selected);
}

/** Get all distinct sort_orders present across the participating slots. */
function getCompositeRows(
  tab: TabDef,
  session: PluginSession,
): number[] {
  const participating = new Set(tab.slots.map((s) => s.id));
  const orders = new Set<number>();
  const scopeStepId = tab.step_id
    ?? (session.steps?.some((s) => s.step_id === tab.id) ? tab.id : undefined);
  for (const slot of session.slots ?? []) {
    const matchesTabStep = scopeStepId ? slot.step_id === scopeStepId : slot.selected;
    if (matchesTabStep && participating.has(slot.slot)) {
      if (slot.sort_order !== undefined) {
        orders.add(slot.sort_order);
      }
    }
  }
  return Array.from(orders).sort((a, b) => a - b);
}

/** Find a slot revision for (slot, sort_order). */
function findSlotRevision(
  session: PluginSession,
  tab: TabDef,
  artifactKey: string,
  sortOrder: number,
): SlotRevision | undefined {
  return getTabSlotRevisions(session, tab, artifactKey).find(
    (s) => s.slot === artifactKey && s.sort_order === sortOrder,
  );
}

// ---------------------------------------------------------------------------
// InnerTabsCell: renders an {tabs: [...]} node for a single row
// ---------------------------------------------------------------------------

function InnerTabsCell({
  tabsNode,
  tab,
  session,
  slotDefs,
  sortOrder,
  onRefresh,
  onReference,
}: {
  tabsNode: InnerTabsNode;
  tab: TabDef;
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
        const artifactKey = def?.id ?? slotId;
        const rev = findSlotRevision(session, tab, artifactKey, sortOrder);
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
  const { t } = useTranslation();
  const rows = getCompositeRows(tab, session);
  const columns = buildColumns(tab);

  // Compute total weight for flex proportions.
  const totalWeight = columns.reduce((s, c) => s + c.weight, 0);

  if (rows.length === 0) {
    return (
      <div className='plugin-panel__empty' role='status' aria-live='polite'>
        <span>{t('chat.pluginWaitingForResults')}</span>
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
          aria-label={t('chat.pluginRowAria', { index: sortOrder })}
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
                    tab={tab}
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
            const artifactKey = def?.id ?? slotId;
            const rev = findSlotRevision(session, tab, artifactKey, sortOrder);
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
  const { t } = useTranslation();
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
              aria-label={t('chat.pluginImageAria', { index: listIndex })}
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
          title={t('chat.pluginAddAttachment')}
          aria-label={t('chat.pluginAddAttachment')}
          type='button'
        >
          <span className='plugin-panel__image-add-card-icon'>+</span>
          <span className='plugin-panel__image-add-card-label'>{t('chat.pluginAddAttachment')}</span>
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
  const { t } = useTranslation();
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
  const resolveVisibleSlots = (slotDefs: SlotDef[]): SlotDef[] => {
    if (session.plugin_id !== 'image-plugin' || tab.id !== 'result') {
      return slotDefs;
    }
    const selectedImageSlots = (session.slots ?? []).filter(
      (s) => s.selected && s.content_type === 'image',
    );
    if (!selectedImageSlots.length) {
      return slotDefs;
    }
    const latest = [...selectedImageSlots].sort(
      (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    )[0];
    const sourceTool = String(latest?.artifact_value?._source_tool ?? '').trim();
    if (sourceTool === 'image_generator') {
      return slotDefs
        .filter((s) => s.id === 'image_output')
        .map((s) => ({
          ...s,
          // In pure generation flow, this slot is the final generated output, not an editor input.
          label: 'Generated Image',
        }));
    }
    if (sourceTool === 'image_editor') {
      const allowed = new Set(['image_output', 'enhanced_image_output']);
      return slotDefs.filter((s) => allowed.has(s.id));
    }
    return slotDefs;
  };
  const visibleSlots = resolveVisibleSlots(tab.slots);
  const resolveSlotLabel = (slotDef: SlotDef): string => {
    const key = slotDef.id;
    if (
      session.plugin_id === 'image-plugin'
      && tab.id === 'result'
      && key === 'image_output'
      && getLatestSelectedImageSourceTool(session) === 'image_generator'
    ) {
      return t('chat.pluginGeneratedImage');
    }
    return slotDef.label ?? slotDef.id;
  };
  return (
    <div className={`plugin-panel__tab-content plugin-panel__tab-content--${tab.layout ?? 'vertical'}`}>
      {/* Hidden file input for adding new items */}
      <input
        ref={addFileInputRef}
        type='file'
        accept='image/*'
        style={{ display: 'none' }}
        onChange={handleAddFileChange}
        aria-hidden='true'
      />
      {visibleSlots.map((slotDef) => {
        const artifactKey = slotDef.id;
        const revisions = getTabSlotRevisions(session, tab, artifactKey);
        if (session.plugin_id === 'image-plugin' && tab.id === 'result' && revisions.length === 0) {
          return null;
        }
        const isImageList = slotDef.type === 'image' && slotDef.cardinality === 'list';
        const isDraggable = Boolean(slotDef.ordered);
        return (
          <div key={slotDef.id} className='plugin-panel__named-slot'>
            {(slotDef.label || slotDef.id) && (
              <span className='plugin-panel__slot-label'>{resolveSlotLabel(slotDef)}</span>
            )}
            {revisions.length === 0 ? (
              <div
                className='plugin-panel__slot-placeholder'
                aria-label={`${resolveSlotLabel(slotDef)} pending`}
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
                  aria-label={t('chat.pluginContentItemAria', { index: rev.sort_order ?? '' })}
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
  onStop,
  onDismissed,
}: PluginPanelProps) {
  const { t, i18n } = useTranslation();
  const { session, loading, refresh } = usePluginSession(conversationId);
  const bumpDismissedRefresh = usePluginStore((s) => s.bumpDismissedRefresh);
  const autoRunning = usePluginStore((s) =>
    conversationId ? (s.autoRunningByConversation[conversationId] ?? false) : false,
  );
  const [activeTabIdx, setActiveTabIdx] = React.useState(0);
  const [collapsed, setCollapsed] = useState(false);
  const fetchPluginUI = usePluginStore((s) => s.fetchPluginUI);
  const setFocusedTab = usePluginStore((s) => s.setFocusedTab);
  const setFocusedSortOrder = usePluginStore((s) => s.setFocusedSortOrder);
  // Focused tab id mirrored out of the session so polling refreshes don't
  // reset the user's current tab.
  const focusedTabByConversation = usePluginStore((s) => s.focusedTabByConversation);
  const persistedFocusedTab = conversationId ? focusedTabByConversation[conversationId] : undefined;
  const [ui, setUI] = useState<PluginUI>({});
  const [dismissing, setDismissing] = useState(false);
  const [stateGraphOpen, setStateGraphOpen] = useState(false);

  const handleDismiss = useCallback(async () => {
    if (!session || dismissing) return;
    setDismissing(true);
    try {
      await PluginSessionApi().dismissSession(session.session_id);
      bumpDismissedRefresh(conversationId);
      onDismissed?.();
      refresh();
    } catch {
      message.error(t('chat.pluginDismissFailed'));
      setDismissing(false);
    }
  }, [session, dismissing, refresh, t, onDismissed, bumpDismissedRefresh, conversationId]);
  // Track which text slots are currently being edited; disable footer buttons while any are.
  const editingSlots = useRef<Set<string>>(new Set());
  const [anySlotEditing, setAnySlotEditing] = useState(false);
  const [intentOpen, setIntentOpen] = useState(false);

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
    const lang = i18n.language || '';
    const cached = usePluginStore.getState().pluginUIByPlugin[`${session.plugin_id}:${lang}`];
    if (cached) {
      setUI(cached);
    }
    // Always re-fetch once to avoid stale cached tab/slot layouts after plugin.yaml updates.
    fetchPluginUI(session.plugin_id).then(setUI);
  }, [session?.plugin_id, fetchPluginUI, i18n.language]);

  // Restore the previously focused tab when UI loads.
  useEffect(() => {
    const tabs: TabDef[] = ui.tabs ?? [];
    if (!tabs.length || !persistedFocusedTab) return;
    const idx = tabs.findIndex((t) => t.id === persistedFocusedTab);
    if (idx !== -1) setActiveTabIdx(idx);
  }, [ui.tabs, persistedFocusedTab]);

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
        aria-label={t('chat.pluginPanelLoading')}
      />
    );
  }

  if (!session) return null;

  const tabs: TabDef[] = ui.tabs ?? [];
  const hasTabs = tabs.length > 0;
  const hasIntent = true;

  const showActions =
    session.status === 'waiting' ||
    session.status === 'active' ||
    session.status === 'completed';
  const displayStatus = autoRunning ? 'active' : session.status;
  const buttonsDisabled = displayStatus === 'active' || anySlotEditing || autoRunning;
  // "继续" is only shown in waiting/active; completed shows rollback step picker instead.
  const showContinue = displayStatus === 'waiting' || displayStatus === 'active';

  // A failed step cannot be checkpoint-resumed — the SubAgent exited uncleanly and there is
  // no valid checkpoint to restore. Only "重试" (full restart) is meaningful in this case.
  // Note: "interrupted" steps CAN be resumed via checkpoint, so only "failed" is blocked.
  const currentStepStatus = session.steps
    ?.filter((s) => s.step_id === session.current_step_id)
    ?.sort((a, b) => b.attempt - a.attempt)[0]?.status;
  const continueDisabled = buttonsDisabled || currentStepStatus === 'failed';

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
      aria-label={t('chat.pluginPanelTitle')}
    >
      {/* Header */}
      <div className='plugin-panel__header'>
        <div className='plugin-panel__header-left'>
          <span className='plugin-panel__title'>{session.plugin_id}</span>
          <span
            className={`plugin-panel__status plugin-panel__status--${displayStatus}`}
            aria-label={t('chat.pluginStatusAria', { status: t(STATUS_KEY[displayStatus] ?? displayStatus) })}
            onClick={() => session && setStateGraphOpen(true)}
            style={{ cursor: 'pointer' }}
            title={t('chat.pluginViewWorkflow')}
            role='button'
            tabIndex={0}
            onKeyDown={(e) => e.key === 'Enter' && session && setStateGraphOpen(true)}
          >
            {t(STATUS_KEY[displayStatus] ?? displayStatus)}
          </span>
        </div>
        <div className='plugin-panel__header-right'>
          {hasIntent && (
            <div className='plugin-panel__intent-btn-wrap'>
              <button
                type='button'
                className='plugin-panel__intent-btn'
                onClick={() => setIntentOpen((v) => !v)}
                aria-label={t('chat.pluginIntentBtn')}
                aria-expanded={intentOpen}
              >
                <svg width='13' height='13' viewBox='0 0 13 13' fill='none' xmlns='http://www.w3.org/2000/svg' aria-hidden='true'>
                  <circle cx='6.5' cy='6.5' r='5.75' stroke='currentColor' strokeWidth='1.5' />
                  <path d='M6.5 5.5v4' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
                  <circle cx='6.5' cy='3.75' r='0.75' fill='currentColor' />
                </svg>
                {t('chat.pluginIntentBtn')}
              </button>
              {intentOpen && (
                <IntentPopover
                  session={session}
                  tabs={tabs}
                  onClose={() => setIntentOpen(false)}
                />
              )}
            </div>
          )}
          <Popconfirm
            title={t('chat.pluginDismissConfirmTitle')}
            description={t('chat.pluginDismissConfirmDesc')}
            onConfirm={handleDismiss}
            okText={t('chat.pluginDismissConfirmOk')}
            cancelText={t('chat.pluginDismissConfirmCancel')}
            okButtonProps={{ danger: true, size: 'small' }}
            cancelButtonProps={{ size: 'small' }}
            disabled={dismissing}
            placement='bottomRight'
          >
            <button
              type='button'
              className='plugin-panel__dismiss-btn'
              disabled={dismissing}
              aria-label={t('chat.pluginDismissBtn')}
              title={t('chat.pluginDismissBtn')}
            >
              <svg width='12' height='12' viewBox='0 0 12 12' fill='none' xmlns='http://www.w3.org/2000/svg' aria-hidden='true'>
                <path d='M2 2L10 10M10 2L2 10' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
              </svg>
            </button>
          </Popconfirm>
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
          {tabs.map((tab, idx) => {
            const stepID = getTabStepId(tab);
            const step = session.steps?.find((s) => s.step_id === stepID);
            const stepStatus = step?.status;
            return (
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
                  {stepStatus && stepStatus !== 'succeeded' && (
                    <span
                      className={`plugin-panel__step-status plugin-panel__step-status--${stepStatus}`}
                      aria-label={`Step status: ${stepStatus}`}
                      title={stepStatus}
                    />
                  )}
                </button>
                {idx < tabs.length - 1 && (
                  <span className={`plugin-panel__tab-connector${idx < activeTabIdx ? ' plugin-panel__tab-connector--done' : ''}`} aria-hidden='true' />
                )}
              </React.Fragment>
            );
          })}
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
        <div className='plugin-panel__footer' role='group' aria-label={t('chat.pluginSessionControls')}>
          {displayStatus === 'active' && onStop && (
            <button
              type='button'
              className='plugin-panel__action-btn plugin-panel__action-btn--danger'
              onClick={onStop}
              title={t('chat.pluginStop')}
            >
              {t('chat.pluginStop')}
            </button>
          )}
          {session.status !== 'completed' && (
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
          )}
          {showContinue && (
            <button
              type='button'
              className='plugin-panel__action-btn plugin-panel__action-btn--primary'
              disabled={continueDisabled}
              aria-disabled={continueDisabled}
              onClick={handleContinue}
              title={
                currentStepStatus === 'failed'
                  ? t('chat.pluginContinueDisabledFailed')
                  : buttonsDisabled
                    ? t('chat.pluginBtnDisabledHint')
                    : t('chat.pluginContinue')
              }
            >
              {t('chat.pluginContinue')}
            </button>
          )}
          {session.status === 'completed' && session.steps && session.steps.length > 0 && (
            <div style={{ flex: '1 1 100%', display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 12, color: '#6b7280', fontWeight: 500 }}>{t('chat.pluginRollbackLabel')}</span>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                {session.steps.map((step) => (
                  <button
                    key={`${step.step_id}-${step.attempt}`}
                    type='button'
                    className='plugin-panel__action-btn plugin-panel__action-btn--secondary'
                    style={{ padding: '3px 10px', fontSize: 12 }}
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
    {session && (
      <StateGraphModal
        open={stateGraphOpen}
        onClose={() => setStateGraphOpen(false)}
        sessionId={session.session_id}
        pluginId={session.plugin_id}
        liveRefresh
        conversationId={conversationId}
      />
    )}
    </SlotEditingContext.Provider>
  );
}
