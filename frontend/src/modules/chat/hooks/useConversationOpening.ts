import { useEffect } from "react";
import { CHAT_CONVERSATION_ACTIVITY_EVENT, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from "../constants/chat";
import { conversationOpeningState } from "../utils/request";

// One poller per signed-in layout. Metadata summaries never enter frontend state.
export function useConversationOpening(userKey: string, refresh: () => void) {
  useEffect(() => {
    if (!userKey) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let revision: number | undefined;
    let inFlight = false;
    let started = false;
    function schedule() {
      clearTimeout(timer);
      if (!document.hidden && !controller.signal.aborted) timer = setTimeout(poll, 5000);
    }
    async function poll() {
      if (document.hidden || controller.signal.aborted || inFlight) return;
      inFlight = true;
      try {
        const state = await conversationOpeningState(started ? undefined : "start", controller.signal);
        started = true;
        if (revision !== state.revision && (revision !== undefined || state.revision > 0)) refresh();
        revision = state.revision;
        if (state.pending > 0 || state.batch.status === "running") schedule();
      } catch {
        // A transient network failure must not abandon a persisted backfill.
        schedule();
      } finally {
        inFlight = false;
      }
    }
    function visibilityChanged() {
      clearTimeout(timer);
      if (!document.hidden) void poll();
    }
    void poll();
    window.addEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, schedule);
    window.addEventListener(CHAT_CONVERSATION_ACTIVITY_EVENT, schedule);
    document.addEventListener("visibilitychange", visibilityChanged);
    return () => {
      controller.abort();
      clearTimeout(timer);
      window.removeEventListener(CHAT_CONVERSATION_LIST_REFRESH_EVENT, schedule);
      window.removeEventListener(CHAT_CONVERSATION_ACTIVITY_EVENT, schedule);
      document.removeEventListener("visibilitychange", visibilityChanged);
    };
  }, [userKey, refresh]);
}
