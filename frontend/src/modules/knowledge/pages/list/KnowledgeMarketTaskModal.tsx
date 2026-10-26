import { useEffect, useRef, useState, type MouseEvent } from "react";
import { Button, Empty, Modal, Popconfirm, Progress, Table, Typography, message } from "antd";
import { MinusOutlined, PlusOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";
import type { KnowledgeMarketTaskListItemOpenAPIResponse } from "@/api/generated/core-client";
import { cancelKnowledgeMarketTask, deleteKnowledgeMarketTask, retryKnowledgeMarketTask } from "@/modules/knowledge/api/knowledgeMarket";
import type { KnowledgeMarketTasksQuery } from "./useKnowledgeMarketTasks";
import { getKnowledgeMarketTaskPercent, isKnowledgeMarketTaskCompleted, isKnowledgeMarketTaskFailed, isKnowledgeMarketTaskPartiallyFailed } from "./knowledgeMarketTaskState";
import KnowledgeMarketTaskFiles from "./KnowledgeMarketTaskFiles";
import KnowledgeMarketTaskPagination from "./KnowledgeMarketTaskPagination";
import "./KnowledgeMarketTaskModal.scss";

type TaskRow = KnowledgeMarketTaskListItemOpenAPIResponse;
interface KnowledgeMarketTaskModalProps {
  open: boolean;
  query: KnowledgeMarketTasksQuery;
  onClose: () => void;
  onTasksChanged: (retriedTask?: { name: string }) => void;
}
function toTaskState(task: TaskRow) {
  return { jobType: task.job_type, jobStatus: task.job_status, stage: task.stage, overallPercent: task.overall_percent, progress: task.progress, displayState: task.display_state };
}
function displayState(task: TaskRow) {
  const value = toTaskState(task);
  return task.display_state || (isKnowledgeMarketTaskPartiallyFailed(value) ? "partial_failed" : isKnowledgeMarketTaskFailed(value) ? "failed" : isKnowledgeMarketTaskCompleted(value) ? "done" : task.job_status === "pending" ? "pending" : "processing");
}

export default function KnowledgeMarketTaskModal({ open, query, onClose, onTasksChanged }: KnowledgeMarketTaskModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageHolder] = message.useMessage();
  const { data, setPage, refresh, loading, queryFailed } = query;
  const [expanded, setExpanded] = useState<string>();
  const [busyJob, setBusyJob] = useState<string>();
  const [bodyHeight, setBodyHeight] = useState(440);
  const [table, setTable] = useState<HTMLDivElement | null>(null);
  const tableRef = useRef<HTMLDivElement | null>(null);
  const busy = useRef(false);
  const mounted = useRef(false);
  const session = useRef(0);
  const followJob = useRef<string>();
  const warned = useRef(false);
  const tasks = (data?.items || []).map((task) => queryFailed ? { ...task, display_state: "unknown", can_cancel: false, can_retry: false, can_delete: false } : task);
  const queryFailedHint = t(data ? "knowledge.taskQueryFailedHint" : "knowledge.taskInitialQueryFailedHint");

  useEffect(() => { tableRef.current = table; }, [table]);
  useEffect(() => { mounted.current = open; session.current += 1; if (!open) setBusyJob(undefined); return () => { mounted.current = false; session.current += 1; }; }, [open]);
  useEffect(() => {
    if (!open || !data || queryFailed) return;
    if (followJob.current && (data.items || []).some((task) => task.job_id === followJob.current)) {
      setExpanded(followJob.current);
      followJob.current = undefined;
      tableRef.current?.querySelector(".ant-table-body")?.scrollTo?.({ top: 0 });
    } else {
      setExpanded((value) => value && (data.items || []).some((task) => task.job_id === value) ? value : undefined);
    }
  }, [open, data, queryFailed]);

  useEffect(() => {
    if (open && queryFailed && !warned.current) void messageApi.warning(queryFailedHint);
    warned.current = open && queryFailed;
  }, [open, queryFailed, queryFailedHint, messageApi]);

  useEffect(() => {
    if (!open || !table) return;
    const content = table.closest<HTMLElement>(".ant-modal-content");
    const footer = content?.querySelector<HTMLElement>(".ant-modal-footer");
    if (!content || !footer) return;
    const measure = () => {
      const maxHeight = Math.min(704, window.innerHeight - (window.innerHeight <= 740 ? 64 : 124));
      const top = table.getBoundingClientRect().top - content.getBoundingClientRect().top;
      setBodyHeight(Math.max(0, maxHeight - top - footer.offsetHeight - 44));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(content);
    observer.observe(table);
    observer.observe(footer);
    window.addEventListener("resize", measure);
    return () => { observer.disconnect(); window.removeEventListener("resize", measure); };
  }, [open, table]);

  const runAction = async (task: TaskRow, action: "delete" | "retry" | "cancel") => {
    if (busy.current) return;
    const actionSession = session.current;
    const isCurrent = () => mounted.current && session.current === actionSession;
    busy.current = true;
    setBusyJob(task.job_id);
    try {
      if (action === "delete") {
        await deleteKnowledgeMarketTask(task.job_id);
      } else if (action === "retry") {
        const next = await retryKnowledgeMarketTask(task.job_id);
        if (!isCurrent()) return;
        followJob.current = next.job_id;
        setExpanded(undefined);
        setPage(1);
      } else {
        const result = await cancelKnowledgeMarketTask(task.job_id);
        if (!isCurrent()) return;
        void messageApi.info(result.stop_requested ? t("knowledge.taskStopRequested") : t("knowledge.taskCancelResult", { canceled: result.canceled, running: result.running, unknown: result.unknown }));
      }
      if (!isCurrent()) return;
      if (action === "delete") void messageApi.success(t("knowledge.taskDeleteSuccess"));
      onTasksChanged(action === "retry" ? { name: task.name || t("knowledge.taskTypeUpdateAll") } : undefined);
    } catch {
      if (isCurrent()) void messageApi.warning(t(action === "cancel" ? "knowledge.taskCancelUnavailable" : "knowledge.taskActionFailed"));
    } finally {
      busy.current = false;
      if (isCurrent()) setBusyJob(undefined);
    }
  };
  const columns: ColumnsType<TaskRow> = [
    { title: t("knowledge.taskName"), dataIndex: "name", ellipsis: true, render: (name: string, task) => {
      const label = name || (task.job_type === "knowledge_market_update_all" ? t("knowledge.taskTypeUpdateAll") : "-");
      return task.dataset_id ? <Typography.Link className="market-task-name" title={label} href={`/lib/knowledge/detail/${encodeURIComponent(task.dataset_id)}`}>{label}</Typography.Link> : <span title={label}>{label}</span>;
    } },
    { title: t("knowledge.taskType"), dataIndex: "job_type", width: 96, render: (kind: string) => t(kind === "knowledge_market_install" ? "knowledge.taskTypeInstall" : kind === "knowledge_market_update" ? "knowledge.taskTypeUpdate" : "knowledge.taskTypeUpdateAll") },
    { title: t("knowledge.status"), width: 176, render: (_, task) => <span className={`market-task-state ${displayState(task)}`}>{t(`knowledge.taskState_${displayState(task)}`)}</span> },
    { title: t("knowledge.taskProgress"), width: 136, render: (_, task) => {
      const state = displayState(task);
      const percent = Math.min(100, Math.max(0, getKnowledgeMarketTaskPercent(toTaskState(task))));
      const color = state === "failed" ? "#f54a45" : ["partial_failed", "partial_canceled", "blocked"].includes(state) ? "#faad14" : ["unknown", "canceled"].includes(state) ? "#8f959e" : "#1677ff";
      return <div className="market-task-progress"><Progress percent={percent} showInfo={false} size="small" strokeColor={color} /><span>{percent}%</span></div>;
    } },
    { title: t("knowledge.taskCreatedAt"), dataIndex: "created_at", width: 184, render: (value: string) => value ? new Date(value).toLocaleString() : "-" },
    { title: t("common.actions"), width: 210, align: "center", render: (_, task) => <div className="market-task-actions">
      <div>{task.can_cancel === true ? <Popconfirm title={t("knowledge.taskCancelTitle")} description={t("knowledge.taskCancelHint")} disabled={!!busyJob} onConfirm={() => runAction(task, "cancel")}>
        <Button size="small" disabled={!!busyJob} loading={busyJob === task.job_id} aria-label={t(["pending", "running"].includes(task.job_status) ? "knowledge.taskStopFollowing" : "knowledge.taskCancelWaiting")}>{t("knowledge.taskFileStatus_canceled")}</Button>
      </Popconfirm> : task.can_delete === true && <Popconfirm title={t("knowledge.taskDeleteConfirm")} description={t("knowledge.taskDeleteHint")} disabled={!!busyJob} onConfirm={() => runAction(task, "delete")}>
        <Button size="small" danger disabled={!!busyJob}>{t("common.delete")}</Button>
      </Popconfirm>}</div>
      <div>{task.can_retry === true && <Button size="small" loading={busyJob === task.job_id} disabled={!!busyJob} onClick={() => void runAction(task, "retry")}>{t("knowledge.taskRetry")}</Button>}</div>
    </div> },
  ];
  return <Modal className="knowledge-market-task-modal" width={1100} open={open} title={t("knowledge.backgroundTasks")} onCancel={onClose} destroyOnHidden footer={<KnowledgeMarketTaskPagination scope="tasks" page={data?.page || 1} total={data?.total || 0} disabled={loading || !!busyJob} onChange={(value) => { setPage(value); setExpanded(undefined); refresh(); }} />}>
    {messageHolder}
    <div className="market-task-table" ref={setTable}>
      <Table<TaskRow> rowKey="job_id" columns={columns} dataSource={tasks} loading={loading} pagination={false} tableLayout="fixed" locale={{ emptyText: <Empty description={queryFailed ? queryFailedHint : t("knowledge.taskEmpty")} /> }} rowClassName={(task: TaskRow) => expanded === task.job_id ? "market-task-selected" : ""} scroll={{ x: 1030, y: bodyHeight }} expandable={{
        columnWidth: 48,
        expandedRowKeys: expanded ? [expanded] : [],
        onExpand: (value: boolean, task: TaskRow) => setExpanded(value ? task.job_id : undefined),
        expandIcon: ({ expanded: isExpanded, onExpand, record }: { expanded: boolean; onExpand: (record: TaskRow, event: MouseEvent<HTMLElement>) => void; record: TaskRow }) => <button type="button" className="market-task-expand" aria-label={`${t(isExpanded ? "knowledge.taskCollapse" : "knowledge.taskExpand")} ${record.name || t("knowledge.taskTypeUpdateAll")}`} aria-expanded={isExpanded} onClick={(event) => onExpand(record, event)}>{isExpanded ? <MinusOutlined /> : <PlusOutlined />}</button>,
        expandedRowRender: (task: TaskRow) => <KnowledgeMarketTaskFiles key={task.job_id} task={task} height={Math.max(358, bodyHeight - tasks.length * 48)} busy={!!busyJob} onRetry={() => void runAction(task, "retry")} />,
      }} />
    </div>
  </Modal>;
}
