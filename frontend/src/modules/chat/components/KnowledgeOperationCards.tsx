import { useTranslation } from "react-i18next";
import { tr } from "./SourceImports/translations";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button, Modal, message } from "antd";
import { axiosInstance, BASE_URL } from "@/components/request";
import { Link } from "react-router-dom";
import { v4 as uuid } from "uuid";
import { knowledgeResourceHref, type KnowledgeOperation } from "../utils/knowledgeOperations";
import type { ImportItem } from "./SourceImports/api";

type CurrentState = { status: string; reason?: string; unavailable?: boolean };
const processingLabels = (): Record<string, string> => ({
  submitted: tr("parsingSubmitted"), pending: tr("pending"), running: tr("running"),
  ready: tr("added"), failed: tr("parseFailed"), unknown: tr("unknown"),
  not_requested: tr("addedOriginal"), not_submitted: tr("pending"),
});
const receiptLabels = (): Record<string, string> => ({
  created: tr("created"), uploaded: tr("added"), moved: tr("moved"), deleted: tr("deleted"),
  updated: tr("updated"), failed: tr("failed"), unconfirmed: tr("unconfirmed"), skipped: tr("skipped"),
});
export function importStatusLabel(item: ImportItem) {
  const stages: Record<string, string> = {
    download: tr("download"), processing: tr("parseIndexStage"), import: tr("saveOriginal"),
    parse: tr("parse"), chunk: tr("chunk"), index: tr("index"),
  };
  const stage = stages[item.stage];
  if (item.status === "exists") {
    const suffixes: Record<string, string> = {
      failed: tr("parseFailedSuffix"), not_requested: tr("originalSuffix"), ready: tr("readySuffix"),
    };
    const suffix = suffixes[item.processing_status || ""]
      ?? ` · ${processingLabels()[item.processing_status || "unknown"] || tr("unknown")}`;
    return tr("existsStatus", { v0: suffix });
  }
  if (item.status === "added") return item.processing_status === "not_requested" ? tr("addedOriginal") : tr("added");
  if (item.status === "failed") return tr("failedStatus", { v0: stage ? ` · ${stage}` : "" });
  if (item.status === "running") return tr("runningStatus", { v0: stage ? ` · ${stage}` : "" });
  if (item.status === "pending") return tr("pending");
  return tr("unknown");
}

