import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Image, Progress, Tabs, Tooltip } from "antd";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LoadingOutlined,
  FileTextOutlined,
  DownOutlined,
  RightOutlined,
} from "@ant-design/icons";

import {
  SubAgentTask,
  TaskArtifact,
  TaskLogEntry,
  TaskStatus,
} from "@/modules/chat/store/taskCenter";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";
import "./index.scss";

interface Props {
  tasks: SubAgentTask[];
  onClose?: () => void;
}

const RUNNING_STATUSES: TaskStatus[] = ["pending", "running"];
const HISTORY_STATUSES: TaskStatus[] = [
  "succeeded",
  "failed",
  "interrupted",
  "canceled",
];

function imageUrlOf(value: any): string {
  if (!value) return "";
  if (value.url) return value.url;
  if (value.path) return resolveCoreAssetUrl(value.path);
  return "";
}

function CollapsibleSection({
  title,
  defaultOpen = true,
  children,
}: {
  title: React.ReactNode;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="task-section">
      <button
        type="button"
        className="task-section-header"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <span className="task-section-icon">
          {open ? <DownOutlined /> : <RightOutlined />}
        </span>
        <span className="task-section-title">{title}</span>
      </button>
      {open && <div className="task-section-body">{children}</div>}
    </div>
  );
}

function ExecutionLog({ log }: { log: TaskLogEntry[] }) {
  const { t } = useTranslation();
  if (!log || log.length === 0) return null;
  const merged = log.reduce<{ type: "text" | "think"; content: string }[]>(
    (acc, entry) => {
      const last = acc[acc.length - 1];
      if (last && last.type === entry.type) {
        return [...acc.slice(0, -1), { ...last, content: last.content + entry.content }];
      }
      return [...acc, { ...entry }];
    },
    [],
  );
  return (
    <CollapsibleSection title={t("taskCenter.executionProcess")} defaultOpen={false}>
      <div className="task-execution-log">
        {merged.map((entry, i) =>
          entry.type === "think" ? (
            <div key={i} className="task-log-think">{entry.content}</div>
          ) : (
            <div key={i} className="task-log-text">{entry.content}</div>
          ),
        )}
      </div>
    </CollapsibleSection>
  );
}

function ArtifactGrid({ artifacts }: { artifacts: TaskArtifact[] }) {
  const { t } = useTranslation();
  if (!artifacts || artifacts.length === 0) {
    return null;
  }
  const images = artifacts.filter((a) => a.content_type === "image");
  const fileLists = artifacts.filter((a) => a.content_type === "file_list");
  const files = artifacts.filter((a) => a.content_type === "file");
  const texts = artifacts.filter(
    (a) => a.content_type === "text" || a.content_type === "json",
  );

  const fileListPaths = fileLists.flatMap((a) =>
    Array.isArray(a.value?.paths) ? a.value.paths : [],
  );

  const total =
    images.length + fileListPaths.length + files.length + texts.length;

  return (
    <CollapsibleSection title={`${t("taskCenter.artifacts")} (${total})`}>
      <div className="task-artifacts-inner">
        {(images.length > 0 || fileListPaths.length > 0) && (
          <div className="task-artifacts-grid">
            <Image.PreviewGroup>
              {images.map((a) => (
                <Image
                  key={`img-${a.artifact_key}-${a.seq}`}
                  src={imageUrlOf(a.value)}
                  width={64}
                  height={64}
                  className="task-artifact-thumb"
                />
              ))}
              {fileListPaths.map((p: string, i: number) => (
                <Image
                  key={`fl-${i}`}
                  src={resolveCoreAssetUrl(p)}
                  width={64}
                  height={64}
                  className="task-artifact-thumb"
                />
              ))}
            </Image.PreviewGroup>
          </div>
        )}
        {files.map((a) => (
          <div className="task-artifact-file" key={`file-${a.artifact_key}-${a.seq}`}>
            <FileTextOutlined />
            <span className="task-artifact-file-name">
              {a.value?.filename || a.artifact_key}
            </span>
          </div>
        ))}
        {texts.map((a) => (
          <div className="task-artifact-text" key={`txt-${a.artifact_key}-${a.seq}`}>
            <div className="task-artifact-text-key">{a.artifact_key}</div>
            <div className="task-artifact-text-body">
              {a.content_type === "json"
                ? JSON.stringify(a.value?.data ?? a.value)
                : a.value?.text}
            </div>
          </div>
        ))}
      </div>
    </CollapsibleSection>
  );
}

