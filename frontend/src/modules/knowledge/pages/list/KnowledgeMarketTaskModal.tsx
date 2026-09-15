import { useEffect, useState } from "react";
import { Empty, Modal, Progress, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";

import type {
  KnowledgeMarketTaskDetailOpenAPIResponse,
  KnowledgeMarketTaskListItemOpenAPIResponse,
} from "@/api/generated/core-client";
import {
  getKnowledgeMarketTask,
  listKnowledgeMarketTasks,
} from "@/modules/knowledge/api/knowledgeMarket";
import {
  getLatestKnowledgeMarketTasks,
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
}: KnowledgeMarketTaskModalProps) {
  const { t } = useTranslation();
  const [tasks, setTasks] = useState<TaskRow[]>([]);
  const [loading, setLoading] = useState(false);

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
        const listItems = getLatestKnowledgeMarketTasks(
          taskLists.flatMap((list) => list.items || []),
        );
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
          const visibleTasks = details.filter(
            (task) => !isKnowledgeMarketTaskCompleted(toTaskState(task)),
          );
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
  }, [open, refreshKey]);

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
              ? t("knowledge.partialFailed")
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
  ];

  return (
    <Modal
      width={900}
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
        pagination={false}
        scroll={{ x: 760, y: 480 }}
      />
    </Modal>
  );
}
