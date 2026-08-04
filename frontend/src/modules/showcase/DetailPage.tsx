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
import { getShowcaseCase, type ShowcaseCase } from "./api";
import "./index.scss";

const REPLAY_INITIAL_DELAY_MS = 480;
const REPLAY_STEP_DELAY_MS = 420;

function prefersReducedMotion() {
  return typeof window !== "undefined"
    && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
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

function ProductDesignResult() {
  const { t } = useTranslation();
  const metrics = [
    {
      label: t("showcase.detail.productPreview.coreUsers"),
      value: t("showcase.detail.productPreview.coreUsersValue"),
      hint: t("showcase.detail.productPreview.coreUsersHint"),
      accent: true,
    },
    {
      label: t("showcase.detail.productPreview.frequentScenarios"),
      value: t("showcase.detail.productPreview.frequentScenariosValue"),
      hint: t("showcase.detail.productPreview.frequentScenariosHint"),
    },
    {
      label: t("showcase.detail.productPreview.northStar"),
      value: t("showcase.detail.productPreview.northStarValue"),
      hint: t("showcase.detail.productPreview.northStarHint"),
    },
  ];
  const paths = [1, 2, 3, 4].map((index) => ({
    label: t(`showcase.detail.productPreview.path${index}Label`),
    description: t(`showcase.detail.productPreview.path${index}Description`),
  }));
  const mechanisms = ["A", "B", "C", "D"].map((key) => ({
    key,
    description: t(`showcase.detail.productPreview.mechanism${key}`),
  }));

  return (
    <>
      <p className="showcase-document-eyebrow">
        {t("showcase.detail.productPreview.eyebrow")}
      </p>
      <h2>{t("showcase.detail.productPreview.title")}</h2>
      <p className="showcase-document-subtitle">
        {t("showcase.detail.productPreview.subtitle")}
      </p>

      <div className="showcase-document-metrics">
        {metrics.map((metric) => (
          <div className="showcase-document-metric" key={metric.label}>
            <span>{metric.label}</span>
            <strong className={metric.accent ? "is-accent" : ""}>{metric.value}</strong>
            <small>{metric.hint}</small>
          </div>
        ))}
      </div>

      <div className="showcase-document-columns">
        <section className="showcase-document-section">
          <h3>{t("showcase.detail.productPreview.corePath")}</h3>
          <ol>
            {paths.map((path, index) => (
              <li key={path.label}>
                <span>{index + 1}</span>
                <p><strong>{path.label}</strong>{path.description}</p>
              </li>
            ))}
          </ol>
        </section>
        <section className="showcase-document-section showcase-document-mechanisms">
          <h3>{t("showcase.detail.productPreview.keyMechanisms")}</h3>
          <ul>
            {mechanisms.map((mechanism) => (
              <li key={mechanism.key}>
                <span>{mechanism.key}</span>
                <p>{mechanism.description}</p>
              </li>
            ))}
          </ul>
        </section>
      </div>

      <p className="showcase-document-deliverables">
        {t("showcase.detail.productPreview.deliverables")}
      </p>
    </>
  );
}

function GenericResult({ item }: { item: ShowcaseCase }) {
  const { t } = useTranslation();

  return (
    <>
      <p className="showcase-document-eyebrow">{item.output_label}</p>
      <h2>{item.title}</h2>
      <p className="showcase-document-subtitle">{item.result_summary}</p>
      <div className="showcase-generic-result">
        <section className="showcase-document-section">
          <h3>{t("showcase.executionFlow")}</h3>
          <ol>
            {item.steps.map((step, index) => (
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
            {item.result_highlights.map((highlight, index) => (
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

export default function DetailPage() {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage || i18n.language;
  const { caseId = "" } = useParams();
  const navigate = useNavigate();
  const resultPanelRef = useRef<HTMLElement>(null);
  const [item, setItem] = useState<ShowcaseCase | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [visibleSteps, setVisibleSteps] = useState(0);
  const [isAnimationSkipped, setIsAnimationSkipped] = useState(false);
  const [isResultExpanded, setIsResultExpanded] = useState(false);
  const [selectedTaskId, setSelectedTaskId] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    setIsLoading(true);
    setHasError(false);
    getShowcaseCase(caseId, { signal: controller.signal })
      .then(setItem)
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
    const shouldReduceMotion = prefersReducedMotion();
    setVisibleSteps(shouldReduceMotion ? (item?.steps.length ?? 0) : 0);
    setIsAnimationSkipped(Boolean(shouldReduceMotion));
    setIsResultExpanded(false);
    setSelectedTaskId(item?.tasks?.[0]?.id ?? "");
  }, [item]);

  useEffect(() => {
    if (!item || isAnimationSkipped || visibleSteps >= item.steps.length) {
      return;
    }

    const delay = visibleSteps === 0 ? REPLAY_INITIAL_DELAY_MS : REPLAY_STEP_DELAY_MS;
    const timer = window.setTimeout(() => {
      setVisibleSteps((current) => Math.min(current + 1, item.steps.length));
    }, delay);
    return () => window.clearTimeout(timer);
  }, [isAnimationSkipped, item, visibleSteps]);

  if (isLoading) {
    return <main className="showcase-page showcase-empty" role="status">{t("showcase.loadingDetail")}</main>;
  }

  if (hasError || !item) {
    return (
      <main className="showcase-page showcase-empty" role="alert">
        <strong>{t("showcase.detailLoadError")}</strong>
        <Link to="/agent/chat/cases">{t("showcase.backToGallery")}</Link>
      </main>
    );
  }

  const startCase = () => {
    const params = new URLSearchParams({ showcase_case: item.id });
    if (selectedTaskId) {
      params.set("showcase_task", selectedTaskId);
    }
    navigate(`/agent/chat/home?${params.toString()}`);
  };
  const showFullResult = () => {
    setVisibleSteps(item.steps.length);
    setIsAnimationSkipped(true);
    resultPanelRef.current?.scrollIntoView?.({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
      block: "nearest",
    });
  };
  const toggleResultExpanded = () => {
    if (visibleSteps < item.steps.length) {
      setVisibleSteps(item.steps.length);
      setIsAnimationSkipped(true);
    }
    setIsResultExpanded((current) => !current);
  };
  const isReplayComplete = visibleSteps >= item.steps.length;
  const displayTitle = item.id === "aiProduct"
    ? t("showcase.detail.productTitle")
    : item.title;
  const displayDescription = item.id === "aiProduct"
    ? t("showcase.detail.productDescription")
    : item.description;

  return (
    <main className={`showcase-page showcase-detail-page${isResultExpanded ? " is-result-expanded" : ""}${isAnimationSkipped ? " is-animation-skipped" : ""}${isReplayComplete ? " is-animation-complete" : ""}`}>
      <header className="showcase-detail-header">
        <Link to="/agent/chat/cases" className="showcase-detail-back-link">
          <ArrowLeftOutlined aria-hidden="true" />
          {t("showcase.detail.back")}
        </Link>
        <div className="showcase-detail-heading">
          <h1>{displayTitle}</h1>
          <p>{displayDescription}</p>
        </div>
        <button type="button" className="showcase-detail-try-button" onClick={startCase}>
          {t("showcase.try")}
        </button>
      </header>

      {item.tasks?.length ? (
        <section className="showcase-detail-tasks" aria-labelledby="showcase-task-title">
          <h2 id="showcase-task-title">{t("showcase.chooseTask")}</h2>
          <div className="showcase-task-grid">
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
              <p>{item.prompt_short}</p>
            </div>
            <ol className="showcase-replay-steps" aria-label={t("showcase.executionFlow")}>
              {item.steps.map((step, index) => {
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
              <span>{item.output_label}</span>
            </div>
            <button type="button" onClick={toggleResultExpanded}>
              {isResultExpanded ? t("showcase.detail.exitFullscreen") : t("showcase.detail.viewFullscreen")}
              {isResultExpanded ? <CompressOutlined aria-hidden="true" /> : <ExpandOutlined aria-hidden="true" />}
            </button>
          </header>
          <div className="showcase-result-body">
            {isReplayComplete ? (
              <div className="showcase-result-document showcase-result-document-enter">
                {item.id === "aiProduct" ? <ProductDesignResult /> : <GenericResult item={item} />}
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
