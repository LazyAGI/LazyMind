import { useEffect, useState } from "react";
import { Button, Empty, Modal, Popconfirm, Progress, Space, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";

import type {
  KnowledgeMarketTaskDetailOpenAPIResponse,
  KnowledgeMarketTaskListItemOpenAPIResponse,
} from "@/api/generated/core-client";
import {
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
              return { ...item, ...detail };
            } catch {
              return item;
            }
          }),
        );
        if (!controller.signal.aborted) {
          const visibleTasks = details;
          setTasks(visibleTasks);
          if (
            visibleTasks.some(
              (task) => !isKnowledgeMarketTaskTerminal(toTaskState(task)),
            )
          ) {
            timer = window.setTimeout(refresh, 2000);
          }
        }
      } catch {
        if (!controller.signal.aborted && firstLoad) setTasks([]);
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

  const runAction = async (task: TaskRow, action: "delete" | "retry") => {
    setBusyJob(task.job_id);
    try {
      if (action === "delete") await deleteKnowledgeMarketTask(task.job_id);
      else await retryKnowledgeMarketTask(task.job_id);
      setRevision((value) => value + 1);
      onTasksChanged();
    } catch {
      // The shared interceptor displays API errors. Keep the row for recovery.
    } finally { setBusyJob(undefined); }
  };

  const columns: ColumnsType<TaskRow> = [
    {
      title: t("knowledge.taskName"),
      dataIndex: "name",
      render: (name: string, task) =>
        name ||
        (task.job_type === "knowledge_market_update_all"
          ? t("knowledge.taskTypeUpdateAll")
          : "-"),
    },
    {
      title: t("knowledge.taskType"),
      dataIndex: "job_type",
      width: 120,
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
      width: 110,
      render: (_, task) => {
        const taskState = toTaskState(task);
        const failed = isKnowledgeMarketTaskFailed(taskState);
        const partiallyFailed = isKnowledgeMarketTaskPartiallyFailed(taskState);
        const done = isKnowledgeMarketTaskCompleted(taskState);
        return (
          <Tag
            color={
              failed
                ? "error"
                : partiallyFailed
                  ? "warning"
                  : done
                    ? "success"
                    : "processing"
            }
          >
            {partiallyFailed
              ? t("knowledge.taskCompletedWithFailures")
              : failed
                ? t("knowledge.failed")
                : done
                  ? t("knowledge.processed")
                  : t("knowledge.processing")}
          </Tag>
        );
      },
    },
    {
      title: t("knowledge.taskProgress"),
      key: "progress",
      width: 180,
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
              : undefined
          }
        />
      ),
    },
    {
      title: t("knowledge.taskCreatedAt"),
      dataIndex: "created_at",
      width: 170,
      render: (value: string) => (value ? new Date(value).toLocaleString() : "-"),
    },
    {
      title: t("common.actions"),
      key: "actions",
      width: 150,
      render: (_, task) => {
        const state = toTaskState(task);
        const terminal = isKnowledgeMarketTaskTerminal(state);
        const latest = !tasks.some((other) => other.market_item_id === task.market_item_id && other.created_at > task.created_at);
        const retryable = terminal && latest && (isKnowledgeMarketTaskFailed(state) || isKnowledgeMarketTaskPartiallyFailed(state));
        return <Space>
          {retryable && <Button size="small" loading={busyJob === task.job_id} disabled={!!busyJob} onClick={() => void runAction(task, "retry")}>{t("common.retry")}</Button>}
          <Popconfirm title={t("knowledge.taskDeleteConfirm")} description={t("knowledge.taskDeleteHint")} disabled={!terminal || !!busyJob} onConfirm={() => runAction(task, "delete")}>
            <Button size="small" danger disabled={!terminal || !!busyJob}>{t("common.delete")}</Button>
          </Popconfirm>
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
      <Table<TaskRow>
        rowKey="job_id"
        columns={columns}
        dataSource={tasks}
        loading={loading}
        locale={{ emptyText: <Empty description={t("knowledge.taskEmpty")} /> }}
        expandable={{
          rowExpandable: (task) => Boolean(task.parse?.total || task.error_message || isKnowledgeMarketTaskFailed(toTaskState(task))),
          expandedRowRender: (task) => <Space direction="vertical">
            {task.parse && <Typography.Text>{t("knowledge.taskFileCounts", { total: task.parse.total, done: task.parse.done, failed: task.parse.failed })}</Typography.Text>}
            {(task.parse?.failures || []).map((failure, index) => <Typography.Text key={`${failure.task_id || failure.name}-${index}`} type="danger">
              {failure.name}：{t(`knowledge.taskFileFailure_${failure.reason}`)}
            </Typography.Text>)}
            {(!task.parse?.failures?.length && (task.error_message || isKnowledgeMarketTaskFailed(toTaskState(task)))) && <Typography.Text type="danger">{t("knowledge.taskFailedHint")}</Typography.Text>}
          </Space>,
        }}
        pagination={{ pageSize: 10, showSizeChanger: false }}
        scroll={{ x: 960, y: 480 }}
      />
    </Modal>
  );
}
