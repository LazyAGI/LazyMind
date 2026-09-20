import { useEffect, useRef, useState } from "react";
import { Alert, Button, Empty, Modal, Popconfirm, Progress, Space, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";

import type {
  KnowledgeMarketTaskDetailOpenAPIResponse,
  KnowledgeMarketTaskListItemOpenAPIResponse,
} from "@/api/generated/core-client";
import {
  cancelKnowledgeMarketTask,
  getKnowledgeMarketTask,
  deleteKnowledgeMarketTask,
  retryKnowledgeMarketTask,
  listKnowledgeMarketTasks,
} from "@/modules/knowledge/api/knowledgeMarket";
import {
  getKnowledgeMarketTaskPercent,
  isKnowledgeMarketTaskCompleted,
  isKnowledgeMarketTaskFailed,
  isKnowledgeMarketTaskPartiallyFailed,
  isKnowledgeMarketTaskTerminal,
} from "./knowledgeMarketTaskState";

const JOB_TYPES = [
  "knowledge_market_install",
  "knowledge_market_update",
  "knowledge_market_update_all",
] as const;

type TaskRow = KnowledgeMarketTaskListItemOpenAPIResponse &
  Partial<KnowledgeMarketTaskDetailOpenAPIResponse>;

interface KnowledgeMarketTaskModalProps {
  open: boolean;
  refreshKey: string;
  onClose: () => void;
  onTasksChanged: () => void;
}

function toTaskState(task: TaskRow) {
  return {
    jobType: task.job_type,
    jobStatus: task.job_status,
    stage: task.stage,
    overallPercent: task.overall_percent,
    progress: task.progress,
    displayState: task.display_state,
  };
}

export default function KnowledgeMarketTaskModal({
  open,
  refreshKey,
  onClose,
  onTasksChanged,
}: KnowledgeMarketTaskModalProps) {
  const { t } = useTranslation();
  const [tasks, setTasks] = useState<TaskRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const [busyJob, setBusyJob] = useState<string>();
  const [feedback, setFeedback] = useState<string>();
  const [queryFailed, setQueryFailed] = useState(false);
  const previousTasks = useRef<TaskRow[]>([]);
  const lastChecked = useRef<string>();
  const unknownTask = (task: TaskRow): TaskRow => ({ ...task, display_state: "unknown", can_cancel: false, can_retry: false, can_delete: false });

  useEffect(() => {
    if (!open) return;

    const controller = new AbortController();
    let timer: number | undefined;
    let firstLoad = true;

    const refresh = async () => {
      if (firstLoad) setLoading(true);
      try {
        const taskLists = await Promise.all(
          JOB_TYPES.map((jobType) =>
            listKnowledgeMarketTasks(jobType, {
              signal: controller.signal,
              silentError: true,
            }),
          ),
        );
        const listItems = taskLists.flatMap((list) => list.items || [])
          .sort((a, b) => b.created_at.localeCompare(a.created_at));
        const details = await Promise.all(
          listItems.map(async (item) => {
            try {
              const detail = await getKnowledgeMarketTask(item.job_id, {
                signal: controller.signal,
                silentError: true,
              });
              if (detail.display_state === "unknown") {
                return unknownTask(previousTasks.current.find((old) => old.job_id === item.job_id) || { ...item, ...detail });
              }
              return { ...item, ...detail };
            } catch {
              return unknownTask({ ...previousTasks.current.find((old) => old.job_id === item.job_id), ...item });
            }
          }),
        );
        if (!controller.signal.aborted) {
          const visibleTasks = details;
          previousTasks.current = visibleTasks;
          setTasks(visibleTasks);
          const hasUnknown = visibleTasks.some((task) => task.display_state === "unknown");
          setQueryFailed(hasUnknown);
          if (!hasUnknown) lastChecked.current = new Date().toLocaleString();
          if (
            visibleTasks.some(
              (task) => !isKnowledgeMarketTaskTerminal(toTaskState(task)),
            )
          ) {
            timer = window.setTimeout(refresh, 2000);
          }
        }
      } catch {
        if (!controller.signal.aborted) {
          setQueryFailed(true);
          setTasks(previousTasks.current.map(unknownTask));
          timer = window.setTimeout(refresh, 2000);
        }
      } finally {
        firstLoad = false;
        if (!controller.signal.aborted) setLoading(false);
      }
    };

    void refresh();
    return () => {
      controller.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [open, refreshKey, revision]);

  const runAction = async (task: TaskRow, action: "delete" | "retry" | "cancel") => {
    setBusyJob(task.job_id);
    try {
      if (action === "delete") await deleteKnowledgeMarketTask(task.job_id);
      else if (action === "retry") await retryKnowledgeMarketTask(task.job_id);
      else {
        const result = await cancelKnowledgeMarketTask(task.job_id);
        setFeedback(result.stop_requested ? t("knowledge.taskStopRequested") : t("knowledge.taskCancelResult", { canceled: result.canceled, running: result.running, unknown: result.unknown }));
      }
      setRevision((value) => value + 1);
      onTasksChanged();
    } catch {
      if (action === "cancel") setFeedback(t("knowledge.taskCancelUnavailable"));
    } finally { setBusyJob(undefined); }
  };

  const columns: ColumnsType<TaskRow> = [
    {
      title: t("knowledge.taskName"),
      dataIndex: "name",
      width: 180,
      render: (name: string, task) => {
        const label = name || (task.job_type === "knowledge_market_update_all" ? t("knowledge.taskTypeUpdateAll") : "-");
        return task.dataset_id ? <Typography.Link href={`/lib/knowledge/detail/${encodeURIComponent(task.dataset_id)}`}>{label}</Typography.Link> : label;
      },
    },
    {
      title: t("knowledge.taskType"),
      dataIndex: "job_type",
      width: 90,
      render: (jobType: string) =>
        t(
          jobType === "knowledge_market_install"
            ? "knowledge.taskTypeInstall"
            : jobType === "knowledge_market_update"
              ? "knowledge.taskTypeUpdate"
              : "knowledge.taskTypeUpdateAll",
        ),
    },
    {
      title: t("knowledge.status"),
      key: "status",
      width: 170,
      render: (_, task) => {
        const taskState = toTaskState(task);
        const failed = isKnowledgeMarketTaskFailed(taskState);
        const partiallyFailed = isKnowledgeMarketTaskPartiallyFailed(taskState);
        const done = isKnowledgeMarketTaskCompleted(taskState);
        const state = task.display_state;
        const label = state ? t(`knowledge.taskState_${state}`) : partiallyFailed
          ? t("knowledge.taskCompletedWithFailures") : failed ? t("knowledge.failed") : done ? t("knowledge.processed") : t("knowledge.processing");
        return <Tag style={{ maxWidth: "100%", whiteSpace: "normal" }} color={failed ? "error" : ["blocked", "unknown", "partial_failed", "partial_canceled"].includes(state || "") ? "warning" : done ? "success" : state === "processing" ? "processing" : "default"}>{label}</Tag>;
      },
    },
    {
      title: t("knowledge.taskProgress"),
      key: "progress",
      width: 130,
      render: (_, task) => (
        <Progress
          percent={Math.min(
            100,
            Math.max(0, getKnowledgeMarketTaskPercent(toTaskState(task))),
          )}
          size="small"
          status={
            isKnowledgeMarketTaskFailed(toTaskState(task))
              ? "exception"
              : isKnowledgeMarketTaskCompleted(toTaskState(task)) ? "success" : "normal"
          }
        />
      ),
    },
    {
      title: t("knowledge.taskCreatedAt"),
      dataIndex: "created_at",
      width: 150,
      render: (value: string) => (value ? new Date(value).toLocaleString() : "-"),
    },
    {
      title: t("common.actions"),
      key: "actions",
      width: 250,
      render: (_, task) => {
        const canCancel = task.can_cancel === true;
        const canRetry = task.can_retry === true;
        const canDelete = task.can_delete === true;
        const recheck = ["blocked", "unknown"].includes(task.display_state || "") || !!feedback || (task.parse?.failed && !canRetry);
        return <Space wrap>
          {canCancel && <Popconfirm title={t("knowledge.taskCancelTitle")} description={t("knowledge.taskCancelHint")} disabled={!!busyJob} onConfirm={() => runAction(task, "cancel")}>
            <Button size="small" disabled={!!busyJob} loading={busyJob === task.job_id}>{t(["pending", "running"].includes(task.job_status) ? "knowledge.taskStopFollowing" : "knowledge.taskCancelWaiting")}</Button>
          </Popconfirm>}
          {canRetry && <Button size="small" loading={busyJob === task.job_id} disabled={!!busyJob} onClick={() => void runAction(task, "retry")}>{t("knowledge.taskRetryFailed")}</Button>}
          {!!recheck && <Button size="small" disabled={!!busyJob} onClick={() => setRevision((value) => value + 1)}>{t("knowledge.taskRecheck")}</Button>}
          {canDelete && <Popconfirm title={t("knowledge.taskDeleteConfirm")} description={t("knowledge.taskDeleteHint")} disabled={!!busyJob} onConfirm={() => runAction(task, "delete")}>
            <Button size="small" danger disabled={!!busyJob}>{t("common.delete")}</Button>
          </Popconfirm>}
        </Space>;
      },
    },
  ];

  return (
    <Modal
      width={1100}
      open={open}
      title={t("knowledge.backgroundTasks")}
      footer={null}
      onCancel={onClose}
      destroyOnHidden
    >
      {feedback && <Alert type="info" showIcon message={<span role="status">{feedback}</span>} />}
      {queryFailed && <Alert type="warning" showIcon message={t("knowledge.taskQueryFailedHint")} description={<Space wrap>
        {lastChecked.current && <span>{t("knowledge.taskLastChecked", { time: lastChecked.current })}</span>}
        {tasks.length === 0 && <Button onClick={() => setRevision((value) => value + 1)}>{t("knowledge.taskRecheck")}</Button>}
      </Space>} />}
      <Table<TaskRow>
        rowKey="job_id"
        columns={columns}
        dataSource={tasks}
        loading={loading}
        locale={{ emptyText: <Empty description={t("knowledge.taskEmpty")} /> }}
        expandable={{
          rowExpandable: (task) => Boolean(task.display_state === "blocked" || task.parse?.total || task.error_message || isKnowledgeMarketTaskFailed(toTaskState(task))),
          expandedRowRender: (task) => <Space direction="vertical">
            {task.display_state === "blocked" && <>
              <Typography.Text>{t("knowledge.taskBlockedHint")}</Typography.Text>
              <Typography.Text copyable={{ text: JSON.stringify({ job_id: task.job_id, state: task.display_state, stage: task.stage, total: task.parse?.total, done: task.parse?.done, failed: task.parse?.failed, canceled: task.parse?.canceled, pending: task.parse?.pending, processing: task.parse?.parsing }) }}>{t("knowledge.taskCopyDiagnostics")}</Typography.Text>
            </>}
            {task.parse && <Typography.Text>{t("knowledge.taskControlCounts", { total: task.parse.total, done: task.parse.done, failed: task.parse.failed, canceled: task.parse.canceled || 0, unfinished: task.parse.pending + task.parse.parsing + (task.parse.unknown || 0) })}</Typography.Text>}
            {(task.parse?.failures || []).map((failure, index) => <Typography.Text key={`${failure.task_id || failure.name}-${index}`} type="danger">
              {failure.name}：{t(`knowledge.taskFileFailure_${failure.reason}`)}
            </Typography.Text>)}
            {(!task.parse?.failures?.length && (task.error_message || isKnowledgeMarketTaskFailed(toTaskState(task)))) && <Typography.Text type="danger">{t("knowledge.taskFailedHint")}</Typography.Text>}
          </Space>,
        }}
        pagination={{ pageSize: 10, showSizeChanger: false }}
        scroll={{ x: 1040, y: 480 }}
      />
    </Modal>
  );
}
