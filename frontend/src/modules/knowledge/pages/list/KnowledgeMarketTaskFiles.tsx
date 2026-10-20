import { useEffect, useRef, useState } from "react";
import { Alert, Button, Empty, Spin, Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { KnowledgeMarketTaskFilesOpenAPIResponse, KnowledgeMarketTaskListItemOpenAPIResponse } from "@/api/generated/core-client";
import { listKnowledgeMarketTaskFiles, type KnowledgeMarketFileStatus } from "@/modules/knowledge/api/knowledgeMarket";
import KnowledgeMarketTaskPagination from "./KnowledgeMarketTaskPagination";

const statuses: KnowledgeMarketFileStatus[] = ["success", "failed", "canceled", "unfinished"];

export default function KnowledgeMarketTaskFiles({ task, height, busy, onRetry }: {
  task: KnowledgeMarketTaskListItemOpenAPIResponse; height: number; busy: boolean; onRetry: () => void;
}) {
  const { t } = useTranslation();
  const parse = task.parse;
  const [status, setStatus] = useState<KnowledgeMarketFileStatus>(parse?.failed ? "failed" : (parse?.pending || parse?.parsing || parse?.unknown) ? "unfinished" : parse?.done ? "success" : parse?.canceled ? "canceled" : ["pending", "running"].includes(task.job_status) ? "unfinished" : "failed");
  const [page, setPage] = useState(1);
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<{ key: string; data: KnowledgeMarketTaskFilesOpenAPIResponse }>();
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [selected, setSelected] = useState<string>();
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  const list = useRef<HTMLDivElement>(null);
  const [gutter, setGutter] = useState(0);
  const key = `${task.job_id}:${status}:${page}`;
  const data = result?.key === key ? result.data : undefined;
  const polling = !["done", "failed", "partial_failed", "canceled", "partial_canceled"].includes(task.display_state || task.stage || "");

  useEffect(() => {
    const controller = new AbortController();
    let timer: number | undefined;
    let initial = true;
    const refresh = async () => {
      if (initial) setLoading(true);
      try {
        const next = await listKnowledgeMarketTaskFiles(task.job_id, status, page, { signal: controller.signal, silentError: true });
        if (controller.signal.aborted) return;
        setFailed(false);
        if (next.page !== page) { setPage(next.page); return; }
        setResult({ key, data: next });
        if (polling) timer = window.setTimeout(refresh, 2000);
      } catch {
        if (!controller.signal.aborted) { setFailed(true); timer = window.setTimeout(refresh, 2000); }
      } finally {
        if (!controller.signal.aborted) setLoading(false);
        initial = false;
      }
    };
    void refresh();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [task.job_id, status, page, key, revision, polling]);

  useEffect(() => { if (list.current) list.current.scrollTop = 0; }, [key]);
  useEffect(() => {
    const element = list.current;
    if (!element) return;
    const measure = () => setGutter(element.offsetWidth - element.clientWidth);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const counts = data?.counts || { success: parse?.done || 0, failed: parse?.failed || 0, canceled: parse?.canceled || 0, unfinished: (parse?.pending || 0) + (parse?.parsing || 0) + (parse?.unknown || 0) };
  const total = Object.values(counts).reduce((sum, value) => sum + value, 0);
  const changeStatus = (value: KnowledgeMarketFileStatus) => { setStatus(value); setPage(1); setSelected(undefined); setFailed(false); };
  return <section className="market-task-files" style={{ maxHeight: height }} aria-label={t("knowledge.taskFileDetails")}>
    <div className="market-task-files-summary">
      <h3>{t("knowledge.taskFileDetails")}</h3>
      <div role="tablist" aria-label={t("knowledge.taskFileStatus")}>
        {statuses.map((value, index) => <button key={value} ref={(node) => { tabs.current[index] = node; }} type="button" role="tab" aria-selected={status === value} aria-controls={`market-files-${task.job_id}`} id={`market-tab-${task.job_id}-${value}`} tabIndex={status === value ? 0 : -1} className={value} onClick={() => changeStatus(value)} onKeyDown={(event) => {
          let next: number | undefined;
          if (event.key === "ArrowRight") next = (index + 1) % statuses.length;
          if (event.key === "ArrowLeft") next = (index + statuses.length - 1) % statuses.length;
          if (event.key === "Home") next = 0;
          if (event.key === "End") next = statuses.length - 1;
          if (next !== undefined) { event.preventDefault(); changeStatus(statuses[next]); tabs.current[next]?.focus(); }
        }}>{t(`knowledge.taskFileStatus_${value}`)} <span>{counts[value]}</span></button>)}
      </div>
      <span>{t("knowledge.taskFileTotal", { total })}</span>
    </div>
    <div className="market-task-file-head" style={{ paddingRight: 16 + gutter }}><span>{t("knowledge.taskFileName")}</span><span>{t("knowledge.status")}</span><span>{t("knowledge.taskFileNote")}</span></div>
    <div ref={list} className="market-task-file-list" role="tabpanel" id={`market-files-${task.job_id}`} aria-labelledby={`market-tab-${task.job_id}-${status}`} aria-busy={loading}>
      {task.display_state === "blocked" && <Alert type="warning" showIcon message={t("knowledge.taskBlockedHint")} description={<Typography.Text copyable={{ text: JSON.stringify({job_id: task.job_id, state: task.display_state, stage: task.stage, total: parse?.total, done: parse?.done, failed: parse?.failed, canceled: parse?.canceled, pending: parse?.pending, processing: parse?.parsing}) }}>{t("knowledge.taskCopyDiagnostics")}</Typography.Text>} />}
      {failed && <Alert type="warning" showIcon message={t("knowledge.taskFilesQueryFailed")} action={<Button size="small" onClick={() => setRevision((value) => value + 1)}>{t("knowledge.taskRecheck")}</Button>} />}
      {!data && loading ? <div className="market-task-files-empty"><Spin /></div> : data?.file_details_available ? data.items?.length ? data.items.map((file) => <button type="button" key={file.file_id} aria-pressed={selected === file.file_id} className={`market-task-file-row ${file.status}`} onClick={() => setSelected(file.file_id)}>
        <span title={file.name}>{file.name}</span><span>{t(`knowledge.taskFileStatus_${file.status}`)}</span><span title={t(`knowledge.taskFileNote_${file.reason_code}`)}>{t(`knowledge.taskFileNote_${file.reason_code}`)}</span>
      </button>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("knowledge.taskFileEmpty", { status: t(`knowledge.taskFileStatus_${status}`) })} /> : !failed && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t(task.job_type === "knowledge_market_update_all" ? "knowledge.taskBatchFileHint" : polling ? "knowledge.taskFilesPreparing" : task.job_status === "failed" && total === 0 ? "knowledge.taskFailedHint" : "knowledge.taskHistoricalFilesUnavailable")} />}
    </div>
    <div className="market-task-files-footer">
      <div>{status === "failed" && counts.failed > 0 && <Button type="primary" size="small" loading={busy} disabled={busy || failed || task.can_retry !== true} onClick={onRetry}>{t("knowledge.taskRetry")}</Button>}</div>
      <KnowledgeMarketTaskPagination scope="files" page={page} total={data?.total ?? counts[status]} disabled={loading || !data?.file_details_available} onChange={setPage} />
    </div>
  </section>;
}
