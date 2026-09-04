import React, { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Popconfirm, Tooltip } from 'antd';
import {
  CloudUploadOutlined,
  DownloadOutlined,
  ExportOutlined,
  FullscreenOutlined,
  FullscreenExitOutlined,
} from '@ant-design/icons';
import { useWorkflowSession } from '@/modules/chat/hooks/useWorkflow';
import {
  filterWorkflowTabs,
  workflowTabAllowsDownload,
  useWorkflowStore,
} from '@/modules/chat/store/workflowPanel';
import { isWorkflowReadyToStart } from '@/modules/chat/store/workflowStatus';
import { useTaskCenterStore, type SubAgentTask, type TaskArtifactStream } from '@/modules/chat/store/taskCenter';
import { uploadFileInChunks } from '@/modules/chat/utils/chunkUpload';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import StateGraphModal from '@/components/StateGraphModal';
import {
  WORKFLOW_PANEL_EXPANDED_EVENT,
  WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX,
} from '@/modules/chat/constants/chat';
import type {
  WorkflowSession,
  SlotRevision,
  TabDef,
  WorkflowUI,
  SlotDef,
  CompositePanelNode,
  CompositeLayoutNode,
  CompositeColumnNode,
  InnerTabsNode,
} from '@/modules/chat/store/workflowPanel';
import {
  isWriterIrSource,
  SlotRenderer,
  SlotDownloadContext,
  SlotMarkdownStream,
} from './SlotComponents';
import { SlideThumb } from './ppt/SlideThumb';
import { WorkflowTabActions } from './actions/WorkflowTabActions';
import { WorkflowPanelTabActiveContext, SlotEditingContext, type SlotFooterAction } from './slotEditingContext';
import { findWriterArtifactStream } from './writerArtifactStream';
import { resolveCompletedContinueStep } from './workflowContinue';
import { moveSelectedCompositePages, sameCompositePageOrder } from './compositePageReorder';
import './WorkflowPanel.scss';

const DOCUMENT_FOOTER_LINK_ORDER = 20;
const EMPTY_TASK_CENTER_TASKS: SubAgentTask[] = [];

type DocumentFooterItem =
  | { kind: 'button'; key: string; order: number; action: SlotFooterAction }
  | { kind: 'link'; key: string; order: number; href: string; label: string };

function buildDocumentFooterItems(footerActions: Map<string, SlotFooterAction>): {
  statusMessages: Array<{ key: string; text: string; tone?: SlotFooterAction['statusTone'] }>;
  actionItems: DocumentFooterItem[];
} {
  const statusMessages: Array<{ key: string; text: string; tone?: SlotFooterAction['statusTone'] }> = [];
  const actionItems: DocumentFooterItem[] = [];

  for (const [key, action] of footerActions.entries()) {
    if (action.statusText) {
      statusMessages.push({ key: `${key}:status`, text: action.statusText, tone: action.statusTone });
    }
    if (action.statusLink) {
      actionItems.push({
        kind: 'link',
        key: `${key}:link`,
        order: DOCUMENT_FOOTER_LINK_ORDER,
        href: action.statusLink.href,
        label: action.statusLink.label,
      });
    }
    actionItems.push({
      kind: 'button',
      key,
      order: action.order ?? 100,
      action,
    });
  }

  actionItems.sort((left, right) => left.order - right.order);
  return { statusMessages, actionItems };
}

