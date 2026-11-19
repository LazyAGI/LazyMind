import { useCallback, useEffect, useState } from "react";
import { axiosInstance, BASE_URL } from "@/components/request";

export interface ProcessingTask {
  id: string; dataset_id: string; dataset_name: string; document_id: string; document_name: string;
  level: string; status: string; created_at: string; error?: string;
  stages: { name: string; status: string }[];
}
export interface ProcessingTaskPage {
  items: ProcessingTask[]; total: number; page: number; page_size: number;
  counts: Record<string, number>; unfinished_count: number;
}
export function useProcessingTasks(open: boolean, refreshKey: string) {
  const [filters, setFilters] = useState({ status: "all", level: "", q: "", page: 1 });
  const [data, setData] = useState<ProcessingTaskPage>();
  const [loading, setLoading] = useState(true);
  const [queryFailed, setQueryFailed] = useState(false);
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision(v => v + 1), []);
  useEffect(() => {
    let stopped = false;
    let request: AbortController | undefined;
    let timer: number | undefined;
    let queued = false;
    let initial = true;
    const poll = async () => {
      window.clearTimeout(timer);
      if (stopped || document.hidden) return;
      if (request) { queued = true; return; }
      const controller = new AbortController(); request = controller;
      if (initial) setLoading(true);
      let active = false;
      try {
        const result = await axiosInstance.get(`${BASE_URL}/api/core/knowledge-processing/tasks`, {
          params: open ? filters : { page: 1 }, signal: controller.signal, silentError: true,
        });
        const next: ProcessingTaskPage = result.data;
        if (!Array.isArray(next?.items) || !next.counts) throw new Error("Invalid task snapshot");
        if (stopped || controller.signal.aborted) return;
        setData(next); setQueryFailed(false); active = next.unfinished_count > 0;
      } catch {
        if (!stopped && !controller.signal.aborted) { setQueryFailed(true); active = true; }
      } finally {
        request = undefined; initial = false;
        if (!stopped) {
          setLoading(false);
          if (!document.hidden) {
            if (queued) { queued = false; void poll(); }
            else timer = window.setTimeout(poll, active && open ? 5000 : 15000);
          }
        }
      }
    };
    const visibility = () => { if (document.hidden) { window.clearTimeout(timer); request?.abort(); } else void poll(); };
    const focus = () => { void poll(); };
    document.addEventListener("visibilitychange", visibility); window.addEventListener("focus", focus);
    void poll();
    return () => { stopped = true; request?.abort(); window.clearTimeout(timer); document.removeEventListener("visibilitychange", visibility); window.removeEventListener("focus", focus); };
  }, [open, filters, refreshKey, revision]);
  return { data, filters, setFilters, loading, queryFailed, refresh };
}
export type ProcessingTasksQuery = ReturnType<typeof useProcessingTasks>;
