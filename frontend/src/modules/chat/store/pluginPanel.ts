import { create } from "zustand";
import { PluginInfoApi, PluginSessionApi } from "@/modules/chat/utils/request";

// ---------------------------------------------------------------------------
// DraftStore — two-layer draft management for slot text editing
// key format: `${sessionId}:${slotId}:${sortOrder}`
// ---------------------------------------------------------------------------

interface DraftEntry {
  value: Record<string, unknown>;
  timer: ReturnType<typeof setTimeout> | null;
}

const DRAFT_FLUSH_DELAY_MS = 60_000;
const DRAFT_LS_PREFIX = 'slotDraft:';

const _drafts = new Map<string, DraftEntry>();

function _draftKey(sessionId: string, slotId: string, sortOrder: number): string {
  return `${sessionId}:${slotId}:${sortOrder}`;
}

export const draftStore = {
  /** Write value to localStorage and reset the 60s auto-flush timer. */
  setDraft(sessionId: string, slotId: string, sortOrder: number, value: Record<string, unknown>) {
    const key = _draftKey(sessionId, slotId, sortOrder);
    const existing = _drafts.get(key);
    if (existing?.timer) clearTimeout(existing.timer);
    try {
      localStorage.setItem(DRAFT_LS_PREFIX + key, JSON.stringify(value));
    } catch { /* storage full — ignore */ }
    const timer = setTimeout(() => {
      draftStore.flushDraft(sessionId, slotId, sortOrder);
    }, DRAFT_FLUSH_DELAY_MS);
    _drafts.set(key, { value, timer });
  },

  /** Clear timer and call patchSlotItemValue to produce a human revision. Does NOT clear localStorage. */
  async flushDraft(sessionId: string, slotId: string, sortOrder: number): Promise<void> {
    const key = _draftKey(sessionId, slotId, sortOrder);
    const entry = _drafts.get(key);
    if (!entry) return;
    if (entry.timer) clearTimeout(entry.timer);
    _drafts.set(key, { value: entry.value, timer: null });
    try {
      await PluginSessionApi().patchSlotItem(sessionId, slotId, sortOrder, entry.value);
    } catch { /* best-effort — ignore */ }
    _drafts.delete(key);
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
      const sortOrder = Number(parts[2]);
      if (!slotId || isNaN(sortOrder)) continue;
      tasks.push(draftStore.flushDraft(sessionId, slotId, sortOrder));
    }
    await Promise.all(tasks);
  },

  /** Discard draft without producing a revision. Clears localStorage and timer. */
  cancelDraft(sessionId: string, slotId: string, sortOrder: number) {
    const key = _draftKey(sessionId, slotId, sortOrder);
    const existing = _drafts.get(key);
    if (existing?.timer) clearTimeout(existing.timer);
    _drafts.delete(key);
    try {
      localStorage.removeItem(DRAFT_LS_PREFIX + key);
    } catch { /* ignore */ }
  },

  /** Read a persisted draft from localStorage (for mount-time restore). */
  getLocalDraft(sessionId: string, slotId: string, sortOrder: number): Record<string, unknown> | null {
    const key = _draftKey(sessionId, slotId, sortOrder);
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
  artifact_key: string;
  step_id: string;
  attempt: number;
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
  created_at: string;
  updated_at: string;
  slots?: SlotRevision[];
  /** The tab currently focused by the user — forwarded to the AI in plugin_context. */
  focusedTab?: string;
  /** The sort_order item currently focused by the user — forwarded to the AI. */
  focusedSortOrder?: number;
}

// Slot value resolved from a TaskArtifact's value field.
export type SlotValue =
  | { type: "text"; text: string }
  | { type: "image"; url: string; mimeType?: string }
  | { type: "file"; url: string; name: string; size?: number }
  | { type: "unknown"; raw: unknown };