/** Parse a JSON intent context string and return the text field, or '' if empty/invalid. */
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
function parseSelectedSlotText(session: WorkflowSession, slotKey: string, includeUnselected = false): string {
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

/** IntentPopover shows global intent + per-step intent inside a floating popover. */
function IntentPopover({
  session,
  tabs,
  onClose,
}: {
  session: WorkflowSession;
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
    <div className='workflow-panel__intent-popover' ref={wrapRef} role='dialog' aria-label={t('chat.workflowIntentBtn')}>
      <div className='workflow-panel__intent-popover-title'>{t('chat.workflowIntentBtn')}</div>
      {globalText && (
        <div className='workflow-panel__intent-section'>
          <div className='workflow-panel__intent-section-title'>{t('chat.workflowIntentGlobalTitle')}</div>
          <div className='workflow-panel__intent-section-text'>{globalText}</div>
        </div>
      )}
      {stepIntents.length > 0 && (
        <div className='workflow-panel__intent-section'>
          <div className='workflow-panel__intent-section-title'>{t('chat.workflowIntentStepTitle')}</div>
          <div className='workflow-panel__intent-step-list'>
            {stepIntents.map((si) => (
              <div key={si.stepId} className='workflow-panel__intent-step-row'>
                <span className='workflow-panel__intent-step-badge'>{si.idx}</span>
                <span className='workflow-panel__intent-step-text'>{si.text}</span>
                <span className='workflow-panel__intent-step-arrow'>→</span>
                <span className='workflow-panel__intent-step-tab'>{si.tabLabel}</span>
              </div>
            ))}
          </div>
          <div className='workflow-panel__intent-step-note'>{t('chat.workflowIntentStepMapNote')}</div>
        </div>
      )}
      {!globalText && stepIntents.length === 0 && (
        <div className='workflow-panel__intent-empty'>{t('chat.workflowIntentEmpty')}</div>
      )}
    </div>
  );
}

interface WorkflowPanelProps {
  conversationId: string;
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
  readOnly,
}: {
  session: WorkflowSession;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  if (!session.slots || session.slots.length === 0) {
    return (
      <div className='workflow-panel__empty' role='status' aria-live='polite'>
        <span>{t('chat.workflowWaitingForResults')}</span>
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
    <div className='workflow-panel__auto-grid'>
      {Object.entries(bySlot).map(([slotId, revisions]) => (
        <div key={slotId} className='workflow-panel__slot-group'>
          <span className='workflow-panel__slot-label'>{slotId}</span>
          <div className='workflow-panel__slot-items'>
            {revisions.map((rev) => (
              <SlotRenderer
                key={`${rev.slot_id}-${rev.list_index ?? -1}`}
                slot={rev}
                sessionId={session.session_id}
                slotId={slotId}
                revisionCount={rev.revision_count}
                onRefresh={onRefresh}
                onReference={onReference}
                readOnly={readOnly}
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
  node: import('@/modules/chat/store/workflowPanel').CompositePanelNode,
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
      layout as import('@/modules/chat/store/workflowPanel').CompositePanelNode,
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

/**
 * Lock slot editing only while the plugin session is actively running.
 * When idle (waiting / failed / completed), editable artifact formats stay editable
 * according to their workflow readOnly setting, so the user can revise and re-run
 * a later step from the updated content.
 */
function isWorkflowSessionReadOnly(
  session: WorkflowSession,
  autoRunning = false,
): boolean {
  return autoRunning || session.status === 'active';
}

function revisionMatchesTabScope(
  session: WorkflowSession,
  tab: TabDef,
  slot: SlotRevision,
  scope: 'selected' | 'tab',
): boolean {
  if (scope === 'selected') {
    return Boolean(slot.selected);
  }
  if (tab.step_id) {
    return slot.step_id === tab.step_id;
  }
  const isStepTab = session.steps?.some((s) => s.step_id === tab.id);
  if (isStepTab) {
    return slot.step_id === tab.id;
  }
  return Boolean(slot.selected);
}

/** Slot ids that currently have at least one revision under the tab's empty-column scope. */
function getPresentSlotIds(
  tab: TabDef,
  session: WorkflowSession,
  scope: 'selected' | 'tab' = 'selected',
): Set<string> {
  const participating = new Set(tab.slots.map((s) => s.id));
  const present = new Set<string>();
  for (const slot of session.slots ?? []) {
    if (!participating.has(slot.slot)) continue;
    if (!revisionMatchesTabScope(session, tab, slot, scope)) continue;
    present.add(slot.slot);
  }
  return present;
}

/**
 * Resolve which slot ids should be visible for a tab from `composite_behavior`.
 * Returns null when no behavior is declared (show all configured columns/slots).
 */
function resolveVisibleSlotIds(
  tab: TabDef,
  session: WorkflowSession,
): Set<string> | null {
  const behavior = tab.composite_behavior;
  if (!behavior) return null;

  const scope = behavior.empty_column_scope === 'tab' ? 'tab' : 'selected';
  const present = getPresentSlotIds(tab, session, scope);
  const allowed = new Set(tab.slots.map((s) => s.id));

  for (const group of behavior.mutually_exclusive ?? []) {
    const members = (group.slots ?? []).filter((id) => allowed.has(id));
    if (members.length < 2) continue;
    const prefer = (group.prefer?.length ? group.prefer : members)
      .filter((id) => members.includes(id));
    const winner = prefer.find((id) => present.has(id))
      ?? members.find((id) => present.has(id));
    if (!winner) continue;
    for (const id of members) {
      if (id !== winner) allowed.delete(id);
    }
  }

  if (behavior.hide_empty_columns) {
    for (const id of [...allowed]) {
      if (!present.has(id)) allowed.delete(id);
    }
  }

  return allowed;
}

function filterColumnsByVisibleSlots(
  columns: Array<{ slotId: string | InnerTabsNode; weight: number }>,
  visible: Set<string> | null,
): Array<{ slotId: string | InnerTabsNode; weight: number }> {
  if (!visible) return columns;
  const filtered = columns.filter((col) => {
    if (typeof col.slotId !== 'string') return true;
    return visible.has(col.slotId);
  });
  return filtered.length > 0 ? filtered : columns;
}

function getTabSlotRevisions(
  session: WorkflowSession,
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

function isStructuredArtifactRevision(slot: SlotRevision): boolean {
  if (slot.content_type === 'json') return true;
  const raw = slot.artifact_value;
  if (!raw || typeof raw !== 'object') return false;
  if (raw.type === 'json') return true;
  const source = String(raw.filename ?? raw.name ?? raw.path ?? raw.url ?? '');
  const normalized = source.split(/[?#]/, 1)[0].toLowerCase();
  return isWriterIrSource(normalized) || normalized.endsWith('.json');
}

/** Prefer a structured sibling artifact over its Markdown export when both exist. */
function resolvePreferredStructuredSlotDefs(tab: TabDef, session: WorkflowSession): SlotDef[] {
  const declaredSlotIds = new Set(tab.slots.map((slot) => slot.id));

  return tab.slots.flatMap((slotDef) => {
    if (!slotDef.id.endsWith('_md')) return [slotDef];
    const irSlotId = slotDef.id.slice(0, -3);
    const hasIRArtifact = getTabSlotRevisions(session, tab, irSlotId)
      .some(isStructuredArtifactRevision);
    if (!hasIRArtifact) return [slotDef];
    if (declaredSlotIds.has(irSlotId)) return [];

    return [{
      ...slotDef,
      id: irSlotId,
      label: slotDef.label.replace(/\s*[（(]\s*markdown\s*[）)]/i, '').trim() || irSlotId,
      type: 'text',
    }];
  });
}

/** Pick the first HTML material in this composite row for its filmstrip thumbnail. */
function findCompositeHtmlRevision(
  session: WorkflowSession,
  tab: TabDef,
  sortOrder: number,
): SlotRevision | undefined {
  for (const slot of tab.slots) {
    if (slot.widget?.widgetType !== 'html-slide') continue;
    const revision = findSlotRevision(session, tab, slot.id, sortOrder);
    if (revision) return revision;
  }
  return undefined;
}

/** Get all distinct sort_orders present across the participating slots. */
function getCompositeRows(
  tab: TabDef,
  session: WorkflowSession,
): number[] {
  const participating = new Set(tab.slots.map((s) => s.id));
  const orders = new Set<number>();
  const scopeStepId = tab.step_id
    ?? (session.steps?.some((s) => s.step_id === tab.id) ? tab.id : undefined);
  for (const slot of session.slots ?? []) {
    const matchesTabStep = scopeStepId ? slot.step_id === scopeStepId : slot.selected;
    if (matchesTabStep && participating.has(slot.slot) && slot.sort_order !== undefined) {
      orders.add(slot.sort_order);
    }
  }
  return Array.from(orders).sort((a, b) => a - b);
}

/** Find a slot revision for (slot, sort_order). */
function findSlotRevision(
  session: WorkflowSession,
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
  hideImageMutationActions,
  readOnly,
}: {
  tabsNode: InnerTabsNode;
  tab: TabDef;
  session: WorkflowSession;
  slotDefs: SlotDef[];
  sortOrder: number;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  hideImageMutationActions?: boolean;
  readOnly?: boolean;
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
                widget={def?.widget}
                expectedType={def?.type}
                sessionId={session.session_id}
                slotId={slotId}
                revisionCount={rev.revision_count}
                onRefresh={onRefresh}
                onReference={onReference}
                hideImageMutationActions={hideImageMutationActions}
                readOnly={readOnly}
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

type PageBarPosition = 'top' | 'bottom' | 'left' | 'right';

function CompositeThumbnailRail({
  position,
  pages,
  currentPage,
  onChange,
  onReorder,
  reordering = false,
  tab,
  session,
}: {
  position: PageBarPosition;
  pages: number[];
  currentPage: number;
  onChange: (page: number) => void;
  onReorder?: (nextPages: number[]) => void | Promise<void>;
  reordering?: boolean;
  tab: TabDef;
  session: WorkflowSession;
}) {
  const { t } = useTranslation();
  const isCol = position === 'left' || position === 'right';
  const idx = Math.max(0, pages.indexOf(currentPage));
  const dragPagesRef = useRef<Set<number>>(new Set());
  const [insertIdx, setInsertIdx] = useState<number | null>(null);
  const [selectedPages, setSelectedPages] = useState<Set<number>>(new Set());
  const [draggingPages, setDraggingPages] = useState<Set<number>>(new Set());

  useEffect(() => {
    const available = new Set(pages);
    setSelectedPages((selected) => {
      const next = new Set(Array.from(selected).filter((page) => available.has(page)));
      if (next.size === selected.size) return selected;
      return next;
    });
  }, [pages.join(',')]);

  const computeInsertIdx = useCallback((event: React.DragEvent, itemIdx: number) => {
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    if (isCol) return event.clientY < rect.top + rect.height / 2 ? itemIdx : itemIdx + 1;
    return event.clientX < rect.left + rect.width / 2 ? itemIdx : itemIdx + 1;
  }, [isCol]);

  const handlePageClick = (sortOrder: number, event: React.MouseEvent) => {
    if (event.ctrlKey || event.metaKey) {
      setSelectedPages((selected) => {
        const next = new Set(selected);
        if (next.has(sortOrder)) next.delete(sortOrder);
        else next.add(sortOrder);
        return next;
      });
    } else {
      setSelectedPages(new Set([sortOrder]));
    }
    onChange(sortOrder);
  };

  const handleDragStart = (sortOrder: number, event: React.DragEvent) => {
    if (!onReorder || reordering) return;
    const moving = selectedPages.has(sortOrder)
      ? new Set(selectedPages)
      : new Set([sortOrder]);
    dragPagesRef.current = moving;
    setSelectedPages(moving);
    setDraggingPages(moving);
    event.dataTransfer.setData(
      'application/x-workflow-page-sort',
      JSON.stringify(Array.from(moving)),
    );
    event.dataTransfer.effectAllowed = 'move';
  };

  const handleDragOver = (pageIdx: number, event: React.DragEvent) => {
    if (!onReorder || dragPagesRef.current.size === 0) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
    setInsertIdx(computeInsertIdx(event, pageIdx));
  };

  const resetDrag = () => {
    dragPagesRef.current = new Set();
    setInsertIdx(null);
    setDraggingPages(new Set());
  };

  const handleDrop = async (pageIdx: number, event: React.DragEvent) => {
    event.preventDefault();
    const moving = dragPagesRef.current;
    const gapIdx = computeInsertIdx(event, pageIdx);
    resetDrag();
    if (!moving.size || !onReorder) return;
    const next = moveSelectedCompositePages(pages, moving, gapIdx);
    if (sameCompositePageOrder(next, pages)) return;
    await onReorder(next);
    setSelectedPages(new Set());
  };

  const canDrag = Boolean(onReorder) && !reordering;
  const hasAnyHtmlPreview = pages.some((sortOrder) =>
    Boolean(findCompositeHtmlRevision(session, tab, sortOrder)),
  );

  return (
    <div
      className={[
        'composite-thumb-rail',
        `composite-thumb-rail--${isCol ? 'col' : 'row'}`,
        `composite-thumb-rail--${position}`,
        !hasAnyHtmlPreview ? 'composite-thumb-rail--compact' : '',
      ].filter(Boolean).join(' ')}
      onDragLeave={canDrag ? (event) => {
        if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)) {
          setInsertIdx(null);
        }
      } : undefined}
    >
      <div className='composite-thumb-rail__nav'>
        <button
          type='button'
          className='composite-thumb-rail__arrow'
          disabled={idx <= 0 || reordering}
          onClick={() => idx > 0 && onChange(pages[idx - 1])}
          aria-label={t('chat.workflowPreviousPage')}
        >
          {isCol ? '↑' : '←'}
        </button>
        <div
          className={`composite-thumb-rail__list composite-thumb-rail__list--${isCol ? 'col' : 'row'}`}
          role='list'
        >
          {canDrag && <div className={`composite-thumb-rail__insert${insertIdx === 0 ? ' composite-thumb-rail__insert--active' : ''}`} aria-hidden='true' />}
          {pages.map((sortOrder, pageIdx) => {
            const revision = findCompositeHtmlRevision(session, tab, sortOrder);
            const hasHtmlPreview = Boolean(revision);
            const selected = selectedPages.has(sortOrder);
            const dragging = draggingPages.has(sortOrder);
            return (
              <React.Fragment key={`${sortOrder}-${pageIdx}`}>
                <button
                  type='button'
                  role='listitem'
                  draggable={canDrag}
                  className={[
                    'composite-thumb-rail__item',
                    sortOrder === currentPage ? 'composite-thumb-rail__item--active' : '',
                    selected ? 'composite-thumb-rail__item--selected' : '',
                    canDrag ? 'composite-thumb-rail__item--draggable' : '',
                    dragging ? 'composite-thumb-rail__item--dragging' : '',
                    !hasHtmlPreview ? 'composite-thumb-rail__item--fallback' : '',
                  ].filter(Boolean).join(' ')}
                  onClick={(event) => handlePageClick(sortOrder, event)}
                  onDragStart={(event) => handleDragStart(sortOrder, event)}
                  onDragOver={(event) => handleDragOver(pageIdx, event)}
                  onDrop={(event) => { void handleDrop(pageIdx, event); }}
                  onDragEnd={resetDrag}
                  title={canDrag ? t('chat.workflowPageMultiSelectDragHint') : undefined}
                  aria-label={t('chat.workflowRowAria', { index: pageIdx + 1 })}
                  aria-current={sortOrder === currentPage ? 'true' : undefined}
                  aria-pressed={selected}
                >
                  {hasHtmlPreview && <span className='composite-thumb-rail__badge'>{pageIdx + 1}</span>}
                  {selectedPages.size > 1 && selected && (
                    <span className='composite-thumb-rail__selection-badge' aria-hidden='true'>
                      {selectedPages.size}
                    </span>
                  )}
                  <span
                    className={`composite-thumb-rail__preview${hasHtmlPreview ? '' : ' composite-thumb-rail__preview--fallback'}`}
                    aria-hidden='true'
                  >
                    {revision ? (
                      <SlideThumb slot={revision} sessionId={session.session_id} />
                    ) : (
                      <span className='composite-thumb-rail__page-number'>{pageIdx + 1}</span>
                    )}
                  </span>
                </button>
                {canDrag && <div className={`composite-thumb-rail__insert${insertIdx === pageIdx + 1 ? ' composite-thumb-rail__insert--active' : ''}`} aria-hidden='true' />}
              </React.Fragment>
            );
          })}
        </div>
        <button
          type='button'
          className='composite-thumb-rail__arrow'
          disabled={idx >= pages.length - 1 || reordering}
          onClick={() => idx < pages.length - 1 && onChange(pages[idx + 1])}
          aria-label={t('chat.workflowNextPage')}
        >
          {isCol ? '↓' : '→'}
        </button>
      </div>
    </div>
  );
}

function CompositeSlotGrid({
  tab,
  session,
  onRefresh,
  onReference,
  onFocusSortOrder,
  readOnly,
}: {
  tab: TabDef;
  session: WorkflowSession;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const reorderSlotItems = useWorkflowStore((state) => state.reorderSlotItems);
  const rows = getCompositeRows(tab, session);
  const columns = filterColumnsByVisibleSlots(
    buildColumns(tab),
    resolveVisibleSlotIds(tab, session),
  );
  const hideImageMutationActions = tab.id === 'result';

  // Compute total weight for flex proportions.
  const totalWeight = columns.reduce((s, c) => s + c.weight, 0) || 1;
  const pageBarPosition = tab.composite_tab_position as PageBarPosition | undefined;
  const paged = Boolean(pageBarPosition);
  const stackCompositeCells = Boolean(
    tab.composite_layout
      && !Array.isArray(tab.composite_layout)
      && tab.composite_layout.direction === 'column',
  );
  const [currentPage, setCurrentPage] = useState<number | null>(null);
  const [reorderError, setReorderError] = useState<string | null>(null);
  const [reordering, setReordering] = useState(false);

  useEffect(() => {
    if (!paged) return;
    if (!rows.length) {
      setCurrentPage(null);
      return;
    }
    setCurrentPage((page) => page != null && rows.includes(page) ? page : rows[0]);
  }, [paged, rows.join(',')]);

  useEffect(() => {
    if (paged) onFocusSortOrder?.(currentPage ?? undefined);
  }, [paged, currentPage, onFocusSortOrder]);

  const activePage = currentPage ?? rows[0];
  const reorderableCompositeSlotIds = tab.slots
    .filter((slot) => slot.cardinality === 'list' && slot.ordered)
    .map((slot) => slot.id)
    .filter((slotId) => getTabSlotRevisions(session, tab, slotId)
      .filter((revision) => revision.selected && revision.list_index !== undefined)
      .length > 1);
  const canReorderPages = paged
    && rows.length > 1
    && reorderableCompositeSlotIds.length > 0
    && !readOnly;

  const handlePageReorder = useCallback(async (nextPages: number[]) => {
    if (!canReorderPages || reordering || nextPages.length !== rows.length) return;
    if (nextPages.every((page, index) => page === rows[index])) return;

    const focusedVisualIdx = Math.max(0, nextPages.indexOf(activePage));
    // Composite pagination aligns every participating ordered-list slot by its
    // visual sort_order. Reorder all such slots together so page identity stays
    // aligned for text, images, HTML previews, notes, and future widget types.
    setReordering(true);
    setReorderError(null);
    try {
      for (const slotId of reorderableCompositeSlotIds) {
        const revisions = getTabSlotRevisions(session, tab, slotId)
          .filter((revision) => revision.selected
            && revision.sort_order !== undefined
            && revision.list_index !== undefined)
          .sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0));
        if (revisions.length < 2) continue;
        const bySortOrder = new Map(revisions.map((revision) => [revision.sort_order as number, revision]));
        const nextListOrder = nextPages
          .map((sortOrder) => bySortOrder.get(sortOrder)?.list_index)
          .filter((listIndex): listIndex is number => listIndex !== undefined);
        if (nextListOrder.length !== revisions.length) {
          throw new Error(t('chat.workflowPageReorderFailed'));
        }
        await reorderSlotItems(
          session.session_id,
          slotId,
          nextListOrder,
          revisions[0]?.order_version ?? 0,
        );
      }
      await onRefresh?.();
      const nextFocusedPage = focusedVisualIdx + 1;
      setCurrentPage(nextFocusedPage);
      onFocusSortOrder?.(nextFocusedPage);
    } catch (error) {
      setReorderError(error instanceof Error ? error.message : t('chat.workflowPageReorderFailed'));
    } finally {
      setReordering(false);
    }
  }, [activePage, canReorderPages, onFocusSortOrder, onRefresh, reorderSlotItems, reordering, reorderableCompositeSlotIds, rows, session, tab, t]);

  if (rows.length === 0) {
    return (
      <div className='workflow-panel__empty' role='status' aria-live='polite'>
        <span>{t('chat.workflowWaitingForResults')}</span>
      </div>
    );
  }

  const renderSlotCell = (
    slotId: string,
    sortOrder: number,
    key: React.Key,
    style?: React.CSSProperties,
  ) => {
    const def = tab.slots.find((slot) => slot.id === slotId);
    const rev = findSlotRevision(session, tab, def?.id ?? slotId, sortOrder);
    return (
      <div key={key} className='composite-grid__cell' style={style}>
        {def?.label && <span className='composite-grid__cell-label'>{def.label}</span>}
        {rev ? (
          <SlotRenderer
            slot={rev}
            widget={def?.widget}
            expectedType={def?.type}
            sessionId={session.session_id}
            slotId={slotId}
            revisionCount={rev.revision_count}
            onRefresh={onRefresh}
            onReference={onReference}
            hideImageMutationActions={hideImageMutationActions}
            readOnly={readOnly}
          />
        ) : (
          <div className='composite-grid__cell-empty'>—</div>
        )}
      </div>
    );
  };

  const hasNestedContainer = (node: CompositePanelNode): boolean =>
    Boolean(node.children?.some((child) => child.direction || hasNestedContainer(child)));

  const formatCLayout = tab.composite_layout && !Array.isArray(tab.composite_layout)
    ? tab.composite_layout
    : undefined;
  const renderNestedComposite = (
    node: CompositePanelNode,
    sortOrder: number,
    path: string,
    root = false,
  ): React.ReactNode => {
    if (node.slot) return renderSlotCell(node.slot, sortOrder, path);
    if (node.tabs?.length) {
      const tabSlotIds = (node.tabs as unknown[])
        .map((item) => typeof item === 'string'
          ? item
          : String((item as { slot?: string })?.slot ?? ''))
        .filter(Boolean);
      return (
        <div key={path} className='composite-grid__cell'>
          <InnerTabsCell
            tabsNode={{ tabs: tabSlotIds }}
            tab={tab}
            session={session}
            slotDefs={tab.slots}
            sortOrder={sortOrder}
            onRefresh={onRefresh}
            onReference={onReference}
            hideImageMutationActions={hideImageMutationActions}
            readOnly={readOnly}
          />
        </div>
      );
    }
    if (!node.direction || !node.children?.length) {
      return <div key={path} className='composite-grid__cell-empty'>—</div>;
    }
    const children = node.children;
    return (
      <div
        key={path}
        className={`composite-grid__tree composite-grid__tree--${node.direction}${root ? ' composite-grid__tree--root' : ''}`}
      >
        {children.map((child, index) => (
          <div
            key={`${path}-${index}`}
            className='composite-grid__tree-child'
            style={{ flex: `${child.weight ?? 1} 1 0` }}
          >
            {renderNestedComposite(child, sortOrder, `${path}-${index}`)}
          </div>
        ))}
      </div>
    );
  };

  const renderRow = (sortOrder: number) => (
        <div
          key={sortOrder}
          className={`composite-grid__row${formatCLayout && hasNestedContainer(formatCLayout) ? ' composite-grid__row--tree' : stackCompositeCells ? ' composite-grid__row--stack' : ''}`}
          onClick={() => onFocusSortOrder?.(sortOrder)}
          role='button'
          tabIndex={0}
          aria-label={t('chat.workflowRowAria', { index: sortOrder })}
        >
          {formatCLayout && hasNestedContainer(formatCLayout)
            ? renderNestedComposite(formatCLayout, sortOrder, `page-${sortOrder}`, true)
            : columns.map((col, colIdx) => {
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
                    hideImageMutationActions={hideImageMutationActions}
                    readOnly={readOnly}
                  />
                </div>
              );
            }
            const slotId = col.slotId as string;
            return renderSlotCell(slotId, sortOrder, slotId, {
              flexBasis,
              flexGrow: col.weight,
              flexShrink: 1,
            });
          })}
        </div>
  );

  if (!paged) {
    return <div className='composite-grid'>{rows.map(renderRow)}</div>;
  }

  const rail = (
    <CompositeThumbnailRail
      position={pageBarPosition!}
      pages={rows}
      currentPage={activePage}
      onChange={setCurrentPage}
      onReorder={canReorderPages ? handlePageReorder : undefined}
      reordering={reordering}
      tab={tab}
      session={session}
    />
  );

  return (
    <div className='composite-shell composite-shell--paged'>
      <div className={`composite-with-pagebar composite-with-pagebar--${pageBarPosition}`}>
        {(pageBarPosition === 'top' || pageBarPosition === 'left') && rail}
        <div className='composite-main'>
          <WorkflowTabActions actions={tab.actions} tab={tab} session={session} rows={rows} />
          {reorderError && <span className='composite-toolbar__error'>{reorderError}</span>}
          <div className='composite-grid composite-grid--paged'>{renderRow(activePage)}</div>
        </div>
        {(pageBarPosition === 'bottom' || pageBarPosition === 'right') && rail}
      </div>
    </div>
  );
}

/**
 * TabSlotGrid renders slots according to the workflow UI tab definition.
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
  readOnly,
}: {
  revisions: SlotRevision[];
  session: WorkflowSession;
  slotDef: SlotDef;
  isDraggable: boolean;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
  onAddItem?: () => void;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const reorderSlotItems = useWorkflowStore((s) => s.reorderSlotItems);
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
    e.dataTransfer.setData('application/x-workflow-sort', String(idx));
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

  const canDrag = isDraggable && !readOnly;
  const useGridLayout = slotDef.widget?.itemLayout === 'grid' && !canDrag;
  const itemWidth = slotDef.widget?.itemWidth;
  const itemHeight = slotDef.widget?.itemHeight;
  const gridMaxCols = slotDef.widget?.gridMaxCols;
  const imageListStyle = {
    ...(typeof itemWidth === 'number'
      ? { '--workflow-image-item-width': `${itemWidth}px` }
      : {}),
    ...(typeof itemHeight === 'number'
      ? { '--workflow-image-item-height': `${itemHeight}px` }
      : {}),
    ...(typeof itemWidth === 'number' && typeof gridMaxCols === 'number'
      ? {
        '--workflow-image-list-max-width': `${
          itemWidth * gridMaxCols + Math.max(0, gridMaxCols - 1) * 12
        }px`,
      }
      : {}),
  } as React.CSSProperties;

  return (
    <div
      className={`workflow-panel__image-list${canDrag ? ' workflow-panel__image-list--sortable' : ''}${useGridLayout ? ' workflow-panel__image-list--grid' : ''}`}
      style={imageListStyle}
      onDragLeave={canDrag ? handleContainerDragLeave : undefined}
      onDragEnter={canDrag ? handleDragEnter : undefined}
      onDragOver={canDrag ? handleContainerDragOver : undefined}
      onDrop={canDrag ? handleContainerDrop : undefined}
    >
      {/* Insert indicator before first item */}
      {canDrag && (
        <div className={`workflow-panel__image-insert-gap${insertIdx === 0 ? ' workflow-panel__image-insert-gap--active' : ''}`} aria-hidden='true' />
      )}
      {localOrder.map((listIndex, idx) => {
        const rev = byListIndex[listIndex];
        if (!rev) return null;
        return (
          <React.Fragment key={`${rev.slot_id}-${rev.sort_order ?? rev.list_index ?? 0}`}>
            <div
              draggable={canDrag}
              onDragStart={canDrag ? (e) => handleDragStart(idx, e) : undefined}
              onDragEnter={canDrag ? handleDragEnter : undefined}
              onDragOver={canDrag ? (e) => handleDragOver(e, idx) : undefined}
              onDrop={canDrag ? (e) => handleDrop(e, idx) : undefined}
              onDragEnd={canDrag ? handleDragEnd : undefined}
              onClick={() => onFocusSortOrder?.(rev.sort_order)}
              role='button'
              tabIndex={0}
              aria-label={t('chat.workflowImageAria', { index: listIndex })}
              className={`workflow-panel__image-list-item${dragSrcIdx.current === idx ? ' workflow-panel__image-list-item--dragging' : ''}`}
            >
              <SlotRenderer
                slot={rev}
                cardMode
                widget={slotDef.widget}
                expectedType={slotDef.type}
                sessionId={session.session_id}
                slotId={slotDef.id}
                revisionCount={rev.revision_count}
                isDraggable={canDrag}
                onRefresh={onRefresh}
                onReference={onReference}
                readOnly={readOnly}
              />
            </div>
            {/* Insert indicator after each item */}
            {canDrag && (
              <div className={`workflow-panel__image-insert-gap${insertIdx === idx + 1 ? ' workflow-panel__image-insert-gap--active' : ''}`} aria-hidden='true' />
            )}
          </React.Fragment>
        );
      })}
      {/* Add new item card */}
      {onAddItem && !readOnly && (
        <button
          className='workflow-panel__image-add-card'
          onClick={onAddItem}
          title={t('chat.workflowAddAttachment')}
          aria-label={t('chat.workflowAddAttachment')}
          type='button'
        >
          <span className='workflow-panel__image-add-card-icon'>+</span>
          <span className='workflow-panel__image-add-card-label'>{t('chat.workflowAddAttachment')}</span>
        </button>
      )}
    </div>
  );
}

function NamedTabSlot({
  slotDef,
  revisions,
  artifactStream,
  session,
  onRefresh,
  onReference,
  onFocusSortOrder,
  onAddItem,
  readOnly,
}: {
  slotDef: SlotDef;
  revisions: SlotRevision[];
  session: WorkflowSession;
  artifactStream?: TaskArtifactStream;
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
  onAddItem: () => void;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const slotLabel = slotDef.label ?? slotDef.id;
  const isImageList = slotDef.type === 'image' && slotDef.cardinality === 'list';
  const isDraggable = Boolean(slotDef.ordered) && !readOnly;
  const [contentCollapsed, setContentCollapsed] = useState(slotDef.widget?.collapsed === true);
  const prefersFullGridRow = slotDef.widget?.itemLayout === 'grid'
    || (slotDef.widget?.itemWidth ?? 0) >= 600
    || slotDef.widget?.collapsed === true;
  const isWriterDocument = slotDef.widget?.widgetType === 'writer-document';
  const showStream = Boolean(artifactStream && (
    revisions.length === 0 || artifactStream.state === 'streaming'
  ));

  const content = showStream && artifactStream ? (
    <SlotMarkdownStream stream={artifactStream} />
  ) : revisions.length === 0 ? (
    <div
      className='workflow-panel__slot-placeholder'
      aria-label={`${slotLabel} pending`}
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
      onAddItem={readOnly ? undefined : onAddItem}
      readOnly={readOnly}
    />
  ) : (
    revisions.map((rev) => (
      <div
        key={`${rev.slot_id}-${rev.list_index ?? -1}`}
        onClick={() => onFocusSortOrder?.(rev.sort_order)}
        role='button'
        tabIndex={0}
        aria-label={t('chat.workflowContentItemAria', { index: rev.sort_order ?? '' })}
      >
        <SlotRenderer
          slot={rev}
          widget={slotDef.widget}
          originalFileSlot={
            slotDef.id === 'delivered_markdown'
              ? session.slots?.find((item) => item.slot === 'final_document' && item.selected)
              : undefined
          }
          expectedType={slotDef.type}
          sessionId={session.session_id}
          slotId={slotDef.id}
          revisionCount={rev.revision_count}
          onRefresh={onRefresh}
          onReference={onReference}
          readOnly={readOnly}
        />
      </div>
    ))
  );

  return (
    <div className={`workflow-panel__named-slot${prefersFullGridRow ? ' workflow-panel__named-slot--full-grid-row' : ''}${isWriterDocument ? ' workflow-panel__named-slot--writer-document' : ''}`}>
      <div className='workflow-panel__slot-heading'>
        {(slotDef.label || slotDef.id) && (
          <span className='workflow-panel__slot-label'>{slotLabel}</span>
        )}
        {slotDef.widget?.collapsed !== undefined && (
          <button
            type='button'
            className={`workflow-panel__slot-collapse${contentCollapsed ? ' workflow-panel__slot-collapse--collapsed' : ''}`}
            aria-expanded={!contentCollapsed}
            aria-label={contentCollapsed ? t('chat.workflowPanelExpand') : t('chat.workflowPanelCollapse')}
            onClick={() => setContentCollapsed((value) => !value)}
          >
            <span aria-hidden='true'>⌃</span>
          </button>
        )}
      </div>
      {!contentCollapsed && content}
    </div>
  );
}

function TabSlotGrid({
  tab,
  session,
  tasks,
  onRefresh,
  onReference,
  onFocusSortOrder,
  readOnly,
}: {
  tab: TabDef;
  session: WorkflowSession;
  tasks: SubAgentTask[];
  onRefresh?: () => void;
  onReference?: (slot: SlotRevision) => void;
  onFocusSortOrder?: (sortOrder: number | undefined) => void;
  readOnly?: boolean;
}) {
  const addFileInputRef = useRef<HTMLInputElement>(null);
  const addingSlotIdRef = useRef<string>('');
  const addingSlotTypeRef = useRef<string>('');
  const { createSlotItem } = useWorkflowStore();

  const handleAddItem = useCallback((slotId: string, slotType: string) => {
    if (readOnly) return;
    addingSlotIdRef.current = slotId;
    addingSlotTypeRef.current = slotType;
    addFileInputRef.current?.click();
  }, [readOnly]);

  const handleAddFileChange = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file || readOnly) return;
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
  }, [session.session_id, createSlotItem, onRefresh, readOnly]);
  if (tab.layout === 'composite') {
    return (
      <CompositeSlotGrid
        tab={tab}
        session={session}
        onRefresh={onRefresh}
        onReference={onReference}
        onFocusSortOrder={onFocusSortOrder}
        readOnly={readOnly}
      />
    );
  }
  const resolveVisibleSlots = (slotDefs: SlotDef[]): SlotDef[] => {
    const visible = resolveVisibleSlotIds(tab, session);
    if (!visible) return slotDefs;
    const filtered = slotDefs.filter((s) => visible.has(s.id));
    return filtered.length > 0 ? filtered : slotDefs;
  };
  const visibleSlots = resolveVisibleSlots(resolvePreferredStructuredSlotDefs(tab, session));
  return (
    <div className={`workflow-panel__tab-content workflow-panel__tab-content--${tab.layout ?? 'vertical'}`}>
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
        const artifactStream = findWriterArtifactStream(
          session,
          getTabStepId(tab),
          slotDef.id,
          tasks,
        );
        const hideEmpty = Boolean(tab.composite_behavior?.hide_empty_columns);
        if (hideEmpty && revisions.length === 0 && !artifactStream) {
          return null;
        }
        return (
          <NamedTabSlot
            key={slotDef.id}
            slotDef={slotDef}
            revisions={revisions}
            artifactStream={artifactStream}
            session={session}
            onRefresh={onRefresh}
            onReference={onReference}
            onFocusSortOrder={onFocusSortOrder}
            onAddItem={() => handleAddItem(slotDef.id, slotDef.type)}
            readOnly={readOnly}
          />
        );
      })}
    </div>
  );
}

const STATUS_KEY: Record<string, string> = {
  active: 'chat.workflowStatusRunning',
  completed: 'chat.workflowStatusDone',
  waiting: 'chat.workflowStatusWaiting',
  failed: 'chat.workflowStatusFailed',
  stopped: 'chat.workflowStatusStopped',
};

function readPersistedExpanded(conversationId: string): boolean {
  try {
    return localStorage.getItem(`${WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX}${conversationId}`) === 'true';
  } catch {
    return false;
  }
}

function persistExpanded(conversationId: string, expanded: boolean) {
  try {
    localStorage.setItem(
      `${WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX}${conversationId}`,
      String(expanded),
    );
  } catch {
    // The live layout state still works when browser storage is unavailable.
  }
}

export function WorkflowPanel({
  conversationId,
  onSendMessage,
  onReference,
  onStop,
  onDismissed,
}: WorkflowPanelProps) {
  const { t, i18n } = useTranslation();
  const { session, loading, refresh } = useWorkflowSession(conversationId);
  const taskCenterTasks = useTaskCenterStore((state) =>
    conversationId
      ? state.tasksByConversation[conversationId] ?? EMPTY_TASK_CENTER_TASKS
      : EMPTY_TASK_CENTER_TASKS,
  );
  const bumpDismissedRefresh = useWorkflowStore((s) => s.bumpDismissedRefresh);
  const autoRunning = useWorkflowStore((s) =>
    conversationId ? (s.autoRunningByConversation[conversationId] ?? false) : false,
  );
  const setAutoRunning = useWorkflowStore((s) => s.setAutoRunning);
  const [activeTabIdx, setActiveTabIdx] = React.useState(0);
  const [collapsed, setCollapsed] = useState(false);
  const fetchWorkflowUI = useWorkflowStore((s) => s.fetchWorkflowUI);
  const setFocusedTab = useWorkflowStore((s) => s.setFocusedTab);
  const setFocusedSortOrder = useWorkflowStore((s) => s.setFocusedSortOrder);
  // Focused tab id mirrored out of the session so polling refreshes don't
  // reset the user's current tab.
  const focusedTabByConversation = useWorkflowStore((s) => s.focusedTabByConversation);
  const persistedFocusedTab = conversationId ? focusedTabByConversation[conversationId] : undefined;
  const [ui, setUI] = useState<WorkflowUI>({});
  const [dismissing, setDismissing] = useState(false);
  const [stateGraphOpen, setStateGraphOpen] = useState(false);
  const [expanded, setExpanded] = useState(() => readPersistedExpanded(conversationId));
  const initialExpandedRef = useRef(expanded);
  // Track which slots are currently being edited; dismiss stays blocked until
  // each editor saves or cancels. Footer retry/continue flushes pending saves.
  const editingSlots = useRef<Set<string>>(new Set());
  const flushFns = useRef<Map<string, () => Promise<boolean>>>(new Map());
  const [anySlotEditing, setAnySlotEditing] = useState(false);
  const [actionPending, setActionPending] = useState(false);
  const [footerActions, setFooterActions] = useState<Map<string, SlotFooterAction>>(new Map());

  const setExpandedMode = useCallback((nextExpanded: boolean) => {
    if (nextExpanded) setCollapsed(false);
    setExpanded(nextExpanded);
    persistExpanded(conversationId, nextExpanded);
    window.dispatchEvent(new CustomEvent(WORKFLOW_PANEL_EXPANDED_EVENT, {
      detail: { conversationId, expanded: nextExpanded },
    }));
  }, [conversationId]);

  const handleStop = useCallback(() => {
    setAutoRunning(conversationId, false);
    onStop?.();
    window.setTimeout(() => {
      void refresh();
    }, 250);
  }, [conversationId, onStop, refresh, setAutoRunning]);

  useEffect(() => {
    window.dispatchEvent(new CustomEvent(WORKFLOW_PANEL_EXPANDED_EVENT, {
      detail: { conversationId, expanded: initialExpandedRef.current },
    }));
    return () => {
      window.dispatchEvent(new CustomEvent(WORKFLOW_PANEL_EXPANDED_EVENT, {
        detail: { conversationId, expanded: false },
      }));
    };
  }, [conversationId]);

  const handleDismiss = useCallback(async () => {
    if (!session || dismissing || anySlotEditing) return;
    setDismissing(true);
    try {
      await WorkflowSessionApi().dismissSession(session.session_id);
      bumpDismissedRefresh(conversationId);
      onDismissed?.();
      refresh();
    } catch {
      setDismissing(false);
    }
  }, [session, dismissing, anySlotEditing, refresh, t, onDismissed, bumpDismissedRefresh, conversationId]);
  const [intentOpen, setIntentOpen] = useState(false);

  const handleSlotEditingChange = useCallback((key: string, editing: boolean) => {
    if (editing) {
      editingSlots.current.add(key);
    } else {
      editingSlots.current.delete(key);
    }
    setAnySlotEditing(editingSlots.current.size > 0);
  }, []);

  const registerFlush = useCallback((key: string, flush: () => Promise<boolean>) => {
    flushFns.current.set(key, flush);
    return () => {
      flushFns.current.delete(key);
    };
  }, []);

  const registerFooterAction = useCallback((key: string, action: SlotFooterAction | null) => {
    setFooterActions((previous) => {
      const next = new Map(previous);
      if (action) next.set(key, action);
      else next.delete(key);
      return next;
    });
    return () => {
      setFooterActions((previous) => {
        if (!previous.has(key)) return previous;
        const next = new Map(previous);
        next.delete(key);
        return next;
      });
    };
  }, []);

  const flushPendingEdits = useCallback(async (flushKey?: string): Promise<boolean> => {
    const selectedFlusher = flushKey ? flushFns.current.get(flushKey) : undefined;
    const flushers = flushKey
      ? (selectedFlusher ? [selectedFlusher] : [])
      : [...flushFns.current.values()];
    if (flushers.length === 0) return true;
    const results = await Promise.all(flushers.map((flush) => flush()));
    return results.every(Boolean);
  }, []);

  useEffect(() => {
    editingSlots.current.clear();
    flushFns.current.clear();
    setFooterActions(new Map());
    setAnySlotEditing(false);
    setActionPending(false);
  }, [session?.session_id]);

  useEffect(() => {
    if (!session?.workflow_id) return;
    const lang = i18n.language || '';
    const cached = useWorkflowStore.getState().workflowUIByWorkflow[`${session.workflow_id}:${lang}`];
    if (cached) {
      setUI(cached);
    }
    // Always re-fetch once to avoid stale cached tab/slot layouts after workflow.yaml updates.
    fetchWorkflowUI(session.workflow_id).then(setUI);
  }, [session?.workflow_id, fetchWorkflowUI, i18n.language]);

  // Restore the previously focused tab when UI loads.
  useEffect(() => {
    const tabs = filterWorkflowTabs(
      ui.tabs ?? [],
      session?.slots ?? [],
      ui.tab_visibility_ready_material,
    );
    if (!tabs.length || !persistedFocusedTab) return;
    const idx = tabs.findIndex((t) => t.id === persistedFocusedTab);
    if (idx !== -1) setActiveTabIdx(idx);
  }, [ui.tabs, ui.tab_visibility_ready_material, persistedFocusedTab, session?.slots]);

  // Until the user explicitly chooses a visible tab, follow the runtime
  // frontier. This also moves focus immediately when a skip material removes
  // the previously selected tab.
  useEffect(() => {
    const tabs = filterWorkflowTabs(
      ui.tabs ?? [],
      session?.slots ?? [],
      ui.tab_visibility_ready_material,
    );
    const focusedTabVisible = Boolean(
      persistedFocusedTab && tabs.some((tab) => tab.id === persistedFocusedTab),
    );
    if (!tabs.length || focusedTabVisible || !session) return;
    let idx = tabs.findIndex((tab) => getTabStepId(tab) === session.current_step_id);
    if (idx === -1 && (session.status === 'completed' || session.status === 'failed')) {
      idx = tabs.length - 1;
    }
    if (idx !== -1) setActiveTabIdx(idx);
  }, [
    ui.tabs,
    ui.tab_visibility_ready_material,
    persistedFocusedTab,
    session?.current_step_id,
    session?.session_id,
    session?.slots,
    session?.status,
  ]);

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
        className='workflow-panel workflow-panel--loading'
        role='status'
        aria-label={t('chat.workflowPanelLoading')}
      />
    );
  }

  if (!session) return null;

  const tabs = filterWorkflowTabs(
    ui.tabs ?? [],
    session.slots ?? [],
    ui.tab_visibility_ready_material,
  );
  const hasTabs = tabs.length > 0;
  const visibleActiveTabIdx = hasTabs
    ? Math.min(activeTabIdx, tabs.length - 1)
    : 0;
  const hasIntent = true;
  const showActions =
    session.status === 'waiting' ||
    session.status === 'active' ||
    session.status === 'completed' ||
    session.status === 'failed';
  const documentFooter = useMemo(
    () => buildDocumentFooterItems(footerActions),
    [footerActions],
  );
  const displayStatus = autoRunning ? 'active' : session.status;
  const displayStatusKey = isWorkflowReadyToStart(
    displayStatus,
    session.projection,
    session.steps?.length ?? 0,
  )
    ? 'chat.workflowStatusReady'
    : STATUS_KEY[displayStatus] ?? displayStatus;
  // Only block footer actions while the plugin is actually running (or flush-in-progress).
  // Dirty editors no longer disable retry — click flushes saves first, then proceeds.
  const sessionBusy = displayStatus === 'active' || autoRunning;
  const buttonsDisabled = sessionBusy || actionPending;
  const dismissDisabled = dismissing || anySlotEditing || actionPending;
  const collapseDisabled = (anySlotEditing || actionPending) && !collapsed;
  const completedContinueStepId = resolveCompletedContinueStep(
    session,
    tabs[visibleActiveTabIdx],
  );
  // Workflow packages may expose a follow-on action from a completed tab.
  const showContinue = displayStatus === 'waiting'
    || displayStatus === 'active'
    || Boolean(completedContinueStepId);
  const showStepRollback =
    (session.status === 'completed' || session.status === 'failed')
    && Boolean(session.steps && session.steps.length > 0);

  // A failed step cannot be checkpoint-resumed — the SubAgent exited uncleanly and there is
  // no valid checkpoint to restore. Only "重试" (full restart) is meaningful in this case.
  // Note: "interrupted" steps CAN be resumed via checkpoint, so only "failed" is blocked.
  const authoritativeCurrent = session.projection?.current ?? [];
  const currentStepStatus = authoritativeCurrent
    .map((id) => session.projection?.nodes?.[id]?.execution)
    .find((status) => status === 'failed')
    ?? (session.current_step_id
      ? session.steps
        ?.filter((s) => s.step_id === session.current_step_id && s.validity !== 'stale')
        ?.sort((a, b) => b.attempt - a.attempt)[0]?.status
      : undefined);
  const effectivePast = new Set(session.projection?.past ?? []);
  const continueDisabled = buttonsDisabled || currentStepStatus === 'failed';

  async function runFooterAction(action: () => void, flushKey?: string) {
    if (sessionBusy || actionPending) return;
    setActionPending(true);
    try {
      const saved = await flushPendingEdits(flushKey);
      if (!saved) return;
      action();
    } finally {
      setActionPending(false);
    }
  }

  function handleContinue() {
    const message = completedContinueStepId
      ? `${t('chat.workflowRollbackPrefix')}${completedContinueStepId}`
      : t('chat.workflowContinue');
    void runFooterAction(() => onSendMessage?.(message));
  }

  function handleRetry() {
    void runFooterAction(() => onSendMessage?.(t('chat.workflowRetry')));
  }

  function handleRollback(stepId: string) {
    void runFooterAction(() => onSendMessage?.(`${t('chat.workflowRollbackPrefix')}${stepId}`));
  }

  const continueLabel = displayStatus === 'waiting'
    ? t('chat.workflowSaveAndContinue')
    : t('chat.workflowContinue');

  const panel = (
    <SlotEditingContext.Provider value={{
      setEditing: handleSlotEditingChange,
      registerFlush,
      registerFooterAction,
    }}>
    <div
      className={`workflow-panel workflow-panel--${displayStatus}${collapsed ? ' workflow-panel--collapsed' : ''}${expanded ? ' workflow-panel--expanded' : ''}`}
      data-session-id={session.session_id}
      aria-label={t('chat.workflowPanelTitle')}
    >
      {/* Header */}
      <div className='workflow-panel__header'>
        <div className='workflow-panel__header-left'>
          <span className='workflow-panel__title'>{ui.name || session.workflow_id}</span>
          <span
            className={`workflow-panel__status workflow-panel__status--${displayStatus}`}
            aria-label={t('chat.workflowStatusAria', { status: t(displayStatusKey) })}
            onClick={() => session && setStateGraphOpen(true)}
            style={{ cursor: 'pointer' }}
            title={t('chat.workflowViewWorkflow')}
            role='button'
            tabIndex={0}
            onKeyDown={(e) => e.key === 'Enter' && session && setStateGraphOpen(true)}
          >
            {t(displayStatusKey)}
          </span>
        </div>
        <div className='workflow-panel__header-right'>
          {hasIntent && (
            <div className='workflow-panel__intent-btn-wrap'>
              <button
                type='button'
                className='workflow-panel__intent-btn'
                onClick={() => setIntentOpen((v) => !v)}
                aria-label={t('chat.workflowIntentBtn')}
                aria-expanded={intentOpen}
              >
                <svg width='13' height='13' viewBox='0 0 13 13' fill='none' xmlns='http://www.w3.org/2000/svg' aria-hidden='true'>
                  <circle cx='6.5' cy='6.5' r='5.75' stroke='currentColor' strokeWidth='1.5' />
                  <path d='M6.5 5.5v4' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
                  <circle cx='6.5' cy='3.75' r='0.75' fill='currentColor' />
                </svg>
                {t('chat.workflowIntentBtn')}
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
          <button
            type='button'
            className='workflow-panel__expand-btn'
            onClick={() => setExpandedMode(!expanded)}
            aria-label={t(expanded ? 'chat.workflowPanelShrink' : 'chat.workflowPanelExpand')}
            title={t(expanded ? 'chat.workflowPanelShrink' : 'chat.workflowPanelExpand')}
          >
            {expanded ? <FullscreenExitOutlined /> : <FullscreenOutlined />}
            <span>{t(expanded ? 'chat.workflowPanelShrinkShort' : 'chat.workflowPanelExpandShort')}</span>
          </button>
          {!expanded && (
            <Tooltip
              title={anySlotEditing ? t('chat.workflowFinishEditingFirst') : undefined}
              placement='bottomRight'
            >
              <span
                className='workflow-panel__header-action-wrap'
                tabIndex={anySlotEditing ? 0 : undefined}
                aria-label={anySlotEditing ? t('chat.workflowFinishEditingFirst') : undefined}
              >
                <Popconfirm
                  title={t('chat.workflowDismissConfirmTitle')}
                  description={t('chat.workflowDismissConfirmDesc')}
                  onConfirm={handleDismiss}
                  okText={t('chat.workflowDismissConfirmOk')}
                  cancelText={t('chat.workflowDismissConfirmCancel')}
                  okButtonProps={{ danger: true, size: 'small' }}
                  cancelButtonProps={{ size: 'small' }}
                  disabled={dismissDisabled}
                  placement='bottomRight'
                >
                  <button
                    type='button'
                    className='workflow-panel__dismiss-btn'
                    disabled={dismissDisabled}
                    aria-label={t('chat.workflowDismissBtn')}
                    title={anySlotEditing ? undefined : t('chat.workflowDismissBtn')}
                  >
                    <svg width='12' height='12' viewBox='0 0 12 12' fill='none' xmlns='http://www.w3.org/2000/svg' aria-hidden='true'>
                      <path d='M2 2L10 10M10 2L2 10' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' />
                    </svg>
                  </button>
                </Popconfirm>
              </span>
            </Tooltip>
          )}
          {!expanded && (
            <Tooltip
              title={collapseDisabled ? t('chat.workflowFinishEditingFirst') : undefined}
              placement='bottomRight'
            >
              <span
                className='workflow-panel__header-action-wrap'
                tabIndex={collapseDisabled ? 0 : undefined}
                aria-label={collapseDisabled ? t('chat.workflowFinishEditingFirst') : undefined}
              >
                <button
                  type='button'
                  className='workflow-panel__collapse-btn'
                  onClick={() => setCollapsed((c) => !c)}
                  disabled={collapseDisabled}
                  aria-label={collapsed ? t('chat.workflowPanelExpand') : t('chat.workflowPanelCollapse')}
                  title={collapseDisabled
                    ? undefined
                    : collapsed
                      ? t('chat.workflowPanelExpand')
                      : t('chat.workflowPanelCollapse')}
                >
                  <svg
                    width='12'
                    height='12'
                    viewBox='0 0 12 12'
                    fill='none'
                    xmlns='http://www.w3.org/2000/svg'
                    className={`workflow-panel__collapse-icon${collapsed ? ' workflow-panel__collapse-icon--up' : ''}`}
                  >
                    <path d='M2 4L6 8L10 4' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
                  </svg>
                </button>
              </span>
            </Tooltip>
          )}
        </div>
      </div>

      {/* Tabs — step navigator style */}
      {!collapsed && hasTabs && (
        <div className='workflow-panel__tabs' role='tablist'>
          {tabs.map((tab, idx) => {
            const statusStepIds = tab.status_step_ids ?? [tab.step_id ?? tab.id];
            const step = session.steps
              ?.filter((s) => statusStepIds.includes(s.step_id) && s.validity !== 'stale')
              .sort((a, b) => (
                Number(b.step_id === session.current_step_id)
                - Number(a.step_id === session.current_step_id)
                || b.attempt - a.attempt
              ))[0];
            const stepStatus = step?.status;
            return (
              <React.Fragment key={tab.id}>
                <button
                  role='tab'
                  aria-selected={idx === visibleActiveTabIdx}
                  aria-controls={`workflow-tab-panel-${tab.id}`}
                  className={`workflow-panel__tab${idx === visibleActiveTabIdx ? ' workflow-panel__tab--active' : ''}${idx < visibleActiveTabIdx ? ' workflow-panel__tab--done' : ''}`}
                  onClick={() => handleTabChange(idx, tab.id)}
                  type='button'
                >
                  <span className='workflow-panel__tab-badge'>{idx + 1}</span>
                  <span className='workflow-panel__tab-label'>{tab.label}</span>
                  {stepStatus && stepStatus !== 'succeeded' && (
                    <span
                      className={`workflow-panel__step-status workflow-panel__step-status--${stepStatus}`}
                      aria-label={`Step status: ${stepStatus}`}
                      title={stepStatus}
                    />
                  )}
                </button>
                {idx < tabs.length - 1 && (
                  <span className={`workflow-panel__tab-connector${idx < visibleActiveTabIdx ? ' workflow-panel__tab-connector--done' : ''}`} aria-hidden='true' />
                )}
              </React.Fragment>
            );
          })}
        </div>
      )}

      {/* Body */}
      {!collapsed && (
        <div className='workflow-panel__body' key={session.session_id}>
          {hasTabs ? (
            tabs.map((tab, idx) => (
              <div
                key={tab.id}
                id={`workflow-tab-panel-${tab.id}`}
                role='tabpanel'
                hidden={idx !== visibleActiveTabIdx}
              >
                <WorkflowPanelTabActiveContext.Provider value={idx === visibleActiveTabIdx}>
                <SlotDownloadContext.Provider value={workflowTabAllowsDownload(tab, idx, tabs.length)}>
                  <TabSlotGrid
                    tab={tab}
                    session={session}
                    tasks={taskCenterTasks}
                    onRefresh={refresh}
                    onReference={onReference}
                    onFocusSortOrder={handleFocusSortOrder}
                  />
                </SlotDownloadContext.Provider>
                </WorkflowPanelTabActiveContext.Provider>
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
        <div className='workflow-panel__footer' role='group' aria-label={t('chat.workflowSessionControls')}>
          {documentFooter.actionItems.length > 0 || documentFooter.statusMessages.length > 0 ? (
            <div className='workflow-panel__footer-document'>
              {documentFooter.statusMessages.length > 0 ? (
                <div className='workflow-panel__footer-meta'>
                  {documentFooter.statusMessages.map((message) => (
                    <span
                      key={message.key}
                      className={
                        message.tone === 'error'
                          ? 'workflow-panel__footer-action-status workflow-panel__footer-action-status--error'
                          : message.tone === 'success'
                            ? 'workflow-panel__footer-action-status workflow-panel__footer-action-status--success'
                            : 'workflow-panel__footer-action-status'
                      }
                      role={message.tone === 'error' ? 'alert' : 'status'}
                    >
                      {message.text}
                    </span>
                  ))}
                </div>
              ) : null}
              {documentFooter.actionItems.length > 0 ? (
                <div className='workflow-panel__footer-actions'>
                  {documentFooter.actionItems.map((item) => {
                    if (item.kind === 'link') {
                      return (
                        <a
                          key={item.key}
                          className='workflow-panel__action-btn workflow-panel__action-btn--secondary workflow-panel__action-btn--link'
                          href={item.href}
                          target='_blank'
                          rel='noopener noreferrer'
                        >
                          <ExportOutlined aria-hidden />
                          {item.label}
                        </a>
                      );
                    }

                    const { action } = item;
                    return (
                      <button
                        key={item.key}
                        type='button'
                        className={`workflow-panel__action-btn workflow-panel__action-btn--${action.tone ?? 'secondary'}`}
                        disabled={actionPending || action.disabled}
                        aria-disabled={actionPending || action.disabled}
                        onClick={() => {
                          if (action.flushBeforeAction) {
                            void runFooterAction(action.onClick, action.flushKey);
                            return;
                          }
                          action.onClick();
                        }}
                      >
                        {action.icon === 'write-back' ? <CloudUploadOutlined aria-hidden /> : null}
                        {action.icon === 'download' ? <DownloadOutlined aria-hidden /> : null}
                        {action.label}
                      </button>
                    );
                  })}
                </div>
              ) : null}
            </div>
          ) : null}
          {displayStatus === 'active' && onStop && (
            <button
              type='button'
              className='workflow-panel__action-btn workflow-panel__action-btn--danger'
              onClick={handleStop}
              title={t('chat.workflowStop')}
            >
              {t('chat.workflowStop')}
            </button>
          )}
          {session.status !== 'completed' && (
            <button
              type='button'
              className='workflow-panel__action-btn workflow-panel__action-btn--secondary'
              disabled={buttonsDisabled}
              aria-disabled={buttonsDisabled}
              onClick={handleRetry}
              title={
                actionPending
                  ? t('chat.workflowSavingBeforeAction')
                  : buttonsDisabled
                    ? t('chat.workflowBtnDisabledHint')
                    : anySlotEditing
                      ? t('chat.workflowRetryFlushHint')
                      : t('chat.workflowRetry')
              }
            >
              {actionPending ? t('chat.workflowSavingBeforeAction') : t('chat.workflowRetry')}
            </button>
          )}
          {showContinue && (
            <button
              type='button'
              className='workflow-panel__action-btn workflow-panel__action-btn--primary'
              disabled={continueDisabled}
              aria-disabled={continueDisabled}
              onClick={handleContinue}
              title={
                currentStepStatus === 'failed'
                  ? t('chat.workflowContinueDisabledFailed')
                  : actionPending
                    ? t('chat.workflowSavingBeforeAction')
                    : buttonsDisabled
                      ? t('chat.workflowBtnDisabledHint')
                      : anySlotEditing
                        ? t('chat.workflowContinueFlushHint')
                        : completedContinueStepId
                          ? t('chat.workflowContinueWithLatestOutline')
                          : continueLabel
              }
            >
              {actionPending ? t('chat.workflowSavingBeforeAction') : continueLabel}
            </button>
          )}
          {showStepRollback && (
            <div style={{ flex: '1 1 100%', display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 12, color: '#6b7280', fontWeight: 500 }}>{t('chat.workflowRollbackLabel')}</span>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                {session.steps!
                  .filter((step, index, all) => effectivePast.has(step.step_id)
                    && step.validity !== 'stale'
                    && all.findIndex((candidate) => candidate.step_id === step.step_id && candidate.validity !== 'stale') === index)
                  .map((step) => (
                  <button
                    key={`${step.step_id}-${step.attempt}`}
                    type='button'
                    className='workflow-panel__action-btn workflow-panel__action-btn--secondary'
                    style={{ padding: '3px 10px', fontSize: 12 }}
                    disabled={buttonsDisabled}
                    aria-disabled={buttonsDisabled}
                    onClick={() => handleRollback(step.step_id)}
                    title={
                      actionPending
                        ? t('chat.workflowSavingBeforeAction')
                        : buttonsDisabled
                          ? t('chat.workflowBtnDisabledHint')
                          : `${t('chat.workflowRollbackPrefix')}${step.step_id}`
                    }
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
        workflowId={session.workflow_id}
        liveRefresh
        conversationId={conversationId}
      />
    )}
    </SlotEditingContext.Provider>
  );

  if (expanded) {
    const host = document.querySelector('.detail-container');
    if (host) return createPortal(panel, host);
  }
  return panel;
}
