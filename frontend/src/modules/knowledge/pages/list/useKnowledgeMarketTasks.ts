import { useCallback, useEffect, useState } from "react";
import type { KnowledgeMarketTaskListOpenAPIResponse } from "@/api/generated/core-client";
import { listKnowledgeMarketTaskPage } from "@/modules/knowledge/api/knowledgeMarket";

// The entry badge and the open dialog consume the same server snapshot.
export function useKnowledgeMarketTasks(open: boolean, refreshKey: string) {
  const [page, setPage] = useState(1);
  const [revision, setRevision] = useState(0);
  const [data, setData] = useState<KnowledgeMarketTaskListOpenAPIResponse>();
  const [loading, setLoading] = useState(false);
  const [queryFailed, setQueryFailed] = useState(false);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);

  useEffect(() => {
    let stopped = false;
    let request: AbortController | undefined;
    let queued = false;
    let timer: number | undefined;
    let count: number | undefined;
    let initial = true;
    const poll = async () => {
      window.clearTimeout(timer);
      if (stopped || document.hidden) return;
      if (request) {
        queued = true;
        return;
      }
      const controller = new AbortController();
      request = controller;
      if (initial) setLoading(true);
      let failed = false;
      try {
        const next = await listKnowledgeMarketTaskPage(open ? page : 1, {
          signal: controller.signal,
          silentError: true,
        });
        if (stopped || controller.signal.aborted) return;
        count = next.unfinished_count;
        setData(next);
        setQueryFailed(false);
      } catch {
        if (stopped || controller.signal.aborted) return;
        failed = true;
        setQueryFailed(true);
      } finally {
        request = undefined;
        initial = false;
        if (!stopped) {
          setLoading(false);
          if (!document.hidden) {
            if (queued) {
              queued = false;
              void poll();
            } else {
              timer = window.setTimeout(poll, failed || (count ?? 0) > 0 ? 2000 : 15000);
            }
          }
        }
      }
    };
    const onVisibility = () => {
      if (document.hidden) {
        window.clearTimeout(timer);
        request?.abort();
      } else {
        void poll();
      }
    };
    const onFocus = () => { void poll(); };
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("focus", onFocus);
    void poll();
    return () => {
      stopped = true;
      request?.abort();
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("focus", onFocus);
    };
  }, [open, page, refreshKey, revision]);

  return { data, page, setPage, refresh, loading, queryFailed };
}

export type KnowledgeMarketTasksQuery = ReturnType<typeof useKnowledgeMarketTasks>;
