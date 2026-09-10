import { useEffect } from "react";
import { create } from "zustand";
import type { RawAxiosRequestConfig } from "axios";
import type { ConversationBatchStatusResponse, ConversationRunningStatusItem } from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import { CHAT_CONVERSATION_ACTIVITY_EVENT } from "@/modules/chat/constants/chat";
import { CONVERSATION_STATUS_REFRESH_EVENT } from "@/modules/chat/utils/conversationStatusEvents";

type StatusEntry = { status: ConversationRunningStatusItem["status"]; confirmedAt: number };
interface RunningState {
  entries: Record<string, StatusEntry>;
  watchers: Record<string, string[]>;
  watch: (key: string, ids: string[]) => void;
  unwatch: (key: string) => void;
}

export const useConversationRunningStore = create<RunningState>((set) => ({
  entries: {},
  watchers: {},
  watch: (key, ids) => set((state) => {
    const next = new Set(ids.filter(Boolean));
    const previous = state.watchers[key] ?? [];
    if (previous.length === next.size && previous.every((id) => next.has(id))) return state;
    return { watchers: { ...state.watchers, [key]: [...next] } };
  }),
  unwatch: (key) => set((state) => {
    if (!(key in state.watchers)) return state;
    const watchers = { ...state.watchers };
    delete watchers[key];
    return { watchers };
  }),
}));

const POLL_MS = 5_000;
const STALE_MS = 30_000;
const BATCH_SIZE = 100;

// One coordinator belongs to the authenticated layout, not to individual rows
// or the current conversation page. No chat streams are opened here.
export function startConversationRunningSync() {
  const store = useConversationRunningStore;
  let disposed = false;
  let inFlight = false;
  let revision = 0;
  let failures = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let staleTimer: ReturnType<typeof setTimeout> | undefined;
  let controller: AbortController | undefined;
  const pending = new Set<string>();
  const canQuery = () => document.visibilityState !== "hidden" && navigator.onLine;
  const watchedIDs = () => new Set(Object.values(store.getState().watchers).flat());

  function markUnavailable(ids: string[]) {
    const entries = { ...store.getState().entries };
    const now = Date.now();
    for (const id of ids) {
      if (!entries[id] || now - entries[id].confirmedAt >= STALE_MS) {
        entries[id] = { status: "unknown", confirmedAt: entries[id]?.confirmedAt ?? 0 };
      }
    }
    store.setState({ entries });
    clearTimeout(staleTimer);
    const deadlines = Object.values(entries).filter((entry) => entry.status !== "unknown")
      .map((entry) => entry.confirmedAt + STALE_MS - now).filter((delay) => delay > 0);
    if (deadlines.length) {
      staleTimer = setTimeout(() => markUnavailable(Object.keys(store.getState().entries)), Math.min(...deadlines));
    }
  }

  function schedule(delay = POLL_MS) {
    clearTimeout(timer);
    if (!disposed && canQuery()) timer = setTimeout(() => void refresh(), delay);
  }

  function invalidate(ids: string[] = []) {
    ids.forEach((id) => pending.add(id));
    revision++;
    if (!inFlight) schedule(100);
  }

  async function refresh() {
    if (disposed || inFlight || !canQuery()) return;
    inFlight = true;
    const watched = watchedIDs();
    const entries = store.getState().entries;
    const ids = [...new Set([...watched, ...pending, ...Object.keys(entries).filter((id) => entries[id].status !== "idle")])];
    pending.clear();
    const requestRevision = revision;
    controller = new AbortController();
    let failed = false;
    try {
      for (let offset = 0; offset < ids.length; offset += BATCH_SIZE) {
        const batch = ids.slice(offset, offset + BATCH_SIZE);
        try {
          const config: RawAxiosRequestConfig & { silentError: boolean } = {
            signal: controller.signal, timeout: 10_000, silentError: true,
          };
          const response = await axiosInstance.post<ConversationBatchStatusResponse>(
            `${BASE_URL}/api/core/conversations:batchStatus`, { conversation_ids: batch }, config,
          );
          if (disposed || controller.signal.aborted) return;
          if (revision !== requestRevision) { batch.forEach((id) => pending.add(id)); continue; }
          if (!Array.isArray(response.data.statuses)) throw new Error("Invalid conversation status response");
          const current = { ...store.getState().entries };
          // Missing IDs are no longer accessible; remove stale local state.
          batch.forEach((id) => delete current[id]);
          for (const item of response.data.statuses) {
            if (!batch.includes(item.conversation_id)) continue;
            current[item.conversation_id] = {
              status: ["running", "idle", "unknown"].includes(item.status) ? item.status : "unknown",
              confirmedAt: Date.now(),
            };
          }
          store.setState({ entries: current });
        } catch {
          if (disposed || controller.signal.aborted) return;
          failed = true;
          if (revision === requestRevision) markUnavailable(batch);
          else batch.forEach((id) => pending.add(id));
        }
      }
      failures = failed ? failures + 1 : 0;
      const entries = store.getState().entries;
      const watching = watchedIDs();
      const obsolete = Object.keys(entries).filter((id) => entries[id].status === "idle" && !watching.has(id));
      if (obsolete.length) {
        const current = { ...entries };
        obsolete.forEach((id) => delete current[id]);
        store.setState({ entries: current });
      }
    } finally {
      if (controller.signal.aborted) ids.forEach((id) => pending.add(id));
      inFlight = false;
      if (!disposed) schedule(revision !== requestRevision ? 100 : failures ? [5_000, 10_000, 30_000][Math.min(failures - 1, 2)] : POLL_MS);
    }
  }

  const unsubscribe = store.subscribe((next, previous) => {
    if (next.watchers !== previous.watchers) invalidate();
  });
  const onActivity = (event: Event) => {
    const id = (event as CustomEvent<{ conversationId?: string }>).detail?.conversationId;
    if (id && !id.startsWith("temp_")) invalidate([id]);
  };
  const onVisibility = () => {
    if (canQuery()) { failures = 0; invalidate(); }
    else {
      clearTimeout(timer);
      if (!navigator.onLine) {
        controller?.abort();
        markUnavailable(Object.keys(store.getState().entries));
      }
    }
  };
  window.addEventListener(CONVERSATION_STATUS_REFRESH_EVENT, onActivity);
  window.addEventListener(CHAT_CONVERSATION_ACTIVITY_EVENT, onActivity);
  window.addEventListener("online", onVisibility);
  window.addEventListener("offline", onVisibility);
  document.addEventListener("visibilitychange", onVisibility);
  schedule(0);
  return () => {
    disposed = true;
    controller?.abort();
    clearTimeout(timer);
    clearTimeout(staleTimer);
    unsubscribe();
    window.removeEventListener(CONVERSATION_STATUS_REFRESH_EVENT, onActivity);
    window.removeEventListener(CHAT_CONVERSATION_ACTIVITY_EVENT, onActivity);
    window.removeEventListener("online", onVisibility);
    window.removeEventListener("offline", onVisibility);
    document.removeEventListener("visibilitychange", onVisibility);
    store.setState({ entries: {} });
  };
}

export function useConversationRunningSync(userScope: string, conversationId = "") {
  useEffect(() => {
    useConversationRunningStore.getState().watch("current-route", conversationId ? [conversationId] : []);
    return () => useConversationRunningStore.getState().unwatch("current-route");
  }, [conversationId, userScope]);
  useEffect(() => {
    if (userScope) return startConversationRunningSync();
  }, [userScope]);
}
