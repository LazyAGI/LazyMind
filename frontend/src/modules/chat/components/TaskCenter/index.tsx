import { useMemo, useState, useRef, useCallback, useEffect, useId } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Image, Progress, Tooltip } from "antd";
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LoadingOutlined,
  FileTextOutlined,
  DownOutlined,
  RightOutlined,
  ApiOutlined,
  BulbOutlined,
  CheckOutlined,
  DownloadOutlined,
  GlobalOutlined,
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
import {
  basenameFromPath,
  resolveCoreAssetUrl,
} from "@/modules/knowledge/utils/imageUrl";
import { downloadStream } from "@/modules/chat/utils/download";
import {
  type ChatSource,
  getSearchSources,
  getSourceDedupKey,
  getSourceEvidenceText,
  getSourceFaviconUrl,
  getSourceHref,
  getSourceLabel,
  getSourceSubtitle,
} from "@/modules/chat/utils/sourceAdapter";
import type { WorkflowSessionStep } from "@/modules/chat/store/workflowPanel";
import {
  buildOrdinaryTaskTimeline,
  type OrdinaryTaskGroup,
  type OrdinaryTaskItem,
  type OrdinaryTaskState,
  type OrdinaryTaskTimeline,
} from "./taskTimeline";
import "./index.scss";

interface Props {
  sessionId: string;
  onClose?: () => void;
  showHeader?: boolean;
  developerMode?: boolean;
  workflowSteps?: WorkflowSessionStep[];
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
  const previewImages = [...imageUrls, ...fileListImages].filter(
    (image, index, items) =>
      items.findIndex((candidate) => candidate.src === image.src) === index,
  );

  const total =
    previewImages.length + fileListFiles.length + files.length + texts.length;

