import { useCallback, useEffect } from "react";
import { usePluginStore, type SlotRevision } from "@/modules/chat/store/pluginPanel";

/**
 * usePluginSession returns the active plugin session and helpers for the given conversationId.
 * It loads the session on mount and keeps slots refreshed.
 */
export function usePluginSession(conversationId: string) {
  const session = usePluginStore((s) => s.sessionByConversation[conversationId] ?? null);
  const loading = usePluginStore((s) => s.loadingByConversation[conversationId] ?? false);
  const loadActiveSession = usePluginStore((s) => s.loadActiveSession);
  const refreshSlots = usePluginStore((s) => s.refreshSlots);
  const patchSlot = usePluginStore((s) => s.patchSlot);
  const advanceSession = usePluginStore((s) => s.advanceSession);
  const retrySession = usePluginStore((s) => s.retrySession);

  useEffect(() => {
    loadActiveSession(conversationId);
  }, [conversationId, loadActiveSession]);

  const refresh = useCallback(() => {
    if (session?.session_id) {
      refreshSlots(conversationId, session.session_id);
    }
  }, [conversationId, session?.session_id, refreshSlots]);

  const selectRevision = useCallback(
    (slotId: string, revision: number) => {
      if (session?.session_id) {
        patchSlot(conversationId, session.session_id, slotId, revision);
      }
    },
    [conversationId, session?.session_id, patchSlot],
  );

  const advance = useCallback(() => {
    if (session?.session_id) {
      advanceSession(conversationId, session.session_id);
    }
  }, [conversationId, session?.session_id, advanceSession]);

  const retry = useCallback(() => {
    if (session?.session_id) {
      retrySession(conversationId, session.session_id);
    }
  }, [conversationId, session?.session_id, retrySession]);

  return { session, loading, refresh, selectRevision, advance, retry };
}

/**
 * useSlot returns the currently-selected revision(s) for a given slot_id.
 * For cardinality=single returns a single SlotRevision or null.
 * For cardinality=list returns the full array sorted by list_index.
 */
export function useSlot(conversationId: string, slotId: string): SlotRevision[] {
  const session = usePluginStore((s) => s.sessionByConversation[conversationId] ?? null);
  if (!session?.slots) return [];
  return session.slots
    .filter((s) => s.slot_id === slotId && s.selected)
    .sort((a, b) => (a.list_index ?? 0) - (b.list_index ?? 0));
}
