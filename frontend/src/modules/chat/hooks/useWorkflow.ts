import { useCallback, useEffect } from 'react';
import { useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';

/**
 * useWorkflowSession returns the active workflow session and helpers for the given conversationId.
 * It loads the session on mount and keeps slots refreshed.
 */
export function useWorkflowSession(conversationId: string) {
  const session = useWorkflowStore((s) => s.sessionByConversation[conversationId] ?? null);
  const loading = useWorkflowStore((s) => s.loadingByConversation[conversationId] ?? false);
  const loadActiveSession = useWorkflowStore((s) => s.loadActiveSession);
  const patchSlot = useWorkflowStore((s) => s.patchSlot);
  const subscribeWorkflowSession = useWorkflowStore((s) => s.subscribeWorkflowSession);

  useEffect(() => {
    loadActiveSession(conversationId);
  }, [conversationId, loadActiveSession]);

  useEffect(() => {
    if (!session?.session_id) return;
    return subscribeWorkflowSession(conversationId, session.session_id);
  }, [conversationId, session?.session_id, subscribeWorkflowSession]);

  // Use loadActiveSession so we always get the latest session status (not just slots).
  // This is important for detecting when the session transitions from 'active' to
  // 'waiting'/'completed' even if the SSE push event was missed.
  const refresh = useCallback(() => {
    loadActiveSession(conversationId, { silentError: true });
  }, [conversationId, loadActiveSession]);

  const selectRevision = useCallback(
    (slotId: string, revision: number) => {
      if (session?.session_id) {
        patchSlot(conversationId, session.session_id, slotId, revision);
      }
    },
    [conversationId, session?.session_id, patchSlot],
  );

  return { session, loading, refresh, selectRevision };
}

/**
 * useSlot returns the currently-selected revision(s) for a given slot_id.
 * For cardinality=single returns a single SlotRevision or null.
 * For cardinality=list returns the full array sorted by list_index.
 */
export function useSlot(conversationId: string, slotId: string): SlotRevision[] {
  const session = useWorkflowStore((s) => s.sessionByConversation[conversationId] ?? null);
  if (!session?.slots) return [];
  return session.slots
    .filter((s) => s.slot_id === slotId && s.selected)
    .sort((a, b) => (a.list_index ?? 0) - (b.list_index ?? 0));
}