// UI tab/slot declaration from plugin.yaml.
export interface SlotDef {
  id: string;
  label: string;
  type: "image" | "text" | "file";
  cardinality?: "single" | "list";
  /** The artifact_key written by the SubAgent. If absent, falls back to id. */
  artifact_key?: string;
  /** Whether this list slot supports drag-reorder. */
  ordered?: boolean;
  /** The artifact_key used for the caption of this slot's items. */
  caption_key?: string;
  /** Maximum characters shown in the artifact summary injected into the AI prompt. */
  summary_max_chars?: number;
}

// composite_layout node types (recursive).
// A node is one of:
//   - string: slot_id
//   - CompositeColumnNode: { slot?: string | InnerTabsNode; weight?: number }
//   - InnerTabsNode: { tabs: CompositeLayoutNode[] }
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
  label: string;
  layout?: "grid" | "list" | "composite" | "horizontal";
  slots: SlotDef[];
  /** Only present when layout === "composite". Each element describes one column. */
  composite_layout?: CompositeLayoutNode[];
}

export interface PluginUI {
  tabs?: TabDef[];
}

export interface SlotOrderInfo {
  order_list: number[];
  order_version: number;
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
  // Slot order cache: keyed by "sessionId:slotId"
  slotOrderCache: Record<string, SlotOrderInfo>;

  setSession: (conversationId: string, session: PluginSession | null) => void;
  updateSlot: (conversationId: string, slot: SlotRevision) => void;
  loadActiveSession: (conversationId: string) => Promise<void>;
  refreshSlots: (conversationId: string, sessionId: string) => Promise<void>;
  patchSlot: (conversationId: string, sessionId: string, slotId: string, revision: number) => Promise<void>;
  advanceSession: (conversationId: string, sessionId: string) => Promise<void>;
  retrySession: (conversationId: string, sessionId: string) => Promise<void>;
  clearSession: (conversationId: string) => void;
  setAutoRunning: (conversationId: string, running: boolean) => void;
  fetchPluginUI: (pluginId: string) => Promise<PluginUI>;
  // Phase 3: slot item management.
  deleteSlotItem: (sessionId: string, slotId: string, sortOrder: number) => Promise<void>;
  patchSlotItemValue: (sessionId: string, slotId: string, sortOrder: number, value: any, contentType?: string) => Promise<void>;
  reorderSlotItems: (sessionId: string, slotId: string, newSortOrderSeq: number[], version: number) => Promise<void>;
  getSlotVersions: (sessionId: string, slotId: string, sortOrder: number) => Promise<SlotVersionEntry[]>;
  rollbackSlotItem: (sessionId: string, slotId: string, sortOrder: number, revision: number) => Promise<void>;
  loadSlotOrder: (sessionId: string, slotId: string) => Promise<SlotOrderInfo>;
  // Phase 4: new item creation and caption editing.
  createSlotItem: (sessionId: string, slotId: string, value: any, caption?: string, insertBefore?: number) => Promise<void>;
  patchSlotCaption: (sessionId: string, slotId: string, sortOrder: number, caption: string) => Promise<void>;
  // Track focused tab and sort_order for the AI.
  setFocusedTab: (conversationId: string, tabId: string) => void;
  setFocusedSortOrder: (conversationId: string, sortOrder: number | undefined) => void;
}

