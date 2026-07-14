import { create } from "zustand";
import { PluginInfoApi, PluginSessionApi, TempUploadServiceApi } from "@/modules/chat/utils/request";
import i18n from "@/i18n";
import type { ChatConfig } from "@/modules/chat/components/ChatConfigs";
import { extractErrorCode, getLocalizedErrorMessage } from "@/components/request";

export function buildPluginSearchConfig(
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
      await PluginSessionApi().patchSlotItem(sessionId, slotId, targetIndex, patchValue);
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
  /** change_source: 'ai' (generated) or 'human' (manually edited). */
  change_source?: "ai" | "human";
  /** Number of revisions for this (slot_id, list_index) — used to show version badge. */
  revision_count?: number;
}

export interface PluginSession {
  session_id: string;
  conversation_id: string;
  plugin_id: string;
  status: "active" | "completed" | "failed" | "waiting";
  current_step_id: string;
  /** Global intent/constraint for this session, JSON string e.g. {"text":"..."} */
  intent_context?: string;
  created_at: string;
  updated_at: string;
  slots?: SlotRevision[];
  /** Steps for this session, used in completed/waiting state to render rollback step list. */
  steps?: PluginSessionStep[];
  /** Go-authoritative runtime projection. Never derive Ready/Past from steps locally. */
  projection?: PluginRuntimeProjection;
  /** Fatal runtime error that makes this conversation's pinned plugin graph unusable. */
  runtime_error_code?: string;
  runtime_error_message?: string;
  /** UI focus state mirrored onto the session for legacy readers; the source of
   *  truth lives in `focusedTabByConversation` / `focusedSortOrderByConversation`
   *  so it survives `setSession()` refreshes. */
  focusedTab?: string;
  focusedSortOrder?: number;
}

/** A single step execution record from plugin_session_steps. */
export interface PluginSessionStep {
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

export interface PluginRuntimeProjection {
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

// UI tab/slot declaration from plugin.yaml.
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
}

export interface PluginUI {
  tabs?: TabDef[];
  /** Global widget config keyed by slot id. */
  slots?: Record<string, Record<string, unknown>>;
}

export interface SlotVersionEntry {
  revision: number;
  change_source: "ai" | "human";
  created_at: string;
  selected: boolean;
  content_snapshot?: any;
}

interface PluginStore {
  // Latest session per conversation (any status, not just active).
  sessionByConversation: Record<string, PluginSession | null>;
  loadingByConversation: Record<string, boolean>;
  // Whether auto-advance is running (driver agent triggered next chat turn).
  // Keyed by conversation_id. True = input should be disabled.
  autoRunningByConversation: Record<string, boolean>;
  // Plugin UI definition cache: keyed by plugin_id.
  pluginUIByPlugin: Record<string, PluginUI>;
  // Incremented each time a session is dismissed, keyed by conversation_id.
  // DismissedPluginRestoreButton subscribes to this to re-fetch the dismissed list.
  dismissedRefreshTrigger: Record<string, number>;
  // Cached dismissed sessions per conversation. Survives component remounts.
  dismissedSessionsByConversation: Record<string, Array<{ session_id: string; plugin_id: string }>>;

  /** UI focus state keyed by conversation_id; held outside `sessionByConversation`
   *  so server refreshes don't overwrite the user's tab / sort_order focus. */
  focusedTabByConversation: Record<string, string | undefined>;
  focusedSortOrderByConversation: Record<string, number | undefined>;

