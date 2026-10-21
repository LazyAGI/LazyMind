import { useState } from "react";
import { Button } from "antd";
import { axiosInstance, BASE_URL } from "@/components/request";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { knowledgeResourceHref, type KnowledgeOperation } from "../utils/knowledgeOperations";

export default function KnowledgeOperationCards({ operations = [] }: { operations?: KnowledgeOperation[] }) {
  const { i18n } = useTranslation();
  const [current, setCurrent] = useState<Record<string, { status: string; reason?: string }>>({});
  const [refreshing, setRefreshing] = useState<string>();
  const refresh = async (operation: KnowledgeOperation) => {
    setRefreshing(operation.call_id);
    const entries = await Promise.all(operation.items.map(async (item, index) => {
      const ref = item.resource_ref;
      const key = `${operation.call_id}:${index}`;
      if (ref?.kind !== "document" || !ref.document_id || item.resource_status === "deleted") return undefined;
      try {
        const response = await axiosInstance.get(`${BASE_URL}/api/core/datasets/${encodeURIComponent(ref.knowledge_base_id)}/documents/${encodeURIComponent(ref.document_id)}`, { silentError: true } as never);
        const doc = response.data.data || response.data;
        return [key, { status: doc.processing_status || "unknown", reason: doc.processing_error?.message }] as const;
      } catch { return [key, { status: "unverified", reason: zh ? "当前状态未核实，请打开资源核对。" : "Current status could not be verified." }] as const; }
    }));
    setCurrent(previous => ({ ...previous, ...Object.fromEntries(entries.filter((entry): entry is NonNullable<typeof entry> => Boolean(entry))) }));
    setRefreshing(undefined);
  };
  const zh = i18n.language.startsWith("zh");
  const labels: Record<string, string> = zh
    ? { created: "已创建", uploaded: "已加入", moved: "已移动", deleted: "已删除", updated: "已更新", failed: "失败", unconfirmed: "结果待核实", skipped: "已跳过" }
    : { created: "Created", uploaded: "Added", moved: "Moved", deleted: "Deleted", updated: "Updated", failed: "Failed", unconfirmed: "Outcome unconfirmed", skipped: "Skipped" };
  return <div aria-live="polite" className="knowledge-operation-results">
    {operations.map(operation => <section key={operation.call_id}>
      <div>{zh ? "本次操作结果" : "Operation receipt"}: {operation.action}</div>
      {operation.summary && <small>{zh ? "成功 / 失败 / 跳过 / 待核实" : "Succeeded / Failed / Skipped / Unconfirmed"}: {operation.summary.succeeded} / {operation.summary.failed} / {operation.summary.skipped} / {operation.summary.unconfirmed}</small>}
      {operation.processing_summary && <div><small>{zh ? "本次回执中的解析状态（成功 / 处理中 / 失败 / 未知或未提交）" : "Processing at receipt (ready / processing / failed / unknown or not submitted)"}: {operation.processing_summary.ready || 0} / {(operation.processing_summary.submitted || 0) + (operation.processing_summary.pending || 0) + (operation.processing_summary.running || 0)} / {operation.processing_summary.failed || 0} / {(operation.processing_summary.unknown || 0) + (operation.processing_summary.not_submitted || 0) + (operation.processing_summary.not_requested || 0)}</small></div>}
      {operation.items.some(item => item.resource_ref?.document_id && item.resource_status !== "deleted") && <Button size="small" loading={refreshing === operation.call_id} onClick={() => void refresh(operation)}>{zh ? "刷新当前状态" : "Refresh current status"}</Button>}
      {operation.items.map((item, index) => {
      const href = operation.action === "knowledge.resource.delete" ? undefined : knowledgeResourceHref(item);
      const states: Record<string, string> = zh
        ? { submitted: "已提交后台解析", pending: "等待解析", running: "解析中", ready: "处理成功", failed: "处理失败", unknown: "处理状态未知", not_requested: "已保存原件，未请求解析", not_submitted: "未提交解析" }
        : { submitted: "Processing submitted", pending: "Pending", running: "Processing", ready: "Ready", failed: "Processing failed", unknown: "Processing state unknown", not_requested: "Original stored", not_submitted: "Not submitted" };
      const processing = states[item.processing_status || ""] || item.processing_status;
      return <div key={`${operation.call_id}:${index}`} style={{ border: "1px solid var(--border-color, #e5e7eb)", borderRadius: 8, padding: "10px 12px", margin: "8px 0" }}>
        <div>{href ? <Link to={href}>{item.name}</Link> : item.name}</div>
        <small>{operation.action === "knowledge.document.upload" && item.resource_status === "created" ? (zh ? "已加入" : "Added") : labels[item.resource_status] || item.resource_status}{processing && ` · ${processing}`}</small>
        {item.cleanup_status && !["completed", "not_requested"].includes(item.cleanup_status) &&
          <div><small>{zh ? "后台清理状态" : "Cleanup status"}: {item.cleanup_status}</small></div>}
        {current[`${operation.call_id}:${index}`] && <div role="status">{zh ? "本次查询状态" : "Status at refresh"}: {states[current[`${operation.call_id}:${index}`].status] || (zh ? "未核实" : "Unverified")}{current[`${operation.call_id}:${index}`].reason && <div>{current[`${operation.call_id}:${index}`].reason}</div>}</div>}
        {item.task_id && <div><small>{zh ? "任务" : "Task"}: {item.task_id}</small></div>}
        {item.processing_error && <div role="status">{item.processing_error.message}<div>{item.processing_error.recovery}</div></div>}
        {item.error?.message && <div role="status">{item.error.message}</div>}
      </div>;
    })}</section>)}
  </div>;
}