// Mounted only while the result panel is open. Receipts remain immutable;
// current document state is refreshed independently and never inferred from upload success.
export default function KnowledgeOperationCards({ operations = [], imports = [], onRetry, retrying, onRefreshImports }: { operations?: KnowledgeOperation[]; imports?: ImportItem[]; onRetry?: (rows: ImportItem[]) => void; retrying?: boolean; onRefreshImports?: () => Promise<void> }) {
  useTranslation();
  const [current, setCurrent] = useState<Record<string, CurrentState>>({});
  const currentRef = useRef(current); currentRef.current = current;
  const [refreshing, setRefreshing] = useState(false);
  const [manualRefreshing, setManualRefreshing] = useState(false);
  const signature = JSON.stringify(operations);
  const stableOperations = useMemo(() => operations, [signature]);
  const refresh = useCallback(async () => {
    setRefreshing(true);
    const entries = await Promise.all(stableOperations.flatMap(operation => operation.items.map(async (item, index) => {
      const ref = item.resource_ref;
      const key = `${operation.call_id}:${index}`;
      if (ref?.kind !== "document" || !ref.document_id || item.resource_status === "deleted") return undefined;
      try {
        const response = await axiosInstance.get(`${BASE_URL}/api/core/datasets/${encodeURIComponent(ref.knowledge_base_id)}/documents/${encodeURIComponent(ref.document_id)}`, { silentError: true } as never);
        const document = response.data.data || response.data;
        return [key, { status: document.processing_error ? "failed" : document.processing_status || "unknown", reason: document.processing_error?.message }] as const;
      } catch { return [key, { ...currentRef.current[key], status: currentRef.current[key]?.status || "unknown", unavailable: true }] as const; }
    })));
    const next: Record<string, CurrentState> = Object.fromEntries(entries.filter((entry): entry is NonNullable<typeof entry> => Boolean(entry)));
    setCurrent(previous => ({ ...previous, ...next })); setRefreshing(false);
    return Object.values(next).some(row => row.unavailable || ["pending", "running", "submitted", "not_submitted", "unknown"].includes(row.status));
  }, [stableOperations]);
  useEffect(() => {
    let disposed = false; let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => { if (disposed || document.hidden) return; const active = await refresh(); if (active && !disposed) timer = setTimeout(poll, 3000); };
    const visible = () => { clearTimeout(timer); if (!document.hidden) void poll(); };
    void poll(); document.addEventListener("visibilitychange", visible);
    return () => { disposed = true; clearTimeout(timer); document.removeEventListener("visibilitychange", visible); };
  }, [refresh]);
  type Receipt = { operation: KnowledgeOperation; item: KnowledgeOperation["items"][number]; key: string };
  const groups = new Map<string, { name: string; created: boolean; receipts: Receipt[]; imports: ImportItem[] }>();
  const group = (id: string) => { if (!groups.has(id)) groups.set(id, { name: tr("library"), created: false, receipts: [], imports: [] }); return groups.get(id)!; };
  operations.forEach(operation => operation.items.forEach((item, index) => {
    const entry = group(item.resource_ref?.knowledge_base_id || "");
    if (item.resource_ref?.kind === "knowledge_base") { entry.name = item.name; entry.created ||= item.resource_status === "created"; }
    entry.receipts.push({ operation, item, key: `${operation.call_id}:${index}` });
  }));
  imports.forEach(item => { const entry = group(item.target_dataset_id); entry.name = item.target_name || entry.name; entry.created ||= item.created_library; entry.imports.push(item); });
  const grouped = groups.size > 1;
  const canRefresh = operations.some(operation => operation.items.some(item => item.resource_ref?.kind === "document" && item.resource_ref.document_id && item.resource_status !== "deleted")) || (imports.length > 0 && Boolean(onRefreshImports));
  return <div aria-live="polite" className="knowledge-operation-results execution-results-body">
    {canRefresh && <Button size="small" aria-label={tr("refreshStatus")} loading={refreshing || manualRefreshing} onClick={async () => {
      setManualRefreshing(true);
      try { await Promise.allSettled([refresh(), onRefreshImports?.()]); }
      finally { setManualRefreshing(false); }
    }}>{tr("refreshStatus")}</Button>}
    {[...groups].map(([id, entry]) => {
      const papers = [...new Map(entry.imports.map(item => [item.paper_key || item.item_id, item])).values()];
      return <section className="execution-results-group" key={id}>
      {grouped ? <h3>{id ? <Link to={`/lib/knowledge/detail/${encodeURIComponent(id)}`}>{entry.name}</Link> : entry.name}{entry.created && <small>{tr("createdHere")}</small>}</h3> : entry.created && <div className="execution-result-row">{tr("createdLibraryPrefix")}{id ? <Link to={`/lib/knowledge/detail/${encodeURIComponent(id)}`}>{entry.name}</Link> : entry.name}</div>}
      {entry.receipts.filter(({ item }) => !(item.resource_ref?.kind === "knowledge_base" && item.resource_status === "created")).map(({ operation, item, key }) => {
        const href = operation.action === "knowledge.resource.delete" ? undefined : knowledgeResourceHref(item);
        const state = current[key];
        return <div className="execution-result-row" key={key}>
          {href ? <Link to={href}>{item.name}</Link> : item.name}
          <small>{tr("receiptPrefix")}{receiptLabels()[item.resource_status] || item.resource_status}{item.processing_status && ` · ${processingLabels()[item.processing_status] || item.processing_status}`}</small>
          {state && <small>{tr("currentStatusPrefix")}{processingLabels()[state.status] || state.status}{state.unavailable && tr("keepLastSuffix")}</small>}
          {state?.reason && <small className="result-error">{state.reason}</small>}
          {item.error?.message && <small className="result-error">{item.error.message}</small>}
          {item.cleanup_status && !["completed", "not_requested"].includes(item.cleanup_status) && <small>{tr("cleanupPrefix")}{item.cleanup_status}</small>}
          {state?.status === "failed" && item.resource_ref?.document_id && <Button size="small" onClick={async () => {
            try { await axiosInstance.post(`${BASE_URL}/api/core/datasets/${encodeURIComponent(id)}/documents/${encodeURIComponent(item.resource_ref.document_id!) }:reparse`, {}, { headers: { "Idempotency-Key": uuid() } }); await refresh(); }
            catch { message.error(tr("retryFailed")); }
          }}>{tr("retryParsing")}</Button>}
        </div>;
      })}
      {papers.length > 0 && <div className="execution-result-summary">{tr("addedPrefix")}{papers.filter(row => row.status === "added").length}{tr("existsPrefix")}{papers.filter(row => row.status === "exists").length}{tr("failedPrefix")}{papers.filter(row => row.status === "failed").length}</div>}
      {entry.imports.map(item => <div className="execution-result-row" key={item.item_id}>
        {item.document_id ? <Link to={`/lib/knowledge/knowledge/${encodeURIComponent(id)}/${encodeURIComponent(item.document_id)}`}>{item.title}</Link> : item.title}
        <small>{tr("importAccepted")}</small><small>{tr("currentStatusPrefix")}{importStatusLabel(item)}{item.state_unavailable && tr("unavailableSuffix")}</small>
        {item.error_message && <small className={item.status === "failed" ? "result-error" : ""}>{item.error_message}</small>}
        {(item.status === "failed" || item.processing_status === "failed") && <Button type="link" size="small" onClick={() => Modal.error({ title: tr("importFailed"), content: `${item.title}：${item.error_message || tr("processingFailed")}` })}>{tr("failureReason")}</Button>}
        {item.retryable && <Button size="small" loading={retrying} onClick={() => onRetry?.([item])}>{tr("retry")}</Button>}
      </div>)}
    </section>; })}
  </div>;
}
