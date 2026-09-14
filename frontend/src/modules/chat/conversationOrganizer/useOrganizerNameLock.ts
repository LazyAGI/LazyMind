import { useEffect, useState } from "react";
import { CONVERSATION_GROUPS_CHANGED_EVENT, getLatestOrganizerState } from "./api";

// Only open editors/pickers subscribe; backend transactions remain authoritative.
export default function useOrganizerNameLock(enabled = true) {
  const [locked, setLocked] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    let disposed = false;
    const refresh = async () => {
      try {
        const state = await getLatestOrganizerState();
        if (!disposed) setLocked(Boolean(state.run && ["pending", "running", "applying"].includes(state.run.status)));
      } catch { /* Preserve the last state on disconnect. */ }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 15000);
    window.addEventListener("focus", refresh);
    window.addEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, refresh);
    return () => {
      disposed = true;
      window.clearInterval(timer);
      window.removeEventListener("focus", refresh);
      window.removeEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, refresh);
    };
  }, [enabled]);
  return locked;
}
