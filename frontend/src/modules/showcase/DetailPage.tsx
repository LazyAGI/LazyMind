import { useEffect, useRef, useState } from "react";
import {
  ArrowLeftOutlined,
  ArrowRightOutlined,
  CheckOutlined,
  CompressOutlined,
  ExpandOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  getShowcaseCase,
  type ShowcaseCase,
  type ShowcaseCaseResult,
  type ShowcaseCaseTask,
} from "./api";
import { buildShowcaseLaunchPath } from "./classification";
import "./index.scss";

const REPLAY_INITIAL_DELAY_MS = 480;
const REPLAY_STEP_DELAY_MS = 420;
const WHEEL_DELTA_LINE = 1;
const WHEEL_DELTA_PAGE = 2;

function prefersReducedMotion() {
  return typeof window !== "undefined"
    && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

function scrollTaskListHorizontally(taskList: HTMLDivElement, event: WheelEvent) {
  const maxScrollLeft = taskList.scrollWidth - taskList.clientWidth;
  if (maxScrollLeft <= 0) {
    return;
  }

  let delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
  if (event.deltaMode === WHEEL_DELTA_LINE) {
    delta *= 16;
  } else if (event.deltaMode === WHEEL_DELTA_PAGE) {
    delta *= taskList.clientWidth;
  }

  const nextScrollLeft = Math.min(maxScrollLeft, Math.max(0, taskList.scrollLeft + delta));
  if (nextScrollLeft === taskList.scrollLeft) {
    return;
  }

  event.preventDefault();
  taskList.scrollLeft = nextScrollLeft;
}

function ResultSkeleton({ label }: { label: string }) {
  return (
    <div className="showcase-result-document showcase-result-skeleton" role="status" aria-live="polite">
      <span className="sr-only">{label}</span>
      <div className="showcase-result-skeleton-content" aria-hidden="true">
        <span className="showcase-skeleton-line is-short" />
        <span className="showcase-skeleton-line is-title" />
        <span className="showcase-skeleton-line is-subtitle" />
        <div className="showcase-skeleton-metrics">
          <span /><span /><span />
        </div>
        <div className="showcase-skeleton-columns">
          <span /><span />
        </div>
        <span className="showcase-skeleton-line is-footer" />
      </div>
    </div>
  );
}

function ProductReportResult({ result }: { result: ShowcaseCaseResult }) {
  const report = result.product_report!;
  return (
    <>
      <p className="showcase-document-eyebrow">{result.eyebrow}</p>
      <h2>{result.title}</h2>
      <p className="showcase-document-subtitle">{result.summary}</p>

      <div className="showcase-document-metrics">
        {(report.metrics || []).map((metric) => (
          <div className="showcase-document-metric" key={metric.label}>
            <span>{metric.label}</span>
            <strong className={metric.accent ? "is-accent" : ""}>{metric.value}</strong>
            <small>{metric.hint}</small>
          </div>
        ))}
      </div>

      <div className="showcase-document-columns">
        {(report.sections || []).map((section) => {
          const List = section.marker === "number" ? "ol" : "ul";
          return (
            <section
              className={`showcase-document-section${section.marker === "letter" ? " showcase-document-mechanisms" : ""}`}
              key={section.title}
            >
              <h3>{section.title}</h3>
              <List>
                {(section.items || []).map((entry, index) => (
                  <li key={`${section.title}-${index}`}>
                    <span>{section.marker === "number" ? index + 1 : String.fromCharCode(65 + index)}</span>
                    <p><strong>{entry.label}</strong>{entry.description}</p>
                  </li>
                ))}
              </List>
            </section>
          );
        })}
      </div>

      <p className="showcase-document-deliverables">{report.deliverables}</p>
    </>
  );
}

function GenericResult({ result, steps }: {
  result: ShowcaseCaseResult;
  steps: NonNullable<ShowcaseCaseTask["steps"]>;
}) {
  const { t } = useTranslation();

  return (
    <>
      <p className="showcase-document-eyebrow">{result.eyebrow}</p>
      <h2>{result.title}</h2>
      <p className="showcase-document-subtitle">{result.summary}</p>
      {result.image_url ? (
        <img className="showcase-document-preview-image" src={result.image_url} alt="" />
      ) : null}
      <div className="showcase-generic-result">
        <section className="showcase-document-section">
          <h3>{t("showcase.executionFlow")}</h3>
          <ol>
            {steps.map((step, index) => (
              <li key={`${step.title}-${index}`}>
                <span>{index + 1}</span>
                <p><strong>{step.title}</strong>{step.description}</p>
              </li>
            ))}
          </ol>
        </section>
        <section className="showcase-document-section showcase-document-mechanisms">
          <h3>{t("showcase.youWillGet")}</h3>
          <ul>
            {result.highlights?.map((highlight, index) => (
              <li key={highlight}>
                <span>{String.fromCharCode(65 + index)}</span>
                <p>{highlight}</p>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </>
  );
}

function HTMLPreviewResult({ url, title }: {
  url: string;
  title: string;
}) {
  return (
    <iframe
      className="showcase-html-preview"
      src={url}
      sandbox="allow-scripts"
      loading="lazy"
      referrerPolicy="no-referrer"
      title={title}
    />
  );
}

function ResultContent({ result, steps }: {
  result: ShowcaseCaseResult;
  steps: NonNullable<ShowcaseCaseTask["steps"]>;
}) {
  if (result.template === "html_preview_v1" && result.html_url) {
    return (
      <HTMLPreviewResult
        url={result.html_url}
        title={result.title}
      />
    );
  }
  if (result.template === "product_report_v1" && result.product_report) {
    return <ProductReportResult result={result} />;
  }
  return <GenericResult result={result} steps={steps} />;
}

export default function DetailPage() {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const { caseId = "" } = useParams();
  const navigate = useNavigate();
  const resultPanelRef = useRef<HTMLElement>(null);
  const taskSectionRef = useRef<HTMLElement>(null);
  const taskListRef = useRef<HTMLDivElement>(null);
  const [item, setItem] = useState<ShowcaseCase | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [visibleSteps, setVisibleSteps] = useState(0);
  const [isAnimationSkipped, setIsAnimationSkipped] = useState(false);
  const [isResultExpanded, setIsResultExpanded] = useState(false);
  const [selectedTaskId, setSelectedTaskId] = useState("");
  const selectedTask = item?.tasks?.find((task) => task.id === selectedTaskId) || item?.tasks?.[0];
  const replaySteps = selectedTask?.steps || [];

  useEffect(() => {
    const controller = new AbortController();
    setIsLoading(true);
    setHasError(false);
    getShowcaseCase(caseId, { signal: controller.signal })
      .then((nextItem) => {
        setItem(nextItem);
        setIsResultExpanded(false);
        setSelectedTaskId(nextItem.tasks?.[0]?.id ?? "");
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setHasError(true);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setIsLoading(false);
        }
      });
    return () => controller.abort();
  }, [caseId, locale]);

  useEffect(() => {
    const taskSection = taskSectionRef.current;
    const taskList = taskListRef.current;
    if (!taskSection || !taskList) {
      return;
    }

    const handleWheel = (event: WheelEvent) => scrollTaskListHorizontally(taskList, event);
    taskSection.addEventListener("wheel", handleWheel, { passive: false });
    return () => taskSection.removeEventListener("wheel", handleWheel);
  }, [isLoading, item?.id]);

  useEffect(() => {
    const shouldReduceMotion = prefersReducedMotion();
    setVisibleSteps(shouldReduceMotion ? replaySteps.length : 0);
    setIsAnimationSkipped(Boolean(shouldReduceMotion));
  }, [item?.id, replaySteps.length, selectedTaskId]);

  useEffect(() => {
    if (!item || isAnimationSkipped || visibleSteps >= replaySteps.length) {
      return;
    }

    const delay = visibleSteps === 0 ? REPLAY_INITIAL_DELAY_MS : REPLAY_STEP_DELAY_MS;
    const timer = window.setTimeout(() => {
      setVisibleSteps((current) => Math.min(current + 1, replaySteps.length));
    }, delay);
    return () => window.clearTimeout(timer);
  }, [isAnimationSkipped, item, replaySteps.length, visibleSteps]);

  if (isLoading) {
    return <main className="showcase-page showcase-empty" role="status">{t("showcase.loadingDetail")}</main>;
  }

  if (hasError || !item || !selectedTask) {
    return (
      <main className="showcase-page showcase-empty" role="alert">
        <strong>{t("showcase.detailLoadError")}</strong>
        <Link to="/agent/chat/cases">{t("showcase.backToGallery")}</Link>
      </main>
    );
  }

  const startCase = () => {
    navigate(buildShowcaseLaunchPath(item.id, item.type, selectedTaskId));
  };
  const showFullResult = () => {
    setVisibleSteps(replaySteps.length);
    setIsAnimationSkipped(true);
    resultPanelRef.current?.scrollIntoView?.({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
      block: "nearest",
    });
  };
  const toggleResultExpanded = () => {
    if (visibleSteps < replaySteps.length) {
      setVisibleSteps(replaySteps.length);
      setIsAnimationSkipped(true);
    }
    setIsResultExpanded((current) => !current);
  };
  const isReplayComplete = visibleSteps >= replaySteps.length;
  const activeResult = selectedTask.result;
  const sourceURL = item.source_url.trim();
  const hasExternalSource = /^https?:\/\//i.test(sourceURL);

  return (
    <main className={`showcase-page showcase-detail-page${isResultExpanded ? " is-result-expanded" : ""}${isAnimationSkipped ? " is-animation-skipped" : ""}${isReplayComplete ? " is-animation-complete" : ""}`}>
      <header className="showcase-detail-header">
        <Link to="/agent/chat/cases" className="showcase-detail-back-link">
          <ArrowLeftOutlined aria-hidden="true" />
          {t("showcase.detail.back")}
        </Link>
        <div className="showcase-detail-heading">
          <h1>
            {hasExternalSource ? (
              <a
                className="showcase-detail-source-link"
                href={sourceURL}
                target="_blank"
                rel="noreferrer"
              >
                {item.detail_title}
              </a>
            ) : item.detail_title}
          </h1>
          <p>{item.detail_description}</p>
        </div>
        <button type="button" className="showcase-detail-try-button" onClick={startCase}>
          {t("showcase.try")}
        </button>
      </header>

      {(item.tasks?.length || 0) > 1 ? (
        <section
          ref={taskSectionRef}
          className="showcase-detail-tasks"
          aria-labelledby="showcase-task-title"
        >
          <h2 id="showcase-task-title">{t("showcase.chooseTask")}</h2>
          <div
            ref={taskListRef}
            className="showcase-task-grid"
            role="group"
            aria-labelledby="showcase-task-title"
            tabIndex={0}
          >
            {item.tasks.map((task, index) => (
              <button
                type="button"
                key={task.id}
                className={`showcase-task-card${selectedTaskId === task.id ? " is-selected" : ""}`}
                onClick={() => setSelectedTaskId(task.id)}
                aria-pressed={selectedTaskId === task.id}
              >
                <span className="showcase-task-card-heading">
                  <span className="showcase-task-card-index" aria-hidden="true">
                    {String(index + 1).padStart(2, "0")}
                  </span>
                  <strong>{task.title}</strong>
                </span>
                <p>{task.description}</p>
                {task.output_label ? (
                  <span className="showcase-task-output">
                    {t("showcase.detail.outputLabel", { output: task.output_label })}
                  </span>
                ) : null}
              </button>
            ))}
          </div>
        </section>
      ) : null}

      <section className="showcase-detail-workbench">
        <article className="showcase-workbench-panel showcase-replay-panel">
          <header className="showcase-panel-header">
            <div>
              <h2>{t("showcase.detail.taskReplay")}</h2>
              <span>{t("showcase.detail.demo")}</span>
            </div>
            <button type="button" onClick={showFullResult}>
              {t("showcase.detail.viewResult")}
              <ArrowRightOutlined aria-hidden="true" />
            </button>
          </header>
          <div className="showcase-replay-body">
            <div className="showcase-user-task">
              <strong><UserOutlined aria-hidden="true" />{t("showcase.detail.userTask")}</strong>
              <p>{selectedTask.prompt_short}</p>
            </div>
            <ol className="showcase-replay-steps" aria-label={t("showcase.executionFlow")}>
              {replaySteps.map((step, index) => {
                const isVisible = index < visibleSteps;
                const isActive = !isReplayComplete && index === visibleSteps;
                return (
                  <li className={`${isVisible ? "is-visible" : ""}${isActive ? " is-active" : ""}`} key={`${step.title}-${index}`}>
                    <span className="showcase-replay-step-marker">
                      {isVisible ? <CheckOutlined aria-hidden="true" /> : index + 1}
                    </span>
                    <div>
                      <strong>{step.title}</strong>
                      <p>{step.description}</p>
                    </div>
                  </li>
                );
              })}
            </ol>
          </div>
        </article>

        <article
          className="showcase-workbench-panel showcase-result-panel"
          ref={resultPanelRef}
          aria-busy={!isReplayComplete}
        >
          <header className="showcase-panel-header">
            <div>
              <h2>{t("showcase.detail.finalOutput")}</h2>
              <span>{selectedTask.output_label}</span>
            </div>
            <button type="button" onClick={toggleResultExpanded}>
              {isResultExpanded ? t("showcase.detail.exitFullscreen") : t("showcase.detail.viewFullscreen")}
              {isResultExpanded ? <CompressOutlined aria-hidden="true" /> : <ExpandOutlined aria-hidden="true" />}
            </button>
          </header>
          <div className={`showcase-result-body${activeResult.template === "html_preview_v1" ? " is-html-preview" : ""}`}>
            {isReplayComplete ? (
              <div className={`showcase-result-document showcase-result-document-enter${activeResult.template === "html_preview_v1" ? " is-html-preview" : ""}`}>
                <ResultContent result={activeResult} steps={replaySteps} />
              </div>
            ) : (
              <ResultSkeleton label={t("showcase.detail.generatingResult")} />
            )}
          </div>
        </article>
      </section>
    </main>
  );
}
