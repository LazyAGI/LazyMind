import { useMemo, useState, useRef, useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Image, Progress, Tooltip } from "antd";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LoadingOutlined,
  FileTextOutlined,
  DownOutlined,
  RightOutlined,
  ApiOutlined,
  CheckOutlined,
  DownloadOutlined,
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
import { usePluginStore } from "@/modules/chat/store/pluginPanel";
import {
  basenameFromPath,
  resolveCoreAssetUrl,
} from "@/modules/knowledge/utils/imageUrl";
import { downloadStream } from "@/modules/chat/utils/download";
import "./index.scss";

interface Props {
  sessionId: string;
  onClose?: () => void;
}

const EMPTY_TASKS: SubAgentTask[] = [];

const RUNNING_STATUSES: TaskStatus[] = ["pending", "running"];

function imageUrlOf(value: any): string {
  const raw = value?.url || value?.path;
  if (!raw) return "";
  const resolved = resolveCoreAssetUrl(raw);
  if (!resolved) return "";
  // Avoid mounting obviously non-browser-accessible local paths (e.g. /data/subagent/*)
  // to prevent transient broken thumbnails before signed/static URLs become available.
  if (
    resolved.startsWith("/static-files/") ||
    resolved.startsWith("/api/core/static-files/") ||
    resolved.startsWith("http://") ||
    resolved.startsWith("https://")
  ) {
    return resolved;
  }
  return "";
}

