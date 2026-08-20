import { create } from "zustand";
import { WorkflowInfoApi, WorkflowSessionApi, TempUploadServiceApi } from "@/modules/chat/utils/request";
import i18n from "@/i18n";
import type { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { extractErrorCode, getLocalizedErrorMessage } from "@/components/request";
import { reconcileWorkflowSessionStatus } from '@/modules/chat/store/workflowStatus';

export function buildWorkflowSearchConfig(
  chatConfig?: Pick<ChatConfig, "knowledgeBaseId" | "creators" | "tags">,
): Record<string, unknown> {
  const kbIds = chatConfig?.knowledgeBaseId?.filter(Boolean) ?? [];
  return {
    dataset_list: kbIds.map((id) => ({ id })),
    creators: chatConfig?.creators ?? [],
    tags: chatConfig?.tags ?? [],
  };
}

// ---------------------------------------------------------------------------
// DraftStore — two-layer draft management for slot text editing
// key format: `${sessionId}:${slotId}:${listIndex}`
// ---------------------------------------------------------------------------

interface DraftEntry {
  value: Record<string, unknown>;
  timer: ReturnType<typeof setTimeout> | null;
  /** The list_index to use when calling the backend API (-1 for single/NULL slots). */
  apiListIndex: number;
}

const DRAFT_FLUSH_DELAY_MS = 60_000;
const DRAFT_LS_PREFIX = 'slotDraft:';

const _drafts = new Map<string, DraftEntry>();

// A write-back can finish while a session request that started earlier is still
// in flight. Do not discard the refresh in that case: queue one follow-up load
// so the selected artifact eventually converges to the new provider_sync revision.
const _activeSessionLoads = new Map<string, Promise<void>>();
const _queuedActiveSessionLoads = new Map<string, { silentError?: boolean }>();

function _draftKey(sessionId: string, slotId: string, listIndex: number): string {
  return `${sessionId}:${slotId}:${listIndex}`;
}

export const draftStore = {
  /** Write value to localStorage and reset the 60s auto-flush timer.
   *  apiListIndex: the list_index to use for the backend PATCH call.
   *  Pass -1 for single (non-list) slots. Defaults to listIndex when omitted.
   */
  setDraft(sessionId: string, slotId: string, listIndex: number, value: Record<string, unknown>, apiListIndex?: number) {
    const key = _draftKey(sessionId, slotId, listIndex);
    const existing = _drafts.get(key);
    if (existing?.timer) clearTimeout(existing.timer);
    try {
      localStorage.setItem(DRAFT_LS_PREFIX + key, JSON.stringify(value));
    } catch { /* storage full — ignore */ }
    const effectiveApiIndex = apiListIndex ?? existing?.apiListIndex ?? listIndex;
    const timer = setTimeout(() => {
      draftStore.flushDraft(sessionId, slotId, listIndex, effectiveApiIndex);
    }, DRAFT_FLUSH_DELAY_MS);
    _drafts.set(key, { value, timer, apiListIndex: effectiveApiIndex });
  },

  /** Clear timer and call patchSlotItemValue to produce a human revision. Does NOT clear localStorage.
   *  apiListIndex: when provided, used for the backend PATCH call (e.g. -1 for single slots);
   *  otherwise falls back to the stored entry's apiListIndex, then listIndex.
   *
   *  When the original artifact value contained a `path` field (large content was offloaded),
   *  the draft text is first uploaded via POST /temp/uploads, then the PATCH carries the new
   *  stored_path instead of the raw text — preserving the large-content offload contract.
   */
  async flushDraft(sessionId: string, slotId: string, listIndex: number, apiListIndex?: number): Promise<void> {
    const key = _draftKey(sessionId, slotId, listIndex);
    let value: Record<string, unknown> | null = null;
    let targetIndex = apiListIndex ?? listIndex;
    const entry = _drafts.get(key);
    if (entry) {
      if (entry.timer) clearTimeout(entry.timer);
      _drafts.set(key, { value: entry.value, timer: null, apiListIndex: entry.apiListIndex });
      value = entry.value;
      targetIndex = apiListIndex ?? entry.apiListIndex;
    } else {
      value = draftStore.getLocalDraft(sessionId, slotId, listIndex);
    }
    if (!value) return;

    // Detect large-content (offloaded) draft: value carries {text: string, _isOffloaded: true}
    // When the original artifact had a `path` field the SlotText component sets _isOffloaded=true
    // so we know to re-upload the edited text instead of writing it inline to the DB.
    let patchValue = value;
    if (value._isOffloaded && typeof value.text === 'string') {
      try {
        const text = value.text as string;
        const blob = new Blob([text], { type: 'text/plain' });
        const filename = (value._originalFilename as string | undefined) ?? 'artifact.txt';
        const api = TempUploadServiceApi();
        const initRes = await api.initUpload({ filename, size: blob.size, content_type: 'text/plain' });
        const uploadId: string = initRes.data?.data?.upload_id ?? initRes.data?.upload_id;
        await api.uploadPart(uploadId, 1, blob);
        const completeRes = await api.completeUpload(uploadId, { parts: [{ part_number: 1, size: blob.size }] });
        const storedPath: string = completeRes.data?.data?.stored_path ?? completeRes.data?.stored_path;
        patchValue = { type: 'text', path: storedPath, size: blob.size };
      } catch {
        // Upload failed — fall back to inline patch so user doesn't lose their edit
        patchValue = { text: value.text as string };
      }
    }

    try {
      await WorkflowSessionApi().patchSlotItem(sessionId, slotId, targetIndex, patchValue);
    } catch { /* best-effort — ignore */ }
    _drafts.delete(key);
    try { localStorage.removeItem(DRAFT_LS_PREFIX + key); } catch { /* ignore */ }
  },

  /** Flush all pending drafts for a session in parallel. Used before sending chat. */
  async flushAllDrafts(sessionId: string): Promise<void> {
    const prefix = `${sessionId}:`;
    const tasks: Promise<void>[] = [];
    for (const key of Array.from(_drafts.keys())) {
      if (!key.startsWith(prefix)) continue;
      const parts = key.split(':');
      if (parts.length < 3) continue;
      const slotId = parts[1];
      const listIndex = Number(parts[2]);
      if (!slotId || isNaN(listIndex)) continue;
      tasks.push(draftStore.flushDraft(sessionId, slotId, listIndex));
    }
    await Promise.all(tasks);
  },

  /** Discard draft without producing a revision. Clears localStorage and timer. */
  cancelDraft(sessionId: string, slotId: string, listIndex: number) {
    const key = _draftKey(sessionId, slotId, listIndex);
    const existing = _drafts.get(key);
    if (existing?.timer) clearTimeout(existing.timer);
    _drafts.delete(key);
    try {
      localStorage.removeItem(DRAFT_LS_PREFIX + key);
    } catch { /* ignore */ }
  },

  /** Read a persisted draft from localStorage (for mount-time restore). */
  getLocalDraft(sessionId: string, slotId: string, listIndex: number): Record<string, unknown> | null {
    const key = _draftKey(sessionId, slotId, listIndex);
    try {
      const raw = localStorage.getItem(DRAFT_LS_PREFIX + key);
      if (!raw) return null;
      return JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return null;
    }
  },
};

export interface SlotRevision {
  slot_id: string;
  revision: number;
  list_index?: number;
  /** 1-based display position within a list slot; computed from order_list. */
  sort_order?: number;
  /** Optimistic-lock version of the slot order row; present on list-slot items. */
  order_version?: number;
  selected: boolean;
  slot: string;
  step_id?: string;
  created_at: string;
  /** Artifact content type returned by the backend (e.g. 'text', 'image', 'file'). */
  content_type?: string;
  /** Artifact value as returned by the backend — shape depends on content_type. */
  artifact_value?: any;
  /** Human-readable description for image/file artifacts. */
  caption?: string;
  /** change_source: ai / human / provider_sync (Feishu-confirmed). */
  change_source?: "ai" | "human" | "provider_sync";
  /** Whether this draft has a server-owned Feishu baseline. */
  write_back_ready?: boolean;
  /** Whether the selected draft differs from that Feishu baseline. */
  write_back_dirty?: boolean;
  /** Server-owned delivery state for the selected draft. */
  write_back_state?: 'initial_delivery' | 'synced_clean' | 'synced_dirty' | 'blocked';
  /** Public Feishu document URL resolved by the server from source_document. */
  write_back_url?: string;
  /** Cloud provider bound to source_document, for example "feishu". */
  provider?: string;
  /** Stable cloud-document identity. It is never a local revision number. */
  provider_document_id?: string;
  /** Most recent local revision confirmed equal to the cloud document. */
  last_synced_revision?: number;
  /** Number of revisions for this (slot_id, list_index) — used to show version badge. */
  revision_count?: number;
}

export interface WorkflowSession {
  session_id: string;
  conversation_id: string;
  workflow_id: string;
  /** Immutable package revision selected when this session was created. */
  pinned_revision_id?: string;
  status: "active" | "completed" | "failed" | "waiting" | "stopped";
  current_step_id: string;
  /** Global intent/constraint for this session, JSON string e.g. {"text":"..."} */
  intent_context?: string;
  created_at: string;
  updated_at: string;
  slots?: SlotRevision[];
  /** Steps for this session, used in completed/waiting state to render rollback step list. */
  steps?: WorkflowSessionStep[];
  /** Go-authoritative runtime projection. Never derive Ready/Past from steps locally. */
  projection?: WorkflowRuntimeProjection;
  /** Fatal runtime error that makes this conversation's pinned workflow graph unusable. */
  runtime_error_code?: string;
  runtime_error_message?: string;
  /** UI focus state mirrored onto the session for legacy readers; the source of
   *  truth lives in `focusedTabByConversation` / `focusedSortOrderByConversation`
   *  so it survives `setSession()` refreshes. */
  focusedTab?: string;
  focusedSortOrder?: number;
}

/** A single step execution record from workflow_session_steps. */
export interface WorkflowSessionStep {
  id: string;
  session_id: string;
  step_id: string;
  attempt: number;
  task_id: string;
  status: string;
  validity?: "effective" | "stale";
  /** Step-level intent/constraint, JSON string e.g. {"text":"..."} */
  intent_context?: string;
  created_at: string;
  updated_at: string;
}

export interface WorkflowRuntimeProjection {
  completed?: boolean;
  past?: string[];
  current?: string[];
  reachable?: string[];
  ready?: string[];
  blocked?: string[];
  stale?: string[];
  pruned?: string[];
  bypassed?: string[];
  nodes?: Record<string, {
    execution: string;
    validity: string;
    reachability: string;
    readiness: string;
    branch: string;
  }>;
}

// UI tab/slot declaration from workflow.yaml.
export interface SlotDef {
  id: string;
  label: string;
  type: "image" | "text" | "file";
  cardinality?: "single" | "list";
  /** Whether this list slot supports drag-reorder. */
  ordered?: boolean;
  /** The slot key used for the caption of this slot's items. */
  caption_key?: string;
  /** Maximum characters shown in the artifact summary injected into the AI prompt. */
  summary_max_chars?: number;
  /** Runtime widget configuration from ui.slots, hydrated when the workflow UI is loaded. */
  widget?: SlotWidgetConfig;
}

export interface SlotWidgetConfig {
  widgetType?: string;
  readOnly?: boolean;
  maxHeight?: number;
  [key: string]: unknown;
}

// composite_layout node types (recursive) — format C.
export interface CompositePanelNode {
  /** Leaf: single slot id. */
  slot?: string;
  /** Leaf: tab-switching area, each item is a slot id. Tab title is derived from slot label. */
  tabs?: string[];
  /** Container: split direction. */
  direction?: 'row' | 'column';
  children?: CompositePanelNode[];
  weight?: number;
}

// Legacy composite layout types kept for backward-compat parsing in buildColumns.
export type CompositeLayoutNode =
  | string
  | CompositeColumnNode
  | InnerTabsNode;

export interface CompositeColumnNode {
  slot?: string | InnerTabsNode;
  weight?: number;
}

export interface InnerTabsNode {
  tabs: CompositeLayoutNode[];
}

/** Declarative action rendered for a workflow tab. */
export interface WorkflowTabAction {
  id: string;
  type: 'export';
  provider: string;
  label?: string;
  /** Provider input names mapped to declared slot ids. */
  inputs: Record<string, string>;
  formats?: string[];
  /** Align mapped list slots by their shared sort_order. */
  alignment?: 'sort_order';
}

export interface TabDef {
  id: string;
  /** Optional workflow step id represented by this tab. Falls back to id when omitted. */
  step_id?: string;
  label: string;
  layout?: 'grid' | 'list' | 'vertical' | 'composite' | 'horizontal';
  slots: SlotDef[];
  /** Composite layout tree (format C) or legacy array (will be normalised at runtime). */
  composite_layout?: CompositePanelNode | CompositeLayoutNode[];
  /** Composite mode: global tab-bar position. */
  composite_tab_position?: 'top' | 'bottom' | 'left' | 'right';
  /**
   * Generic composite display rules declared by the workflow.
   * WorkflowPanel must not special-case workflow IDs; it only executes these rules.
   */
  composite_behavior?: CompositeBehavior;
  /** Actions are rendered through provider modules; the composite stays domain-neutral. */
  actions?: WorkflowTabAction[];
}

/** Mutually exclusive column group: keep the first preferred slot that has data. */
export interface CompositeMutuallyExclusiveGroup {
  slots: string[];
  /** Preference order when multiple group members have data. Defaults to `slots`. */
  prefer?: string[];
}

/**
 * Workflow-declared composite display behavior (UI schema).
 * - hide_empty_columns: drop columns with no matching revisions
 * - empty_column_scope: which revisions count as non-empty
 * - mutually_exclusive: among a group, show only one winner column
 */
export interface CompositeBehavior {
  hide_empty_columns?: boolean;
  empty_column_scope?: 'selected' | 'tab';
  mutually_exclusive?: CompositeMutuallyExclusiveGroup[];
}

export interface WorkflowUI {
  name?: string;
  tabs?: TabDef[];
  /** Global widget config keyed by slot id. */
  slots?: Record<string, Record<string, unknown>>;
}

/**
 * The catalog keeps reusable slot metadata at the workflow root while UI tabs
 * commonly reference a slot with only `{ id }`.  Hydrate those references so
 * renderers can see properties such as `type`, `cardinality`, and `ordered`.
 */
export function hydrateWorkflowUI(raw: unknown, fallbackName?: string): WorkflowUI {
  if (!raw || typeof raw !== 'object') return {};
  const spec = raw as Record<string, unknown>;
  const rawUI = spec.ui;
  if (!rawUI || typeof rawUI !== 'object' || Array.isArray(rawUI)) return {};

  const ui = rawUI as WorkflowUI;
  const slotDefs = new Map<string, SlotDef>();
  if (Array.isArray(spec.slots)) {
    for (const value of spec.slots) {
      if (!value || typeof value !== 'object' || Array.isArray(value)) continue;
      const slot = value as SlotDef;
      if (typeof slot.id === 'string' && slot.id) slotDefs.set(slot.id, slot);
    }
  }

  const name = typeof spec.name === 'string' ? spec.name : fallbackName;
  if (!Array.isArray(ui.tabs)) {
    return name === undefined ? ui : { ...ui, name };
  }
  const hasWidgetConfigs = Boolean(ui.slots && Object.keys(ui.slots).length > 0);
  if (slotDefs.size === 0 && !hasWidgetConfigs) {
    return name === undefined ? ui : { ...ui, name };
  }
  return {
    ...ui,
    ...(name === undefined ? {} : { name }),
    tabs: ui.tabs.map((tab) => ({
      ...tab,
      slots: Array.isArray(tab.slots)
        ? tab.slots.map((slot) => {
          const widget = ui.slots?.[slot.id];
          return {
            ...slotDefs.get(slot.id),
            ...slot,
            ...(widget ? { widget } : {}),
          } as SlotDef;
        })
        : [],
    })),
  };
}

export interface SlotVersionEntry {
  revision: number;
  change_source: "ai" | "human" | "provider_sync";
  created_at: string;
  selected: boolean;
  /** Whether this historical Writer revision was provider-confirmed. */
  provider_synced?: boolean;
  content_snapshot?: any;
}

interface WorkflowStore {
  // Latest session per conversation (any status, not just active).
  sessionByConversation: Record<string, WorkflowSession | null>;
  loadingByConversation: Record<string, boolean>;
  // Whether auto-advance is running (driver agent triggered next chat turn).
  // Keyed by conversation_id. True = input should be disabled.
  autoRunningByConversation: Record<string, boolean>;
  // Workflow UI definition cache: keyed by workflow_id.
  workflowUIByWorkflow: Record<string, WorkflowUI>;
  // Incremented each time a session is dismissed, keyed by conversation_id.
  // DismissedWorkflowRestoreButton subscribes to this to re-fetch the dismissed list.
  dismissedRefreshTrigger: Record<string, number>;
  // Cached dismissed sessions per conversation. Survives component remounts.
  dismissedSessionsByConversation: Record<string, Array<{ session_id: string; workflow_id: string }>>;

  /** UI focus state keyed by conversation_id; held outside `sessionByConversation`
   *  so server refreshes don't overwrite the user's tab / sort_order focus. */
  focusedTabByConversation: Record<string, string | undefined>;
  focusedSortOrderByConversation: Record<string, number | undefined>;
  setSession: (conversationId: string, session: WorkflowSession | null) => void;
  updateSlot: (conversationId: string, slot: SlotRevision) => void;
  loadActiveSession: (
    conversationId: string,
    options?: { silentError?: boolean },
  ) => Promise<void>;
  refreshSlots: (conversationId: string, sessionId: string) => Promise<void>;
  patchSlot: (conversationId: string, sessionId: string, slotId: string, revision: number) => Promise<void>;
  syncSessionSearchConfig: (conversationId: string, sessionId: string, searchConfig: Record<string, unknown>) => Promise<void>;
  setAutoRunning: (conversationId: string, running: boolean) => void;
  fetchWorkflowUI: (workflowId: string) => Promise<WorkflowUI>;
  bumpDismissedRefresh: (conversationId: string) => void;
  fetchDismissedSessions: (conversationId: string) => Promise<void>;
  // Phase 3: slot item management.
  deleteSlotItem: (sessionId: string, slotId: string, listIndex: number, orderVersion?: number) => Promise<void>;
  patchSlotItemValue: (
    sessionId: string,
    slotId: string,
    listIndex: number,
    value: any,
    contentType?: string,
    mode?: 'draft' | 'checkpoint',
    baseRevision?: number,
  ) => Promise<number | undefined>;
  reorderSlotItems: (sessionId: string, slotId: string, newSortOrderSeq: number[], version: number) => Promise<void>;
  getSlotVersions: (sessionId: string, slotId: string, listIndex: number) => Promise<SlotVersionEntry[]>;
  rollbackSlotItem: (sessionId: string, slotId: string, listIndex: number, revision: number) => Promise<void>;
  createSlotItem: (sessionId: string, slotId: string, value: any, caption?: string, insertBefore?: number, contentType?: string) => Promise<void>;
  patchSlotCaption: (sessionId: string, slotId: string, listIndex: number, caption: string) => Promise<void>;
  // Track focused tab and sort_order for the AI. Held in sibling maps so the
  // value persists across `setSession()` refreshes that would otherwise wipe it.
  setFocusedTab: (conversationId: string, tabId: string) => void;
  setFocusedSortOrder: (conversationId: string, sortOrder: number | undefined) => void;
}

export const useWorkflowStore = create<WorkflowStore>()((set, get) => ({
  sessionByConversation: {},
  loadingByConversation: {},
  autoRunningByConversation: {},
  workflowUIByWorkflow: {},
  dismissedRefreshTrigger: {},
  dismissedSessionsByConversation: {},
  focusedTabByConversation: {},
  focusedSortOrderByConversation: {},

  bumpDismissedRefresh: (conversationId) => {
    set((s) => ({
      dismissedRefreshTrigger: {
        ...s.dismissedRefreshTrigger,
        [conversationId]: (s.dismissedRefreshTrigger[conversationId] ?? 0) + 1,
      },
    }));
  },

  fetchDismissedSessions: async (conversationId) => {
    try {
      const resp = await WorkflowSessionApi().listDismissedSessions(conversationId);
      const sessions = (resp.data?.data?.sessions ?? []) as Array<{ session_id: string; workflow_id: string }>;
      set((s) => ({
        dismissedSessionsByConversation: {
          ...s.dismissedSessionsByConversation,
          [conversationId]: sessions,
        },
      }));
    } catch {
      // silently ignore — stale cache is fine
    }
  },

  setSession: (conversationId, session) => {
    set((state) => {
      const next: Partial<WorkflowStore> = {
        sessionByConversation: { ...state.sessionByConversation, [conversationId]: session },
      };
      if (session && session.status !== 'active') {
        if (state.autoRunningByConversation[conversationId]) {
          next.autoRunningByConversation = {
            ...state.autoRunningByConversation,
            [conversationId]: false,
          };
        }
      }
      return next;
    });
  },

  updateSlot: (conversationId, slot) => {
    set((state) => {
      const session = state.sessionByConversation[conversationId];
      if (!session) return state;
      const slots = session.slots ?? [];
      const idx = slots.findIndex(
        (s) => s.slot_id === slot.slot_id && (s.list_index ?? -1) === (slot.list_index ?? -1),
      );
      let nextSlots: SlotRevision[];
      if (idx >= 0) {
        nextSlots = slots.slice();
        nextSlots[idx] = slot;
      } else {
        nextSlots = [...slots, slot];
      }
      return {
        sessionByConversation: {
          ...state.sessionByConversation,
          [conversationId]: { ...session, slots: nextSlots },
        },
      };
    });
  },

  loadActiveSession: async (conversationId, options) => {
    if (!conversationId) return;
    const activeLoad = _activeSessionLoads.get(conversationId);
    if (activeLoad) {
      const queuedOptions = _queuedActiveSessionLoads.get(conversationId);
      _queuedActiveSessionLoads.set(conversationId, {
        silentError: queuedOptions
          ? Boolean(queuedOptions.silentError && options?.silentError)
          : options?.silentError,
      });
      await activeLoad;
      return;
    }

    const load = (async () => {
      set((s) => ({
        loadingByConversation: { ...s.loadingByConversation, [conversationId]: true },
      }));
      try {
        const requestOptions = options?.silentError
          ? ({ silentError: true } as never)
          : undefined;
        const res = await WorkflowSessionApi().getLatestSession(
          conversationId,
          requestOptions,
        );
        const session: WorkflowSession | null = res?.data?.data?.session ?? null;
        // Runtime controls and rollback candidates come from Go's projection.
        // Steps are attempt history only; they never define Past/Ready locally.
        if (session?.session_id) {
          try {
            const [stepsRes, projectionRes] = await Promise.all([
              WorkflowSessionApi().getSteps(session.session_id, requestOptions),
              WorkflowSessionApi().getProjection(
                session.session_id,
                { silentError: true } as never,
              ),
            ]);
            const rawSteps = stepsRes?.data?.data?.steps ?? [];
            session.steps = rawSteps.filter((s: WorkflowSessionStep) => s.step_id !== '__end__');
            session.projection = projectionRes?.data?.data?.projection ?? {};
            session.status = reconcileWorkflowSessionStatus(session.status, session.projection);
          } catch (error) {
            session.steps = [];
            session.projection = {};
            const errorCode = extractErrorCode(error);
            if (errorCode === "WORKFLOW_DEFINITION_CHANGED") {
              session.runtime_error_code = errorCode;
              session.runtime_error_message = getLocalizedErrorMessage(error);
            }
          }
        }
        get().setSession(conversationId, session);
        // Also refresh dismissed sessions so the restore button appears immediately on load.
        get().fetchDismissedSessions(conversationId);
      } catch {
        // ignore
      } finally {
        set((s) => ({
          loadingByConversation: { ...s.loadingByConversation, [conversationId]: false },
        }));
      }
    })();

    _activeSessionLoads.set(conversationId, load);
    try {
      await load;
    } finally {
      if (_activeSessionLoads.get(conversationId) === load) {
        _activeSessionLoads.delete(conversationId);
      }
      const queuedOptions = _queuedActiveSessionLoads.get(conversationId);
      if (queuedOptions) {
        _queuedActiveSessionLoads.delete(conversationId);
        await get().loadActiveSession(conversationId, queuedOptions);
      }
    }
  },

  refreshSlots: async (conversationId, sessionId) => {
    try {
      const res = await WorkflowSessionApi().getSlots(sessionId);
      const slots: SlotRevision[] = res?.data?.data?.slots ?? [];
      set((state) => {
        const session = state.sessionByConversation[conversationId];
        if (!session) return state;
        return {
          sessionByConversation: {
            ...state.sessionByConversation,
            [conversationId]: { ...session, slots },
          },
        };
      });
    } catch {
      // ignore
    }
  },

  patchSlot: async (conversationId, sessionId, slotId, revision) => {
    try {
      await WorkflowSessionApi().patchSlot(sessionId, slotId, revision);
      get().refreshSlots(conversationId, sessionId);
    } catch {
      // ignore
    }
  },

  syncSessionSearchConfig: async (_conversationId, sessionId, searchConfig) => {
    try {
      await WorkflowSessionApi().syncSessionSearchConfig(sessionId, searchConfig);
    } catch {
      // ignore
    }
  },

  setAutoRunning: (conversationId, running) => {
    set((state) => ({
      autoRunningByConversation: { ...state.autoRunningByConversation, [conversationId]: running },
    }));
  },

  fetchWorkflowUI: async (workflowId) => {
    const lang = i18n.language || "";
    const cacheKey = `${workflowId}:${lang}`;
    // Return cached value if already fetched for this language.
    const cached = get().workflowUIByWorkflow[cacheKey];
    if (cached) return cached;
    try {
      const res = await WorkflowInfoApi().getWorkflow(workflowId, {
        headers: lang ? { "Accept-Language": lang } : undefined,
      });
      const payload = res?.data?.data ?? res?.data ?? {};
      const ui = hydrateWorkflowUI(payload, workflowId);
      set((state) => ({
        workflowUIByWorkflow: { ...state.workflowUIByWorkflow, [cacheKey]: ui },
      }));
      return ui;
    } catch {
      return {};
    }
  },

  deleteSlotItem: async (sessionId, slotId, listIndex, orderVersion) => {
    await WorkflowSessionApi().deleteSlotItem(sessionId, slotId, listIndex, orderVersion);
  },

  patchSlotItemValue: async (sessionId, slotId, listIndex, value, contentType, mode, baseRevision) => {
    const res = await WorkflowSessionApi().patchSlotItem(
      sessionId, slotId, listIndex, value, contentType, mode, baseRevision,
    );
    const revision = res?.data?.data?.revision;
    return typeof revision === 'number' ? revision : undefined;
  },

  reorderSlotItems: async (sessionId, slotId, newSortOrderSeq, version) => {
    await WorkflowSessionApi().reorderSlotItems(sessionId, slotId, newSortOrderSeq, version);
  },

  getSlotVersions: async (sessionId, slotId, listIndex) => {
    const res = await WorkflowSessionApi().getSlotItemVersions(sessionId, slotId, listIndex);
    return res?.data?.data?.versions ?? [];
  },

  rollbackSlotItem: async (sessionId, slotId, listIndex, revision) => {
    await WorkflowSessionApi().rollbackSlotItem(sessionId, slotId, listIndex, revision);
  },

  createSlotItem: async (sessionId, slotId, value, caption, insertBefore, contentType) => {
    await WorkflowSessionApi().createSlotItem(sessionId, slotId, value, caption, insertBefore, contentType);
  },

  patchSlotCaption: async (sessionId, slotId, listIndex, caption) => {
    await WorkflowSessionApi().patchSlotCaption(sessionId, slotId, listIndex, caption);
  },

  setFocusedTab: (conversationId, tabId) => {
    set((state) => {
      // Write to the sibling map; mirror onto the session as a fallback so
      // legacy readers (chatLayout request assembly) still see the value.
      const nextFocusedMap = {
        ...state.focusedTabByConversation,
        [conversationId]: tabId,
      };
      const session = state.sessionByConversation[conversationId];
      const nextSessionMap = session
        ? {
            ...state.sessionByConversation,
            [conversationId]: { ...session, focusedTab: tabId },
          }
        : state.sessionByConversation;
      return {
        focusedTabByConversation: nextFocusedMap,
        sessionByConversation: nextSessionMap,
      };
    });
  },

  setFocusedSortOrder: (conversationId, sortOrder) => {
    set((state) => {
      const nextFocusedMap = {
        ...state.focusedSortOrderByConversation,
        [conversationId]: sortOrder,
      };
      const session = state.sessionByConversation[conversationId];
      const nextSessionMap = session
        ? {
            ...state.sessionByConversation,
            [conversationId]: { ...session, focusedSortOrder: sortOrder },
          }
        : state.sessionByConversation;
      return {
        focusedSortOrderByConversation: nextFocusedMap,
        sessionByConversation: nextSessionMap,
      };
    });
  },

}));
