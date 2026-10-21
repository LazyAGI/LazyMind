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
  currentRef.current = current;
  const scope = useRef(signature);
  scope.current = signature;
  const mounted = useRef(true);
  const inFlight = useRef<{ signature: string; promise: Promise<boolean> }>();

  const refresh = useCallback(
    (pendingOnly = false): Promise<boolean> => {
      if (inFlight.current?.signature === signature)
        return inFlight.current.promise;
      const targets = pendingOnly
        ? documents.filter(([key]) => needsRefresh(currentRef.current[key]))
        : documents;
      if (!targets.length) return Promise.resolve(false);
      setRefreshing(true);
      const promise: Promise<boolean> = Promise.all(
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
          if (!mounted.current || scope.current !== signature) return false;
          const next = {
            ...currentRef.current,
            ...Object.fromEntries(entries),
          };
          currentRef.current = next;
          inFlight.current = undefined;
          setRefreshing(false);
          setCurrent(next);
          return documents.some(([key]) => needsRefresh(next[key]));
        })
        .finally(() => {
          if (inFlight.current?.promise === promise)
            inFlight.current = undefined;
          if (mounted.current && scope.current === signature)
            setRefreshing(false);
        });
      inFlight.current = { signature, promise };
      return promise;
    },
    [documents, signature],
  );

  useEffect(() => {
    mounted.current = true;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async (pendingOnly: boolean) => {
      if (disposed || document.hidden) return;
      const active = await refresh(pendingOnly);
      if (active && !disposed) timer = setTimeout(() => void poll(true), 3000);
    };
    const visible = () => {
      clearTimeout(timer);
      if (!document.hidden) void poll(false);
    };
    void poll(false);
    document.addEventListener("visibilitychange", visible);
    return () => {
      disposed = true;
      mounted.current = false;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [refresh]);

  return { current, refreshing, refresh };
}