export const usePluginStore = create<PluginStore>()((set, get) => ({
  sessionByConversation: {},
  loadingByConversation: {},
  autoRunningByConversation: {},
  pluginUIByPlugin: {},
  slotOrderCache: {},

  setSession: (conversationId, session) => {
    set((state) => ({
      sessionByConversation: { ...state.sessionByConversation, [conversationId]: session },
    }));
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
    set((s) => ({
      loadingByConversation: { ...s.loadingByConversation, [conversationId]: true },
    }));
    try {
      const res = await PluginSessionApi().getLatestSession(conversationId);
      const session: PluginSession | null = res?.data?.data?.session ?? null;
      get().setSession(conversationId, session);
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

  advanceSession: async (conversationId, sessionId) => {
    try {
      await PluginSessionApi().advanceSession(sessionId, 'continue');
      get().loadActiveSession(conversationId);
    } catch {
      // ignore
    }
  },

  retrySession: async (conversationId, sessionId) => {
    try {
      await PluginSessionApi().advanceSession(sessionId, 'retry');
      get().loadActiveSession(conversationId);
    } catch {
      // ignore
    }
  },

  clearSession: (conversationId) => {
    set((state) => ({
      sessionByConversation: { ...state.sessionByConversation, [conversationId]: null },
    }));
  },

  setAutoRunning: (conversationId, running) => {
    set((state) => ({
      autoRunningByConversation: { ...state.autoRunningByConversation, [conversationId]: running },
    }));
  },

  fetchPluginUI: async (pluginId) => {
    // Return cached value if already fetched.
    const cached = get().pluginUIByPlugin[pluginId];
    if (cached) return cached;
    try {
      const res = await PluginInfoApi().getPlugin(pluginId);
      const ui: PluginUI = res?.data?.data?.ui ?? res?.data?.ui ?? {};
      set((state) => ({
        pluginUIByPlugin: { ...state.pluginUIByPlugin, [pluginId]: ui },
      }));
      return ui;
    } catch {
      return {};
    }
  },

  deleteSlotItem: async (sessionId, slotId, sortOrder) => {
    await PluginSessionApi().deleteSlotItem(sessionId, slotId, sortOrder);
  },

  patchSlotItemValue: async (sessionId, slotId, sortOrder, value, contentType) => {
    await PluginSessionApi().patchSlotItem(sessionId, slotId, sortOrder, value, contentType);
  },

  reorderSlotItems: async (sessionId, slotId, newSortOrderSeq, version) => {
    await PluginSessionApi().reorderSlotItems(sessionId, slotId, newSortOrderSeq, version);
    // Invalidate order cache.
    set((state) => {
      const key = `${sessionId}:${slotId}`;
      const cache = { ...state.slotOrderCache };
      delete cache[key];
      return { slotOrderCache: cache };
    });
  },

  getSlotVersions: async (sessionId, slotId, sortOrder) => {
    const res = await PluginSessionApi().getSlotItemVersions(sessionId, slotId, sortOrder);
    return res?.data?.data?.versions ?? [];
  },

  rollbackSlotItem: async (sessionId, slotId, sortOrder, revision) => {
    await PluginSessionApi().rollbackSlotItem(sessionId, slotId, sortOrder, revision);
  },

  createSlotItem: async (sessionId, slotId, value, caption, insertBefore) => {
    await PluginSessionApi().createSlotItem(sessionId, slotId, value, caption, insertBefore);
  },

  patchSlotCaption: async (sessionId, slotId, sortOrder, caption) => {
    await PluginSessionApi().patchSlotCaption(sessionId, slotId, sortOrder, caption);
  },

  loadSlotOrder: async (sessionId, slotId) => {
    const key = `${sessionId}:${slotId}`;
    const cached = get().slotOrderCache[key];
    if (cached) return cached;
    try {
      const res = await PluginSessionApi().getSlotOrder(sessionId, slotId);
      const info: SlotOrderInfo = {
        order_list: res?.data?.data?.order_list ?? [],
        order_version: res?.data?.data?.order_version ?? 0,
      };
      set((state) => ({ slotOrderCache: { ...state.slotOrderCache, [key]: info } }));
      return info;
    } catch {
      return { order_list: [], order_version: 0 };
    }
  },

  setFocusedTab: (conversationId, tabId) => {
    set((state) => {
      const session = state.sessionByConversation[conversationId];
      if (!session) return state;
      return {
        sessionByConversation: {
          ...state.sessionByConversation,
          [conversationId]: { ...session, focusedTab: tabId },
        },
      };
    });
  },

  setFocusedSortOrder: (conversationId, sortOrder) => {
    set((state) => {
      const session = state.sessionByConversation[conversationId];
      if (!session) return state;
      return {
        sessionByConversation: {
          ...state.sessionByConversation,
          [conversationId]: { ...session, focusedSortOrder: sortOrder },
        },
      };
    });
  },
}));
