import { create } from "zustand";
import { PluginInfoApi, PluginSessionApi } from "@/modules/chat/utils/request";

export interface SlotRevision {
  slot_id: string;
  revision: number;
  list_index?: number;
  selected: boolean;
  artifact_key: string;
  step_id: string;
  attempt: number;
  created_at: string;
  /** Artifact content type returned by the backend (e.g. 'text', 'image', 'file'). */
  content_type?: string;
  /** Artifact value as returned by the backend — shape depends on content_type. */
  artifact_value?: any;
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
}

export interface TabDef {
  id: string;
  label: string;
  slots: SlotDef[];
}

export interface PluginUI {
  tabs?: TabDef[];
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
}

export const usePluginStore = create<PluginStore>()((set, get) => ({
  sessionByConversation: {},
  loadingByConversation: {},
  autoRunningByConversation: {},
  pluginUIByPlugin: {},

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
}));
