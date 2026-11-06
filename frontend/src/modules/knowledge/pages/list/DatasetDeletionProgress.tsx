import type { DatasetDeletionProgress } from "@/api/generated/core-client";
import { useCallback, useEffect, useState } from "react";
import { Alert, Progress } from "antd";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import { axiosInstance } from "@/components/request";
import { coreApiUrl } from "@/runtime/apiBase";
import { getImportErrorMessage } from "@/modules/knowledge/utils/importError";

type Deletion = DatasetDeletionProgress & {
  status: "cleaning" | "completed" | "failed";
  queryError?: string;
};

function readDeletions(storageKey: string): Deletion[] {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) || "[]");
    return Array.isArray(saved) ? saved.filter((item) => typeof item.dataset_id === "string" && ["cleaning", "completed", "failed"].includes(item.status)) : [];
  } catch { return []; }
}

export function useDatasetDeletions() {
  const { t } = useTranslation();
  const user = AgentAppsAuth.getUserInfo();
  const storageKey = `knowledge-deletions:${user?.tenantId || user?.tenant_id || user?.tenantKey || user?.tenant_key || "default"}:${user?.userId || user?.username || "anonymous"}`;
  const [state, setState] = useState(() => ({ key: storageKey, items: readDeletions(storageKey) }));
  const deletions = state.key === storageKey ? state.items : [];
  const setDeletions = useCallback((update: (items: Deletion[]) => Deletion[]) => {
    setState((current) => current.key === storageKey ? { ...current, items: update(current.items) } : current);
  }, [storageKey]);
  useEffect(() => { setState({ key: storageKey, items: readDeletions(storageKey) }); }, [storageKey]);
  useEffect(() => {
    if (state.key === storageKey) {
      try { localStorage.setItem(storageKey, JSON.stringify(state.items)); } catch { /* Keep tracking in memory when storage is unavailable. */ }
    }
  }, [storageKey, state]);
  const pending = deletions.filter((item) => item.status === "cleaning").map((item) => item.dataset_id).join(",");
  useEffect(() => {
    if (!pending) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      await Promise.all(pending.split(",").map(async (id) => {
        try {
          const { data } = await axiosInstance.get<Deletion>(coreApiUrl(`datasets/${encodeURIComponent(id)}/deletion`), { silentError: true } as never);
          if (data?.dataset_id !== id || !["cleaning", "completed", "failed"].includes(data.status) || !Number.isFinite(data.total) || !Number.isFinite(data.completed)) {
            throw new Error(t("knowledge.cleanupQueryFailed"));
          }
          if (!cancelled) setDeletions((items) => items.map((item) => item.dataset_id === id ? { ...item, ...data, queryError: undefined } : item));
        } catch (error) {
          if (!cancelled) setDeletions((items) => items.map((item) => item.dataset_id === id ? { ...item, queryError: getImportErrorMessage(error, t("knowledge.cleanupQueryFailed")) } : item));
        }
      }));
      if (!cancelled) timer = setTimeout(poll, 3000);
    };
    void poll();
    return () => { cancelled = true; clearTimeout(timer); };
  }, [storageKey, pending, setDeletions, t]);
  return {
    deletions,
    trackDeletion: useCallback((datasetId: string, name: string) => setDeletions((items) => [
      ...items.filter((item) => item.dataset_id !== datasetId),
      { dataset_id: datasetId, display_name: name, status: "cleaning", total: 0, completed: 0, failed: 0 },
    ]), [setDeletions]),
    dismissDeletion: (datasetId: string) => setDeletions((items) => items.filter((item) => item.dataset_id !== datasetId)),
  };
}

export function DatasetDeletionProgressList({ deletions, onDismiss }: { deletions: Deletion[]; onDismiss: (id: string) => void }) {
  const { t } = useTranslation();
  return <>{deletions.map((item) => <Alert key={item.dataset_id} showIcon style={{ marginBottom: 12 }}
    type={item.queryError || item.status === "failed" ? "error" : item.status === "completed" ? "success" : "info"}
    message={t(item.status === "completed" ? "knowledge.cleanupCompleted" : item.status === "failed" ? "knowledge.cleanupFailed" : "knowledge.cleanupInProgress")}
    description={<>
      <div>{item.display_name}</div>
      <div>{item.queryError || (item.status === "failed" ? t(item.failed > 0 ? "knowledge.cleanupFailureDetails" : "knowledge.cleanupSubmissionFailed", { count: item.failed }) : t("knowledge.cleanupProgress", { completed: item.completed, total: item.total }))}</div>
      {item.total > 0 && <Progress size="small" percent={Math.floor(item.completed * 100 / item.total)} status={item.status === "failed" ? "exception" : item.status === "completed" ? "success" : "active"} />}
    </>}
    closable={item.status !== "cleaning"} onClose={() => onDismiss(item.dataset_id)} />)}</>;
}
