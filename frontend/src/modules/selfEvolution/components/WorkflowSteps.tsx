import { type MouseEvent, type ReactNode } from "react";
import { Collapse, Typography } from "antd";
import {
  CheckCircleFilled,
  ClockCircleFilled,
  CloseOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { type SelfEvolutionWorkflowStep } from "./types";

const { Paragraph, Text } = Typography;

type WorkflowStepCardProps = {
  step: SelfEvolutionWorkflowStep;
  index: number;
  statusLabel: string;
  runtimeSummary?: ReactNode;
  children?: ReactNode;
};

export function WorkflowStepCard({
  step,
  index,
  statusLabel,
  runtimeSummary,
  children,
}: WorkflowStepCardProps) {
  const { t } = useTranslation();
  return (
    <article
      className={`self-evolution-step-card is-${step.status}`}
      style={{ animationDelay: `${index * 70}ms` }}
    >
      <div className="self-evolution-step-main">
        <div className="self-evolution-step-title-row">
          <Text className="self-evolution-step-title">{step.title}</Text>
          <span className={`self-evolution-step-status is-${step.status}`}>
            {step.status === "done" && <CheckCircleFilled />}
            {step.status === "running" && <ClockCircleFilled />}
            {step.status === "paused" && <ClockCircleFilled />}
            {step.status === "canceled" && <CloseOutlined />}
            {step.status === "pending" && <FileTextOutlined />}
            <span>{statusLabel}</span>
          </span>
        </div>
        <Paragraph className="self-evolution-step-desc">{step.desc}</Paragraph>
        {step.progressPhases && step.progressPhases.length > 0 && (
          <div className="self-evolution-step-progress-phases" aria-label={t("selfEvolutionRun.stepProgressPhasesAria", { title: step.title })}>
            {step.progressPhases.map((phase) => (
              <div className="self-evolution-step-progress-phase" key={phase.id}>
                <div className="self-evolution-step-progress-phase-head">
                  <span>
                    <Text className="self-evolution-step-progress-phase-title">{phase.title}</Text>
                    <Text className="self-evolution-step-progress-phase-desc">{phase.desc}</Text>
                  </span>
                  <strong>{`${phase.percent}%`}</strong>
                </div>
                <div className="self-evolution-step-progress-meta">
                  <span>{t("selfEvolutionRun.stepProgressStatusLabel", { status: phase.statusText })}</span>
                </div>
                <div className={`self-evolution-step-progress-track is-${phase.id}`}>
                  <span style={{ width: `${phase.percent}%` }} />
                </div>
              </div>
            ))}
          </div>
        )}
        {step.progress && !step.progressPhases?.length && (
          <div className="self-evolution-step-progress" aria-label={t("selfEvolutionRun.stepProgressAria", { title: step.title })}>
            <div className="self-evolution-step-progress-meta">
              <span>{t("selfEvolutionRun.stepProgressStatusLabel", { status: step.progress.statusText })}</span>
              <strong>{`${step.progress.percent}%`}</strong>
            </div>
            <div className="self-evolution-step-progress-track">
              <span style={{ width: `${step.progress.percent}%` }} />
            </div>
          </div>
        )}
        {runtimeSummary}
        {step.runtimeText && !runtimeSummary && (
          <Paragraph className="self-evolution-step-runtime">{step.runtimeText}</Paragraph>
        )}
        {children}
      </div>
    </article>
  );
}

type DatasetWorkflowStepProps = {
  downloadUrl: string;
  fallbackDownloadUrl: string;
  fileName: string;
  getDownloadFileName: (url: string, fallbackName: string) => string;
  onDownload: (event: MouseEvent<HTMLAnchorElement>) => void;
};

export function DatasetWorkflowStep({
  downloadUrl,
  fallbackDownloadUrl,
  fileName,
  getDownloadFileName,
  onDownload,
}: DatasetWorkflowStepProps) {
  const { t } = useTranslation();
  const href = downloadUrl || fallbackDownloadUrl || undefined;

  return (
    <section className="self-evolution-dataset-static-block" aria-label={t("selfEvolutionRun.datasetResultAria")}>
      <div className="self-evolution-dataset-static-head">
        <span>{t("selfEvolutionRun.datasetResultDownloadOnly")}</span>
        <a
          className="self-evolution-dataset-download-link"
          href={href}
          download={getDownloadFileName(downloadUrl || fallbackDownloadUrl, fileName)}
          onClick={onDownload}
        >
          {t("selfEvolutionRun.downloadView")}
        </a>
      </div>
    </section>
  );
}

type PxReportWorkflowStepProps = {
  categoryCount: number;
  isSingleCategory: boolean;
  downloadUrl: string;
  onCollapseChange: (activeKeys: string | string[]) => void;
  onDownload: (event: MouseEvent<HTMLAnchorElement>) => void;
  getDownloadFileName: (url: string, fallbackName: string) => string;
  children: ReactNode;
};

export function PxReportWorkflowStep({
  categoryCount,
  isSingleCategory,
  downloadUrl,
  onCollapseChange,
  onDownload,
  getDownloadFileName,
  children,
}: PxReportWorkflowStepProps) {
  const { t } = useTranslation();
  return (
    <Collapse
      className="self-evolution-dataset-collapse self-evolution-px-collapse"
      bordered={false}
      onChange={onCollapseChange}
      items={[
        {
          key: "px-report-preview",
          label: (
            <span className="self-evolution-dataset-collapse-label">
              <span>
                {categoryCount === 0
                  ? t("selfEvolutionRun.viewEvalChart")
                  : isSingleCategory
                    ? t("selfEvolutionRun.viewEvalChartSingle")
                    : t("selfEvolutionRun.viewEvalChartMulti")}
              </span>
              <a
                className="self-evolution-dataset-download-link"
                href={downloadUrl || undefined}
                download={getDownloadFileName(downloadUrl, "eval-report.json")}
                onClick={onDownload}
              >
                {t("selfEvolutionRun.downloadView")}
              </a>
            </span>
          ),
          children,
        },
      ]}
    />
  );
}

type AnalysisWorkflowStepProps = {
  onCollapseChange: (activeKeys: string | string[]) => void;
  children: ReactNode;
};

export function AnalysisWorkflowStep({ onCollapseChange, children }: AnalysisWorkflowStepProps) {
  const { t } = useTranslation();
  return (
    <Collapse
      className="self-evolution-dataset-collapse self-evolution-analysis-collapse"
      bordered={false}
      onChange={onCollapseChange}
      items={[
        {
          key: "analysis-report-preview",
          label: t("selfEvolutionRun.viewFullAnalysisReport"),
          children,
        },
      ]}
    />
  );
}

type CodeOptimizeWorkflowStepProps = {
  downloadUrl: string;
  onCollapseChange: (activeKeys: string | string[]) => void;
  onDownload: (event: MouseEvent<HTMLAnchorElement>) => void;
  getDownloadFileName: (url: string, fallbackName: string) => string;
  children: ReactNode;
};

export function CodeOptimizeWorkflowStep({
  downloadUrl,
  onCollapseChange,
  onDownload,
  getDownloadFileName,
  children,
}: CodeOptimizeWorkflowStepProps) {
  const { t } = useTranslation();
  return (
    <Collapse
      className="self-evolution-dataset-collapse self-evolution-optimize-collapse"
      bordered={false}
      onChange={onCollapseChange}
      items={[
        {
          key: "code-optimize-diff-preview",
          label: (
            <span className="self-evolution-dataset-collapse-label">
              <span>{t("selfEvolutionRun.viewCodeChanges")}</span>
              <a
                className="self-evolution-dataset-download-link"
                href={downloadUrl || undefined}
                download={getDownloadFileName(downloadUrl, "code-diff.diff")}
                onClick={onDownload}
              >
                {t("selfEvolutionRun.downloadView")}
              </a>
            </span>
          ),
          children,
        },
      ]}
    />
  );
}

type AbTestWorkflowStepProps = {
  downloadUrl: string;
  fallbackDownloadUrl: string;
  onCollapseChange: (activeKeys: string | string[]) => void;
  onDownload: (event: MouseEvent<HTMLAnchorElement>) => void;
  getDownloadFileName: (url: string, fallbackName: string) => string;
  children: ReactNode;
};

export function AbTestWorkflowStep({
  downloadUrl,
  fallbackDownloadUrl,
  onCollapseChange,
  onDownload,
  getDownloadFileName,
  children,
}: AbTestWorkflowStepProps) {
  const { t } = useTranslation();
  const href = downloadUrl || fallbackDownloadUrl;

  return (
    <Collapse
      className="self-evolution-dataset-collapse self-evolution-ab-collapse"
      bordered={false}
      onChange={onCollapseChange}
      items={[
        {
          key: "ab-test-preview",
          label: (
            <span className="self-evolution-dataset-collapse-label">
              <span>{t("selfEvolutionRun.viewABTestDetail")}</span>
              <a
                className="self-evolution-dataset-download-link"
                href={href || undefined}
                download={getDownloadFileName(href, "ab-test-comparison.json")}
                onClick={onDownload}
              >
                {t("selfEvolutionRun.downloadView")}
              </a>
            </span>
          ),
          children,
        },
      ]}
    />
  );
}
