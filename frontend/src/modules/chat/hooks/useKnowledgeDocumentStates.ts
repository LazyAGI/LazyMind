import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { KnowledgeOperation } from "../utils/knowledgeOperations";

type DocumentRef = { knowledge_base_id: string; document_id?: string };
export type KnowledgeDocumentState = {
  status: string;
  reason?: string;
  unavailable?: boolean;
};
export const knowledgeDocumentKey = (ref: DocumentRef) =>
  JSON.stringify([ref.knowledge_base_id, ref.document_id]);
const needsRefresh = (state?: KnowledgeDocumentState) =>
  !state ||
  state.unavailable ||
  ["pending", "running", "submitted", "not_submitted", "unknown"].includes(
    state.status,
  );

export function useKnowledgeDocumentStates(operations: KnowledgeOperation[]) {
  const refs = new Map<string, DocumentRef>();
  operations.forEach((operation) =>
    operation.items.forEach((item) => {
      const ref = item.resource_ref;
      if (
        ref?.kind === "document" &&
        ref.document_id &&
        item.resource_status !== "deleted"
      ) {
        refs.set(knowledgeDocumentKey(ref), ref);
      }
    }),
  );
  const signature = JSON.stringify(
    [...refs].sort(([a], [b]) => a.localeCompare(b)),
  );
  const documents = useMemo(
    () => JSON.parse(signature) as [string, DocumentRef][],
    [signature],
  );
  const [current, setCurrent] = useState<
    Record<string, KnowledgeDocumentState>
  >({});
  const [refreshing, setRefreshing] = useState(false);
  const currentRef = useRef(current);
  const refreshRef = useRef<() => Promise<void>>(() => Promise.resolve());
  const refresh = useCallback(() => refreshRef.current(), []);

  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let inFlight: Promise<void> | undefined;
    let queuedRefresh: Promise<void> | undefined;

    function schedule() {
      clearTimeout(timer);
      if (
        !disposed &&
        !document.hidden &&
        documents.some(([key]) => needsRefresh(currentRef.current[key]))
      ) {
        timer = setTimeout(() => void query(true), 3000);
      }
    }

    function query(pendingOnly = false): Promise<void> {
      if (disposed || (pendingOnly && document.hidden)) return Promise.resolve();
      if (inFlight) {
        if (pendingOnly) return inFlight;
        // A mutation may have happened after this request began. All explicit
        // refreshes during that request share one subsequent full query.
        queuedRefresh ??= inFlight.then(() => {
          queuedRefresh = undefined;
          return query();
        });
        return queuedRefresh;
      }
      const targets = pendingOnly
        ? documents.filter(([key]) => needsRefresh(currentRef.current[key]))
        : documents;
      if (!targets.length) return Promise.resolve();
      clearTimeout(timer);
      setRefreshing(true);
      inFlight = Promise.all(
        targets.map(async ([key, ref]) => {
          try {
            const response = await axiosInstance.get(
              `${BASE_URL}/api/core/datasets/${encodeURIComponent(ref.knowledge_base_id)}/documents/${encodeURIComponent(ref.document_id!)}`,
              { silentError: true } as never,
            );
            const document = response.data.data || response.data;
            return [
              key,
              {
                status: document.processing_error
                  ? "failed"
                  : document.processing_status || "unknown",
                reason: document.processing_error?.message,
              },
            ] as const;
          } catch {
            const previous = currentRef.current[key];
            return [
              key,
              {
                ...previous,
                status: previous?.status || "unknown",
                unavailable: true,
              },
            ] as const;
          }
        }),
      )
        .then((entries) => {
          if (disposed) return;
          const next = {
            ...currentRef.current,
            ...Object.fromEntries(entries),
          };
          currentRef.current = next;
          setCurrent(next);
        })
        .finally(() => {
          inFlight = undefined;
          if (!disposed && !queuedRefresh) {
            setRefreshing(false);
            schedule();
          }
        });
      return inFlight;
    }

    refreshRef.current = () => query();
    const visible = () => {
      clearTimeout(timer);
      if (!document.hidden) void query();
    };
    setRefreshing(false);
    if (!document.hidden) void query();
    document.addEventListener("visibilitychange", visible);
    return () => {
      disposed = true;
      clearTimeout(timer);
      refreshRef.current = () => Promise.resolve();
      document.removeEventListener("visibilitychange", visible);
    };
  }, [documents]);

  return { current, refreshing, refresh };
}