function isLikelyImage(path: string): boolean {
  const pathname = path.split(/[?#]/, 1)[0].toLowerCase();
  return /\.(avif|bmp|gif|jpe?g|png|svg|webp)$/.test(pathname);
}

// Extract the raw text content from a text/json artifact value for download.
function extractTextContent(artifact: TaskArtifact): string {
  const v = artifact.value;
  if (!v) return "";
  if (artifact.content_type === "json") {
    try {
      return JSON.stringify(v.data ?? v, null, 2);
    } catch {
      return String(v.data ?? v ?? "");
    }
  }
  return v.text ?? "";
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
      <div className="task-section-body" style={open ? undefined : { display: 'none' }}>
        {children}
      </div>
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

  const imageUrls = images
    .map((a) => ({
      key: `img-${a.slot}-${a.seq}`,
      src: imageUrlOf(a.value),
      filename:
        a.value?.filename ||
        basenameFromPath(a.value?.url || a.value?.path || a.slot) ||
        "image",
    }))
    .filter((img) => Boolean(img.src));
  const fileListItems = fileLists.flatMap((artifact) => {
    const paths: string[] = Array.isArray(artifact.value?.paths)
      ? artifact.value.paths.filter(
          (path: unknown): path is string => typeof path === "string",
        )
      : [];
    return paths
      .map((path: string, pathIndex: number) => ({
        key: `fl-${artifact.slot}-${artifact.seq}-${pathIndex}`,
        src: resolveCoreAssetUrl(path),
        filename: basenameFromPath(path) || `${artifact.slot}-${pathIndex + 1}`,
        isImage: isLikelyImage(path),
      }))
      .filter((item) => Boolean(item.src));
  });
  const fileListImages = fileListItems.filter((item) => item.isImage);
  const fileListFiles = fileListItems.filter((item) => !item.isImage);

  const total =
    imageUrls.length + fileListItems.length + files.length + texts.length;

  return (
    <CollapsibleSection title={`${t("taskCenter.artifacts")} (${total})`}>
      <div className="task-artifacts-inner">
        {(imageUrls.length > 0 || fileListImages.length > 0) && (
          <div className="task-artifacts-grid">
            <Image.PreviewGroup>
              {imageUrls.map((img) => (
                <div className="task-artifact-preview" key={img.key}>
                  <Image
                    src={img.src}
                    width={64}
                    height={64}
                    className="task-artifact-thumb"
                  />
                  <a
                    href={img.src}
                    download={img.filename}
                    className="task-artifact-preview-download"
                    title={`${t("taskCenter.download")} ${img.filename}`}
                    onClick={(event) => event.stopPropagation()}
                  >
                    <DownloadOutlined />
                  </a>
                </div>
              ))}
              {fileListImages.map((img) => (
                <div className="task-artifact-preview" key={img.key}>
                  <Image
                    src={img.src}
                    width={64}
                    height={64}
                    className="task-artifact-thumb"
                  />
                  <a
                    href={img.src}
                    download={img.filename}
                    className="task-artifact-preview-download"
                    title={`${t("taskCenter.download")} ${img.filename}`}
                    onClick={(event) => event.stopPropagation()}
                  >
                    <DownloadOutlined />
                  </a>
                </div>
              ))}
            </Image.PreviewGroup>
          </div>
        )}
        {fileListFiles.map((file) => (
          <div className="task-artifact-file" key={file.key}>
            <FileTextOutlined />
            <a
              href={file.src}
              download={file.filename}
              className="task-artifact-file-name task-artifact-file-link"
              title={`${t("taskCenter.download")} ${file.filename}`}
              onClick={(event) => event.stopPropagation()}
            >
              {file.filename}
            </a>
          </div>
        ))}
        {files.map((a) => {
          const downloadUrl = resolveCoreAssetUrl(a.value?.url || "");
          const fileName: string =
            a.value?.filename || a.slot || "download";

          return (
            <div
              className="task-artifact-file"
              key={`file-${a.slot}-${a.seq}`}
            >
              <FileTextOutlined />
              {downloadUrl ? (
                <a
                  href={downloadUrl}
                  download={fileName}
                  className="task-artifact-file-name task-artifact-file-link"
                  title={`${t("taskCenter.download")} ${fileName}`}
                  onClick={(e) => e.stopPropagation()}
                >
                  {fileName}
                </a>
              ) : (
                <span className="task-artifact-file-name">
                  {fileName}
                </span>
              )}
            </div>
          );
        })}
        {texts.map((a) => {
          const textContent = extractTextContent(a);
          const textFileName =
            a.slot && a.slot.includes(".")
              ? a.slot
              : `${a.slot || "artifact"}.txt`;

          return (
            <div className="task-artifact-text" key={`txt-${a.slot}-${a.seq}`}>
              <div className="task-artifact-text-header">
                <span className="task-artifact-text-key">{a.slot}</span>
                <button
                  type="button"
                  className="task-artifact-download-btn"
                  title={`${t("taskCenter.download")} ${textFileName}`}
                  aria-label={`${t("taskCenter.download")} ${textFileName}`}
                  onClick={() =>
                    downloadStream(
                      new Blob([textContent], { type: "text/plain;charset=utf-8" }),
                      textFileName,
                    )
                  }
                >
                  <DownloadOutlined />
                  {t("taskCenter.download")}
                </button>
              </div>
              <div className="task-artifact-text-body">
                {a.content_type === "json"
                  ? JSON.stringify(a.value?.data ?? a.value)
                  : a.value?.text}
              </div>
            </div>
          );
        })}
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
          aria-label={collapsed ? t("common.expand") : t("common.collapse")}
        >
          {collapsed ? <RightOutlined /> : <DownOutlined />}
        </button>
        <Tooltip title={task.title} placement="topLeft">
          <span className="task-card-title" title={task.title}>
            {task.title}
          </span>
        </Tooltip>
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
                <span className="task-card-eta">
                  {t("taskCenter.estimatedSeconds", { seconds: task.estimated_sec })}
                </span>
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

type FilterKey = "all" | "running" | "succeeded" | "failed";

const TaskCenter = (props: Props) => {
  const { sessionId, onClose } = props;
  const { t } = useTranslation();
  const [filter, setFilter] = useState<FilterKey>("all");

  const loadActiveSession = usePluginStore((s) => s.loadActiveSession);

  // Ensure plugin session is loaded whenever the conversation changes,
  // independently of whether PluginPanel has mounted yet.
  useEffect(() => {
    if (sessionId) {
      loadActiveSession(sessionId);
    }
  }, [sessionId, loadActiveSession]);

  const tasks = useTaskCenterStore((s) =>
    sessionId ? s.tasksByConversation[sessionId] ?? EMPTY_TASKS : EMPTY_TASKS,
  );

  const filteredTasks = useMemo(() => {
    if (filter === "all") return tasks;
    if (filter === "running") return tasks.filter((t) => RUNNING_STATUSES.includes(t.status));
    if (filter === "succeeded") return tasks.filter((t) => t.status === "succeeded");
    if (filter === "failed") return tasks.filter((t) => t.status === "failed" || t.status === "interrupted" || t.status === "canceled");
    return tasks;
  }, [tasks, filter]);

  const filterDefs: { key: FilterKey; label: string }[] = [
    { key: "all", label: t("taskCenter.filterAll") },
    { key: "running", label: t("taskCenter.running") },
    { key: "succeeded", label: t("taskCenter.filterSucceeded") },
    { key: "failed", label: t("taskCenter.filterFailed") },
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
      <div className="task-center-filters">
        {filterDefs.map(({ key, label }) => (
          <button
            key={key}
            type="button"
            className={`task-filter-btn${filter === key ? " task-filter-btn--active" : ""}`}
            onClick={() => setFilter(key)}
          >
            {label}
            {key === "running" && tasks.filter((t) => RUNNING_STATUSES.includes(t.status)).length > 0 && (
              <span className="task-filter-badge">
                {tasks.filter((t) => RUNNING_STATUSES.includes(t.status)).length}
              </span>
            )}
          </button>
        ))}
      </div>
      <div className="task-list">
        {filteredTasks.length === 0 ? (
          <div className="task-empty">{t("taskCenter.empty")}</div>
        ) : (
          filteredTasks.map((task) => (
            <TaskCard key={task.task_id} task={task} />
          ))
        )}
      </div>
    </div>
  );
};

export default TaskCenter;