  return (
    <CollapsibleSection title={`${t("taskCenter.artifacts")} (${total})`}>
      <div className="task-artifacts-inner">
        {previewImages.length > 0 && (
          <div className="task-artifacts-grid">
            <Image.PreviewGroup>
              {previewImages.map((img) => (
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

function TaskSourceIcon({ source }: { source: ChatSource }) {
  const [failed, setFailed] = useState(false);
  const favicon = getSourceFaviconUrl(source);
  return (
    <span className="task-source-icon" aria-hidden="true">
      {favicon && !failed ? (
        <img
          src={favicon}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
        />
      ) : (
        <FileTextOutlined />
      )}
    </span>
  );
}

function ReferenceSources({
  sources,
  defaultOpen = false,
}: {
  sources: ChatSource[];
  defaultOpen?: boolean;
}) {
  const { t } = useTranslation();
  const displaySources = getSearchSources(sources);
  if (displaySources.length === 0) return null;

  return (
    <CollapsibleSection
      title={`${t("taskCenter.references")} (${displaySources.length})`}
      defaultOpen={defaultOpen}
    >
      <div className="task-source-list">
        {displaySources.map((source, index) => (
          <a
            className="task-source-item"
            key={getSourceDedupKey(source, index)}
            href={getSourceHref(source)}
            target="_blank"
            rel="noopener noreferrer"
            title={getSourceLabel(source)}
          >
            <TaskSourceIcon source={source} />
            <span className="task-source-copy">
              <span className="task-source-heading">
                {getSourceSubtitle(source) || t("taskCenter.references")}
              </span>
              <strong className="task-source-title">{getSourceLabel(source)}</strong>
              {getSourceEvidenceText(source) && (
                <span className="task-source-content">{getSourceEvidenceText(source)}</span>
              )}
            </span>
            <RightOutlined className="task-source-arrow" aria-hidden="true" />
          </a>
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
          <ReferenceSources sources={task.sources} />
        </>
      )}
      {!collapsed && (
        <div className="task-card-resize-handle" onMouseDown={onCardResizeStart} />
      )}
    </div>
  );
}

function formatDuration(seconds: number | undefined, t: TFunction): string {
  if (seconds === undefined) return "";
  if (seconds < 60) {
    return t("taskCenter.durationSeconds", { seconds });
  }
  return t("taskCenter.durationMinutes", {
    minutes: Math.floor(seconds / 60),
    seconds: seconds % 60,
  });
}

function stateLabel(state: OrdinaryTaskState, t: TFunction): string {
  if (state === "complete") return t("taskCenter.statusSucceeded");
  if (state === "running") return t("taskCenter.ordinaryStatusRunning");
  if (state === "failed") return t("taskCenter.statusFailed");
  if (state === "outdated") return t("taskCenter.ordinaryStatusOutdated");
  return t("taskCenter.statusPending");
}

function StateMarker({
  state,
  ordinal,
}: {
  state: OrdinaryTaskState;
  ordinal?: number;
}) {
  if (state === "complete") return <CheckOutlined />;
  if (state === "running") {
    return <span className="ordinary-task-spinner" aria-hidden="true" />;
  }
  if (state === "failed") return <CloseCircleFilled />;
  return <>{ordinal}</>;
}

function publicTaskTitle(task: SubAgentTask | undefined): string {
  if (!task) return "";
  const title = task.title?.trim() ?? "";
  if (
    task.agent_type === "workflow_step" &&
    /^[^:]+:[a-z0-9_.-]+$/i.test(title)
  ) {
    return "";
  }
  return title;
}

interface OrdinaryThinkingStep {
  id: "accepted" | "processing" | "result";
  title: string;
  summary: string;
  state: OrdinaryTaskState;
}

interface OrdinaryThinkingSnapshot {
  progressPct: number;
  artifactCount: number;
  sourceCount: number;
}

function safeProgress(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(100, Math.max(0, Math.round(value)));
}

function thinkingSteps(
  snapshot: OrdinaryThinkingSnapshot,
  state: OrdinaryTaskState,
  t: TFunction,
): OrdinaryThinkingStep[] {
  // Ordinary mode only exposes fixed copy derived from public status/counts.
  // Never use current_phase, summary, or execution_log as timeline text here.
  let processingSummary = t("taskCenter.ordinarySummaryWaiting");
  if (state === "complete") {
    processingSummary = t("taskCenter.ordinaryThinkingProcessingComplete");
  } else if (state === "running") {
    processingSummary = t("taskCenter.ordinaryThinkingRunningSummary", {
      progress: safeProgress(snapshot.progressPct),
    });
  } else if (state === "failed") {
    processingSummary = t("taskCenter.ordinarySummaryFailed");
  } else if (state === "outdated") {
    processingSummary = t("taskCenter.ordinaryThinkingOutdatedSummary");
  }

  const resultDetails: string[] = [];
  if (snapshot.artifactCount > 0) {
    resultDetails.push(
      t("taskCenter.ordinarySummaryArtifacts", { count: snapshot.artifactCount }),
    );
  }
  if (snapshot.sourceCount > 0) {
    resultDetails.push(
      t("taskCenter.ordinarySummarySources", { count: snapshot.sourceCount }),
    );
  }

  const resultSummary = resultDetails.length > 0
    ? resultDetails.join(" ")
    : state === "complete"
      ? t("taskCenter.ordinaryThinkingResultComplete")
      : state === "failed"
        ? t("taskCenter.ordinaryThinkingResultFailed")
        : state === "outdated"
          ? t("taskCenter.ordinaryThinkingResultOutdated")
          : t("taskCenter.ordinaryThinkingResultPending");
  const resultState: OrdinaryTaskState = state === "complete"
    ? "complete"
    : state === "failed"
      ? "failed"
      : state === "outdated"
        ? "outdated"
        : "waiting";

  return [
    {
      id: "accepted",
      title: t("taskCenter.ordinaryThinkingAcceptedTitle"),
      summary: t("taskCenter.ordinaryThinkingAcceptedSummary"),
      state: "complete",
    },
    {
      id: "processing",
      title: t("taskCenter.ordinaryThinkingProcessingTitle"),
      summary: processingSummary,
      state,
    },
    {
      id: "result",
      title: t("taskCenter.ordinaryThinkingResultTitle"),
      summary: resultSummary,
      state: resultState,
    },
  ];
}

function OrdinaryThinkingMarker({ state }: { state: OrdinaryTaskState }) {
  if (state === "complete") return <CheckOutlined />;
  if (state === "running") {
    return <span className="ordinary-task-spinner" aria-hidden="true" />;
  }
  if (state === "failed") return <CloseCircleFilled />;
  return <span className="ordinary-thinking-dot" />;
}

function thinkingFooter(
  state: OrdinaryTaskState,
  durationSeconds: number | undefined,
  t: TFunction,
): string {
  if (state === "complete") {
    const duration = formatDuration(durationSeconds, t);
    return duration
      ? t("taskCenter.ordinaryThinkingDuration", { duration })
      : t("taskCenter.ordinaryThinkingComplete");
  }
  if (state === "running") return t("taskCenter.ordinaryThinkingRunning");
  if (state === "failed") return t("taskCenter.ordinaryThinkingFailed");
  if (state === "outdated") return t("taskCenter.ordinaryThinkingOutdated");
  return t("taskCenter.ordinaryThinkingWaiting");
}

function OrdinaryThinkingProcess({
  snapshot,
  state,
  durationSeconds,
}: {
  snapshot: OrdinaryThinkingSnapshot;
  state: OrdinaryTaskState;
  durationSeconds?: number;
}) {
  const { t } = useTranslation();
  const headingId = useId();
  const steps = useMemo(
    () => thinkingSteps(snapshot, state, t),
    [snapshot, state, t],
  );

  return (
    <section
      className="ordinary-activity-section ordinary-thinking-section"
      aria-labelledby={headingId}
    >
      <h3 className="ordinary-section-heading" id={headingId}>
        <BulbOutlined aria-hidden="true" />
        <span>{t("taskCenter.ordinaryThinking")}</span>
      </h3>
      <ol className="ordinary-thinking-list">
        {steps.map((step) => (
          <li
            className={`ordinary-thinking-item is-${step.state}`}
            key={step.id}
            aria-current={step.state === "running" ? "step" : undefined}
          >
            <span className="ordinary-thinking-marker" aria-hidden="true">
              <OrdinaryThinkingMarker state={step.state} />
            </span>
            <span className="ordinary-thinking-copy">
              <strong>{step.title}</strong>
              <span>{step.summary}</span>
              <span className="ordinary-visually-hidden">
                {stateLabel(step.state, t)}
              </span>
            </span>
          </li>
        ))}
      </ol>
      <div className={`ordinary-thinking-terminal is-${state}`}>
        <span className="ordinary-thinking-marker" aria-hidden="true">
          <OrdinaryThinkingMarker state={state} />
        </span>
        <span className="ordinary-thinking-terminal-copy">
          <strong>{thinkingFooter(state, durationSeconds, t)}</strong>
          <span>{stateLabel(state, t)}</span>
        </span>
      </div>
    </section>
  );
}

function OrdinaryReferenceSources({ sources }: { sources: ChatSource[] }) {
  const { t } = useTranslation();
  const headingId = useId();
  const displaySources = getSearchSources(sources);
  if (displaySources.length === 0) return null;

  return (
    <section
      className="ordinary-activity-section ordinary-source-section"
      aria-labelledby={headingId}
    >
      <h3 className="ordinary-section-heading" id={headingId}>
        <GlobalOutlined aria-hidden="true" />
        <span>{t("taskCenter.ordinarySources")}</span>
        <span className="ordinary-section-count">· {displaySources.length}</span>
      </h3>
      <ul className="ordinary-source-list">
        {displaySources.map((source, index) => {
          const href = getSourceHref(source);
          const label = getSourceLabel(source);
          const subtitle = getSourceSubtitle(source) || t("taskCenter.references");
          const rawEvidence = getSourceEvidenceText(source)
            ?.replace(/\s+/g, " ")
            .trim();
          const evidence = rawEvidence && rawEvidence.length > 160
            ? `${rawEvidence.slice(0, 160).trimEnd()}…`
            : rawEvidence;
          const body = (
            <>
              <span className="ordinary-source-publisher">
                <TaskSourceIcon source={source} />
                <span>{subtitle}</span>
              </span>
              <strong className="ordinary-source-title">{label}</strong>
              {evidence && (
                <span className="ordinary-source-excerpt">{evidence}</span>
              )}
            </>
          );
          return (
            <li key={getSourceDedupKey(source, index)}>
              {href.startsWith("#source-") ? (
                <div className="ordinary-source-card">{body}</div>
              ) : (
                <a
                  className="ordinary-source-card"
                  href={href}
                  target="_blank"
                  rel="noopener noreferrer"
                  aria-label={`${label}, ${t("taskCenter.ordinaryOpenNewWindow")}`}
                >
                  {body}
                </a>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function OrdinaryTaskDetails({
  task,
  state,
  durationSeconds,
}: {
  task: SubAgentTask;
  state: OrdinaryTaskState;
  durationSeconds?: number;
}) {
  const sourceCount = getSearchSources(task.sources).length;
  const snapshot = useMemo<OrdinaryThinkingSnapshot>(() => ({
    progressPct: task.progress_pct,
    artifactCount: task.artifacts.length,
    sourceCount,
  }), [sourceCount, task.artifacts.length, task.progress_pct]);
  return (
    <div className="ordinary-task-details">
      <OrdinaryThinkingProcess
        snapshot={snapshot}
        state={state}
        durationSeconds={durationSeconds}
      />
      <OrdinaryReferenceSources sources={task.sources} />
    </div>
  );
}

function itemDurationSeconds(item: OrdinaryTaskItem): number | undefined {
  if (item.startedAt === undefined || item.endedAt === undefined) return undefined;
  return Math.max(0, Math.round((item.endedAt - item.startedAt) / 1000));
}

function OrdinaryTaskCard({
  item,
  expanded,
  onToggle,
}: {
  item: OrdinaryTaskItem;
  expanded: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  const disabled = item.state === "waiting" || !item.task;
  const title = publicTaskTitle(item.task);
  const panelId = `ordinary-task-panel-${item.id.replace(/[^a-z0-9_-]/gi, "-")}`;
  const triggerId = `${panelId}-trigger`;
  return (
    <article className={`ordinary-task-card is-${item.state}${expanded ? " is-expanded" : ""}`}>
      <button
        type="button"
        id={triggerId}
        className="ordinary-task-trigger"
        onClick={onToggle}
        aria-expanded={expanded}
        aria-controls={panelId}
        disabled={disabled}
      >
        <span className="ordinary-task-marker" aria-hidden="true">
          <StateMarker state={item.state} ordinal={item.ordinal} />
        </span>
        <span className="ordinary-task-main">
          <span className="ordinary-task-title-row">
            <b>{t("taskCenter.ordinaryTaskLabel", { index: item.ordinal })}</b>
            {title && <span className="ordinary-task-title">{title}</span>}
          </span>
          <span className="ordinary-task-meta">
            {itemDurationSeconds(item) !== undefined && (
              <span>{formatDuration(itemDurationSeconds(item), t)}</span>
            )}
            <span>
              {t("taskCenter.ordinaryArtifactCount", {
                count: item.task?.artifacts.length ?? 0,
              })}
            </span>
            {item.retryCount > 0 && (
              <span>
                {t("taskCenter.ordinaryRetryCount", { count: item.retryCount })}
              </span>
            )}
            {(item.task?.input_slots?.length ?? 0) > 0 && (
              <span>
                {t("taskCenter.ordinaryDependencyCount", {
                  count: item.task?.input_slots?.length ?? 0,
                })}
              </span>
            )}
            <span className="ordinary-task-status">
              {stateLabel(item.state, t)}
            </span>
          </span>
        </span>
        <DownOutlined className="ordinary-task-arrow" aria-hidden="true" />
      </button>
      {expanded && item.task && (
        <div
          className="ordinary-task-panel"
          id={panelId}
          role="region"
          aria-labelledby={triggerId}
        >
          <OrdinaryTaskDetails
            task={item.task}
            state={item.state}
            durationSeconds={itemDurationSeconds(item)}
          />
        </div>
      )}
    </article>
  );
}

function groupState(group: OrdinaryTaskGroup): OrdinaryTaskState {
  const states = group.items.map((item) => item.state);
  if (states.includes("failed")) return "failed";
  if (states.includes("running")) return "running";
  if (states.every((state) => state === "complete")) return "complete";
  if (states.every((state) => state === "outdated")) return "outdated";
  return "waiting";
}

function OrdinaryParallelGroup({ group }: { group: OrdinaryTaskGroup }) {
  const { t } = useTranslation();
  const [selectedId, setSelectedId] = useState(group.items[0]?.id ?? "");
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const selected = group.items.find((item) => item.id === selectedId) ?? group.items[0];
  const state = groupState(group);
  const panelId = `ordinary-parallel-panel-${group.id.replace(/[^a-z0-9_-]/gi, "-")}`;
  const selectedTabId = selected
    ? `${panelId}-tab-${selected.id.replace(/[^a-z0-9_-]/gi, "-")}`
    : undefined;

  const handleTabKeyDown = (
    event: React.KeyboardEvent<HTMLButtonElement>,
    index: number,
  ) => {
    let nextIndex: number | undefined;
    if (event.key === "ArrowRight") {
      nextIndex = (index + 1) % group.items.length;
    } else if (event.key === "ArrowLeft") {
      nextIndex = (index - 1 + group.items.length) % group.items.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = group.items.length - 1;
    }
    if (nextIndex === undefined) return;
    event.preventDefault();
    setSelectedId(group.items[nextIndex].id);
    tabRefs.current[nextIndex]?.focus();
  };

  useEffect(() => {
    if (!group.items.some((item) => item.id === selectedId)) {
      setSelectedId(group.items[0]?.id ?? "");
    }
  }, [group.items, selectedId]);

  return (
    <section className={`ordinary-parallel-card is-${state}`}>
      <div className="ordinary-parallel-head">
        <span className="ordinary-parallel-symbol" aria-hidden="true">
          <StateMarker state={state} ordinal={group.items[0]?.ordinal} />
        </span>
        <strong>
          {state === "complete"
            ? t("taskCenter.ordinaryParallelComplete")
            : state === "failed"
              ? t("taskCenter.ordinaryParallelFailed")
              : state === "waiting"
                ? t("taskCenter.ordinaryParallelPending")
                : state === "outdated"
                  ? t("taskCenter.ordinaryParallelOutdated")
                  : t("taskCenter.ordinaryParallelRunning")}
        </strong>
        <span className="ordinary-parallel-status">{stateLabel(state, t)}</span>
      </div>
      <div className="ordinary-parallel-tabs" role="tablist" aria-label={t("taskCenter.ordinaryParallelLabel")}>
        {group.items.map((item, index) => (
          <button
            type="button"
            role="tab"
            key={item.id}
            id={`${panelId}-tab-${item.id.replace(/[^a-z0-9_-]/gi, "-")}`}
            aria-selected={item.id === selected?.id}
            aria-controls={panelId}
            aria-label={`${t("taskCenter.ordinaryTaskLabel", {
              index: item.ordinal,
            })}, ${stateLabel(item.state, t)}`}
            tabIndex={item.id === selected?.id ? 0 : -1}
            className={item.id === selected?.id ? "is-active" : ""}
            onClick={() => setSelectedId(item.id)}
            onKeyDown={(event) => handleTabKeyDown(event, index)}
            ref={(node) => {
              tabRefs.current[index] = node;
            }}
          >
            <span className={`ordinary-parallel-tab-state is-${item.state}`} aria-hidden="true">
              <StateMarker state={item.state} ordinal={item.ordinal} />
            </span>
            {t("taskCenter.ordinaryTaskLabel", { index: item.ordinal })}
          </button>
        ))}
      </div>
      {selected && (
        <div
          className="ordinary-parallel-panel"
          id={panelId}
          role="tabpanel"
          aria-labelledby={selectedTabId}
        >
          {publicTaskTitle(selected.task) && (
            <strong className="ordinary-parallel-task-title">
              {publicTaskTitle(selected.task)}
            </strong>
          )}
          {selected.task && (
            <OrdinaryTaskDetails
              task={selected.task}
              state={selected.state}
              durationSeconds={itemDurationSeconds(selected)}
            />
          )}
        </div>
      )}
    </section>
  );
}

function defaultOrdinaryExpandedId(items: OrdinaryTaskItem[]): string | null {
  return items.find((item) => item.state === "running" && item.task)?.id
    ?? items.find((item) => item.state === "failed" && item.task)?.id
    ?? [...items].reverse().find(
      (item) => item.state !== "waiting" && Boolean(item.task),
    )?.id
    ?? null;
}

function OrdinaryTaskCenter({
  timeline,
  onClose,
  showHeader,
  loading,
  loadError,
  onRetry,
}: {
  timeline: OrdinaryTaskTimeline;
  onClose?: () => void;
  showHeader: boolean;
  loading: boolean;
  loadError: boolean;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const initialExpanded = defaultOrdinaryExpandedId(timeline.items);
  const [expandedId, setExpandedId] = useState<string | null>(initialExpanded);
  const hadItemsRef = useRef(timeline.items.length > 0);
  const activeItem = timeline.items.find((item) => item.state === "running");

  useEffect(() => {
    if (timeline.items.length === 0) {
      hadItemsRef.current = false;
      if (expandedId !== null) setExpandedId(null);
      return;
    }

    const nextExpanded = defaultOrdinaryExpandedId(timeline.items);

    if (!hadItemsRef.current) {
      hadItemsRef.current = true;
      setExpandedId(nextExpanded);
      return;
    }

    if (expandedId && !timeline.items.some((item) => item.id === expandedId)) {
      setExpandedId(nextExpanded);
    }
  }, [expandedId, timeline.items]);

  return (
    <div className="task-center task-center--ordinary">
      {showHeader && (
        <div className="task-center-header">
          <span className="task-center-title">
            {t("taskCenter.panelTitle")}
            <span className="ordinary-task-count">{timeline.items.length}</span>
          </span>
          {onClose && (
            <button
              type="button"
              className="task-center-close-btn"
              onClick={onClose}
              aria-label={t("common.close")}
            >
              <RightOutlined />
            </button>
          )}
        </div>
      )}
      {timeline.items.length === 0 && loading ? (
        <div className="task-empty ordinary-task-loading" role="status">
          <LoadingOutlined aria-hidden="true" />
          <span>{t("taskCenter.ordinaryLoading")}</span>
        </div>
      ) : timeline.items.length === 0 && loadError ? (
        <div className="task-empty ordinary-task-error" role="alert">
          <span>{t("taskCenter.ordinaryLoadError")}</span>
          <button type="button" onClick={onRetry}>
            {t("common.retry")}
          </button>
        </div>
      ) : timeline.items.length === 0 ? (
        <div className="task-empty">{t("taskCenter.empty")}</div>
      ) : (
        <>
          {loadError && (
            <div className="ordinary-stale-warning" role="alert">
              <span>{t("taskCenter.ordinaryStaleData")}</span>
              <button type="button" onClick={onRetry}>
                {t("common.retry")}
              </button>
            </div>
          )}
          <div className="ordinary-queue-summary">
            <span
              className="ordinary-queue-summary-copy"
              role="status"
              aria-live="polite"
              aria-atomic="true"
            >
              <strong>
                {t("taskCenter.ordinaryCompletedSummary", {
                  completed: timeline.completedCount,
                  total: timeline.items.length,
                })}
              </strong>
              <span>
                {activeItem
                  ? timeline.failedCount > 0
                    ? t("taskCenter.ordinaryRunningWithFailures", {
                        count: timeline.failedCount,
                        index: activeItem.ordinal,
                      })
                    : t("taskCenter.ordinaryCurrentTask", { index: activeItem.ordinal })
                  : timeline.failedCount > 0
                    ? t("taskCenter.ordinaryFailedSummary", { count: timeline.failedCount })
                    : timeline.completedCount === timeline.items.length
                      ? t("taskCenter.ordinaryAllComplete")
                      : t("taskCenter.ordinaryIncompleteSummary", {
                          count: timeline.items.length - timeline.completedCount,
                        })}
              </span>
            </span>
            {timeline.elapsedSeconds !== undefined && (
              <time>
                {t("taskCenter.ordinaryTotalDuration", {
                  duration: formatDuration(timeline.elapsedSeconds, t),
                })}
              </time>
            )}
          </div>
          <ol className="ordinary-task-list" aria-label={t("taskCenter.ordinaryTimelineLabel")}>
            {timeline.groups.map((group) => {
              const state = groupState(group);
              const firstItem = group.items[0];
              return (
                <li
                  className={`ordinary-step-row${group.mode === "parallel" ? " is-parallel" : ""} is-${state}`}
                  key={group.id}
                >
                  <div className="ordinary-step-rail" aria-hidden="true">
                    <span className="ordinary-step-label">
                      {group.mode === "parallel"
                        ? t("taskCenter.ordinaryParallelLabel")
                        : t("taskCenter.ordinaryStepLabel", { index: firstItem.ordinal })}
                    </span>
                    <span className="ordinary-step-node">
                      <StateMarker state={state} ordinal={firstItem.ordinal} />
                    </span>
                  </div>
                  {group.mode === "parallel" ? (
                    <OrdinaryParallelGroup group={group} />
                  ) : (
                    <OrdinaryTaskCard
                      item={firstItem}
                      expanded={expandedId === firstItem.id}
                      onToggle={() =>
                        setExpandedId((current) =>
                          current === firstItem.id ? null : firstItem.id,
                        )
                      }
                    />
                  )}
                </li>
              );
            })}
          </ol>
        </>
      )}
    </div>
  );
}

type FilterKey = "all" | "running" | "succeeded" | "failed";

const TaskCenter = (props: Props) => {
  const {
    sessionId,
    onClose,
    showHeader = true,
    developerMode = false,
    workflowSteps = [],
  } = props;
  const { t } = useTranslation();
  const [filter, setFilter] = useState<FilterKey>("all");

  const tasks = useTaskCenterStore((s) =>
    sessionId ? s.tasksByConversation[sessionId] ?? EMPTY_TASKS : EMPTY_TASKS,
  );
  const loading = useTaskCenterStore((s) =>
    sessionId ? Boolean(s._loadingTasks[sessionId]) : false,
  );
  const loadError = useTaskCenterStore((s) =>
    sessionId ? Boolean(s._taskLoadErrors[sessionId]) : false,
  );
  const loadConversationTasks = useTaskCenterStore((s) => s.loadConversationTasks);
  const ordinaryTimeline = useMemo(
    () => buildOrdinaryTaskTimeline(tasks, workflowSteps),
    [tasks, workflowSteps],
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

  if (!developerMode) {
    return (
      <OrdinaryTaskCenter
        timeline={ordinaryTimeline}
        onClose={onClose}
        showHeader={showHeader}
        loading={loading}
        loadError={loadError}
        onRetry={() => {
          if (sessionId) void loadConversationTasks(sessionId);
        }}
      />
    );
  }

  return (
    <div className="task-center">
      {showHeader && <div className="task-center-header">
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
      </div>}
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
