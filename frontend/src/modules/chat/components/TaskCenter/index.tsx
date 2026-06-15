import { useMemo, useState, useRef, useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Image, Progress, Tabs, Tooltip } from "antd";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LoadingOutlined,
  FileTextOutlined,
  DownOutlined,
  RightOutlined,
  ApiOutlined,
  CheckOutlined,
} from "@ant-design/icons";

import {
  SubAgentTask,
  TaskArtifact,
  TaskLogEntry,
  ToolCallItem,
  ToolResultItem,
  TaskStatus,
  useTaskCenterStore,
} from "@/modules/chat/store/taskCenter";
import { resolveCoreAssetUrl } from "@/modules/knowledge/utils/imageUrl";
import "./index.scss";

interface Props {
  sessionId: string;
  onClose?: () => void;
}

const EMPTY_TASKS: SubAgentTask[] = [];

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

// Strip lazyllm tool-call/result XML tags from think content, keeping only the pure reasoning text.
function cleanThinkContent(raw: string): string {
  return raw
    .replace(/<tp\b[^>]*>([\s\S]*?)<\/tp>/g, "$1")
    .replace(/<trp\b[^>]*>([\s\S]*?)<\/trp>/g, "$1")
    .replace(/<tool_call>[\s\S]*?<\/tool_call>/g, "")
    .replace(/<tool_result>[\s\S]*?<\/tool_result>/g, "")
    .trim();
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
  // Auto-expand when defaultOpen flips to true (e.g. task transitions pending→running).
  useEffect(() => {
    if (defaultOpen) setOpen(true);
  }, [defaultOpen]);
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

function ToolCallRow({ call }: { call: ToolCallItem }) {
  const [open, setOpen] = useState(false);
  const argsStr = useMemo(() => {
    try {
      const obj = typeof call.args === "string" ? JSON.parse(call.args) : call.args;
      return JSON.stringify(obj, null, 2);
    } catch {
      return String(call.args ?? "");
    }
  }, [call.args]);
  return (
    <div className="task-tool-call">
      <button
        type="button"
        className="task-tool-call-header"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <ApiOutlined className="task-tool-call-icon" />
        <span className="task-tool-call-name">{call.name}</span>
        <span className="task-tool-call-arrow">{open ? <DownOutlined /> : <RightOutlined />}</span>
      </button>
      {open && argsStr && (
        <pre className="task-tool-call-args">{argsStr}</pre>
      )}
    </div>
  );
}

function ToolResultRow({ result }: { result: ToolResultItem }) {
  const [open, setOpen] = useState(false);
  const bodyText = useMemo(() => {
    const r = result.result;
    if (r === null || r === undefined) return "";
    if (typeof r === "string") return r;
    try {
      return JSON.stringify(r, null, 2);
    } catch {
      return String(r);
    }
  }, [result.result]);
  return (
    <div className="task-tool-result">
      <button
        type="button"
        className="task-tool-result-header"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <CheckOutlined className="task-tool-result-icon" />
        <span className="task-tool-result-name">{result.name}</span>
        <span className="task-tool-result-arrow">{open ? <DownOutlined /> : <RightOutlined />}</span>
      </button>
      {open && bodyText && (
        <pre className="task-tool-result-body">{bodyText}</pre>
      )}
    </div>
  );
}

function ExecutionLog({ log, isRunning }: { log: TaskLogEntry[]; isRunning: boolean }) {
  const { t } = useTranslation();
  if (!log || log.length === 0) return null;

  // Merge consecutive same-type text/think entries to avoid per-token line breaks during streaming.
  const mergedLog = log.reduce<TaskLogEntry[]>((acc, entry) => {
    const prev = acc[acc.length - 1];
    if (prev && (entry.type === "text" || entry.type === "think") && prev.type === entry.type) {
      acc[acc.length - 1] = { ...prev, content: prev.content + entry.content };
      return acc;
    }
    return [...acc, entry];
  }, []);

  return (
    <CollapsibleSection
      title={t("taskCenter.executionProcess")}
      defaultOpen={isRunning}
    >
      <div className="task-execution-log">
        {mergedLog.map((entry, i) => {
          if (entry.type === "think") {
            const cleaned = cleanThinkContent(entry.content);
            if (!cleaned) return null;
            return <div key={i} className="task-log-think">{cleaned}</div>;
          }
          if (entry.type === "text") {
            const cleaned = cleanThinkContent(entry.content);
            if (!cleaned) return null;
            return <div key={i} className="task-log-text">{cleaned}</div>;
          }
          if (entry.type === "tool_calls") {
            return (
              <div key={i} className="task-log-tool-calls">
                {(entry.tool_calls ?? []).map((call, j) => (
                  <ToolCallRow key={`${i}-${j}`} call={call} />
                ))}
              </div>
            );
          }
          if (entry.type === "tool_results") {
            return (
              <div key={i} className="task-log-tool-results">
                {(entry.tool_results ?? []).map((result, j) => (
                  <ToolResultRow key={`${i}-${j}`} result={result} />
                ))}
              </div>
            );
          }
          return null;
        })}
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
  const [cardHeight, setCardHeight] = useState<number>(0);
  const cardDragRef = useRef<{ startY: number; startH: number } | null>(null);
  const { t } = useTranslation();
  const isRunning = RUNNING_STATUSES.includes(task.status);

  const onCardResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const card = (e.currentTarget as HTMLElement).parentElement;
    if (!card) return;
    cardDragRef.current = { startY: e.clientY, startH: card.offsetHeight };
    const onMove = (me: MouseEvent) => {
      if (!cardDragRef.current) return;
      const delta = me.clientY - cardDragRef.current.startY;
      const next = Math.max(80, cardDragRef.current.startH + delta);
      setCardHeight(next);
    };
    const onUp = () => {
      cardDragRef.current = null;
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, []);

  return (
    <div
      className={`task-card ${collapsed ? "task-card-collapsed" : ""}`}
      style={cardHeight && !collapsed ? { maxHeight: cardHeight, overflow: 'hidden', display: 'flex', flexDirection: 'column' } : undefined}
    >
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
        <span className="task-card-tag">{t("taskCenter.panelTitle")}</span>
        <StatusBadge status={task.status} />
      </div>
      {!collapsed && (
        <>
          {isRunning && (
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
          )}
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
          <ExecutionLog log={task.execution_log} isRunning={isRunning} />
          <ArtifactGrid artifacts={task.artifacts} />
        </>
      )}
      {!collapsed && (
        <div className="task-card-resize-handle" onMouseDown={onCardResizeStart} />
      )}
    </div>
  );
}

const TaskCenter = (props: Props) => {
  const { sessionId, onClose } = props;
  const { t } = useTranslation();

  const tasks = useTaskCenterStore((s) =>
    sessionId ? s.tasksByConversation[sessionId] ?? EMPTY_TASKS : EMPTY_TASKS,
  );

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
          {t("taskCenter.panelTitle")}
        </span>
        {onClose && (
          <button
            type="button"
            className="task-center-close-btn"
            onClick={onClose}
            title={t("taskCenter.panelTitle")}
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