  setSession: (conversationId: string, session: PluginSession | null) => void;
  updateSlot: (conversationId: string, slot: SlotRevision) => void;
  loadActiveSession: (conversationId: string) => Promise<void>;
  refreshSlots: (conversationId: string, sessionId: string) => Promise<void>;
  patchSlot: (conversationId: string, sessionId: string, slotId: string, revision: number) => Promise<void>;
  syncSessionSearchConfig: (conversationId: string, sessionId: string, searchConfig: Record<string, unknown>) => Promise<void>;
  setAutoRunning: (conversationId: string, running: boolean) => void;
  fetchPluginUI: (pluginId: string) => Promise<PluginUI>;
  bumpDismissedRefresh: (conversationId: string) => void;
  fetchDismissedSessions: (conversationId: string) => Promise<void>;
  // Phase 3: slot item management.
  deleteSlotItem: (sessionId: string, slotId: string, listIndex: number, orderVersion?: number) => Promise<void>;
  patchSlotItemValue: (sessionId: string, slotId: string, listIndex: number, value: any, contentType?: string) => Promise<void>;
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

export const usePluginStore = create<PluginStore>()((set, get) => ({
  sessionByConversation: {},
  loadingByConversation: {},
  autoRunningByConversation: {},
  pluginUIByPlugin: {},
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
    // Also refresh the cached dismissed list so any remounted component gets fresh data.
    get().fetchDismissedSessions(conversationId);
  },

  fetchDismissedSessions: async (conversationId) => {
    try {
      const resp = await PluginSessionApi().listDismissedSessions(conversationId);
      const sessions = (resp.data?.data?.sessions ?? []) as Array<{ session_id: string; plugin_id: string }>;
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
      const next: Partial<PluginStore> = {
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

  loadActiveSession: async (conversationId) => {
    if (!conversationId) return;
    // Deduplicate concurrent calls for the same conversation.
    if (get().loadingByConversation[conversationId]) return;
    set((s) => ({
      loadingByConversation: { ...s.loadingByConversation, [conversationId]: true },
    }));
    try {
      const res = await PluginSessionApi().getLatestSession(conversationId);
      const session: PluginSession | null = res?.data?.data?.session ?? null;
      // Runtime controls and rollback candidates come from Go's projection.
      // Steps are attempt history only; they never define Past/Ready locally.
      if (session?.session_id) {
        try {
          const [stepsRes, projectionRes] = await Promise.all([
            PluginSessionApi().getSteps(session.session_id),
            PluginSessionApi().getProjection(
              session.session_id,
              { silentError: true } as never,
            ),
          ]);
          const rawSteps = stepsRes?.data?.data?.steps ?? [];
          session.steps = rawSteps.filter((s: PluginSessionStep) => s.step_id !== '__end__');
          session.projection = projectionRes?.data?.data?.projection ?? {};
        } catch (error) {
          session.steps = [];
          session.projection = {};
          const errorCode = extractErrorCode(error);
          if (errorCode === "PLUGIN_DEFINITION_CHANGED") {
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
  },

  refreshSlots: async (conversationId, sessionId) => {
    try {
      const res = await PluginSessionApi().getSlots(sessionId);
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
      await PluginSessionApi().patchSlot(sessionId, slotId, revision);
      get().refreshSlots(conversationId, sessionId);
    } catch {
      // ignore
    }
  },

  syncSessionSearchConfig: async (_conversationId, sessionId, searchConfig) => {
    try {
      await PluginSessionApi().syncSessionSearchConfig(sessionId, searchConfig);
    } catch {
      // ignore
    }
  },

  setAutoRunning: (conversationId, running) => {
    set((state) => ({
      autoRunningByConversation: { ...state.autoRunningByConversation, [conversationId]: running },
    }));
  },

  fetchPluginUI: async (pluginId) => {
    const lang = i18n.language || "";
    const cacheKey = `${pluginId}:${lang}`;
    // Return cached value if already fetched for this language.
    const cached = get().pluginUIByPlugin[cacheKey];
    if (cached) return cached;
    try {
      const res = await PluginInfoApi().getPlugin(pluginId, {
        headers: lang ? { "Accept-Language": lang } : undefined,
      });
      const ui: PluginUI = res?.data?.data?.ui ?? res?.data?.ui ?? {};
      set((state) => ({
        pluginUIByPlugin: { ...state.pluginUIByPlugin, [cacheKey]: ui },
      }));
      return ui;
    } catch {
      return {};
    }
  },

  deleteSlotItem: async (sessionId, slotId, listIndex, orderVersion) => {
    await PluginSessionApi().deleteSlotItem(sessionId, slotId, listIndex, orderVersion);
  },

  patchSlotItemValue: async (sessionId, slotId, listIndex, value, contentType) => {
    await PluginSessionApi().patchSlotItem(sessionId, slotId, listIndex, value, contentType);
  },

  reorderSlotItems: async (sessionId, slotId, newSortOrderSeq, version) => {
    await PluginSessionApi().reorderSlotItems(sessionId, slotId, newSortOrderSeq, version);
  },

  getSlotVersions: async (sessionId, slotId, listIndex) => {
    const res = await PluginSessionApi().getSlotItemVersions(sessionId, slotId, listIndex);
    return res?.data?.data?.versions ?? [];
  },

  rollbackSlotItem: async (sessionId, slotId, listIndex, revision) => {
    await PluginSessionApi().rollbackSlotItem(sessionId, slotId, listIndex, revision);
  },

  createSlotItem: async (sessionId, slotId, value, caption, insertBefore, contentType) => {
    await PluginSessionApi().createSlotItem(sessionId, slotId, value, caption, insertBefore, contentType);
  },

  patchSlotCaption: async (sessionId, slotId, listIndex, caption) => {
    await PluginSessionApi().patchSlotCaption(sessionId, slotId, listIndex, caption);
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