function StatusBadge({ status }: { status: TaskStatus }) {
  const { t } = useTranslation();
  if (status === "succeeded") {
    return (
      <span className="task-status task-status-success">
        <CheckCircleFilled /> {t("taskCenter.statusSucceeded")}
      </span>
    );
  }
  if (status === "failed" || status === "canceled") {
    return (
      <span className="task-status task-status-failed">
        <CloseCircleFilled /> {t("taskCenter.statusFailed")}
      </span>
    );
  }
  if (status === "interrupted") {
    return (
      <span className="task-status task-status-failed">
        <CloseCircleFilled /> {t("taskCenter.statusInterrupted")}
      </span>
    );
  }
  return (
    <span className="task-status task-status-running">
      <LoadingOutlined /> {t("taskCenter.statusRunning")}
    </span>
  );
}

function TaskCard({ task }: { task: SubAgentTask }) {
  const [collapsed, setCollapsed] = useState(false);
  const isRunning = RUNNING_STATUSES.includes(task.status);
  return (
    <div className={`task-card ${collapsed ? "task-card-collapsed" : ""}`}>
      <div className="task-card-header">
        <button
          type="button"
          className="task-card-collapse-btn"
          onClick={() => setCollapsed((v) => !v)}
          aria-expanded={!collapsed}
          aria-label={collapsed ? "展开" : "收起"}
        >
          {collapsed ? <RightOutlined /> : <DownOutlined />}
        </button>
        <span className="task-card-title">{task.title}</span>
        <span className="task-card-tag">SubAgent</span>
        <StatusBadge status={task.status} />
      </div>
      {!collapsed && (
        <>
          <Progress
            percent={task.progress_pct}
            size="small"
            status={
              task.status === "failed" || task.status === "canceled"
                ? "exception"
                : task.status === "succeeded"
                  ? "success"
                  : "active"
            }
            showInfo
          />
          {isRunning && task.current_phase && (
            <div className="task-card-phase">
              <Tooltip title={task.current_phase}>
                <span>{task.current_phase}</span>
              </Tooltip>
              {task.estimated_sec ? (
                <span className="task-card-eta">~{task.estimated_sec}s</span>
              ) : null}
            </div>
          )}
          <ExecutionLog log={task.execution_log} />
          <ArtifactGrid artifacts={task.artifacts} />
        </>
      )}
    </div>
  );
}

const TaskCenter = (props: Props) => {
  const { tasks, onClose } = props;
  const { t } = useTranslation();

  const runningTasks = useMemo(
    () => tasks.filter((t) => RUNNING_STATUSES.includes(t.status)),
    [tasks],
  );
  const historyTasks = useMemo(
    () => tasks.filter((t) => HISTORY_STATUSES.includes(t.status)),
    [tasks],
  );

  const items = [
    {
      key: "running",
      label: `${t("taskCenter.running")} (${runningTasks.length})`,
      children: (
        <div className="task-list">
          {runningTasks.length === 0 ? (
            <div className="task-empty">{t("taskCenter.empty")}</div>
          ) : (
            runningTasks.map((task) => (
              <TaskCard key={task.task_id} task={task} />
            ))
          )}
        </div>
      ),
    },
    {
      key: "history",
      label: t("taskCenter.history"),
      children: (
        <div className="task-list">
          {historyTasks.length === 0 ? (
            <div className="task-empty">{t("taskCenter.empty")}</div>
          ) : (
            historyTasks.map((task) => (
              <TaskCard key={task.task_id} task={task} />
            ))
          )}
        </div>
      ),
    },
  ];

  return (
    <div className="task-center">
      <div className="task-center-header">
        <span className="task-center-title">
          SubAgent ({tasks.length})
        </span>
        {onClose && (
          <button
            type="button"
            className="task-center-close-btn"
            onClick={onClose}
            title="折叠 SubAgent 面板"
          >
            <RightOutlined />
          </button>
        )}
      </div>
      <Tabs defaultActiveKey="running" items={items} />
    </div>
  );
};

export default TaskCenter;
