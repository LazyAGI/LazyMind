import { type MouseEvent, type ReactNode } from "react";
import { Collapse, Typography } from "antd";
import {
  CheckCircleFilled,
  ClockCircleFilled,
  CloseOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
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
          <div className="self-evolution-step-progress-phases" aria-label={`${step.title}分阶段进度`}>
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
                  <span>{`状态：${phase.statusText}`}</span>
                </div>
                <div className={`self-evolution-step-progress-track is-${phase.id}`}>
                  <span style={{ width: `${phase.percent}%` }} />
                </div>
              </div>
            ))}
          </div>
        )}
        {step.progress && !step.progressPhases?.length && (
          <div className="self-evolution-step-progress" aria-label={`${step.title}进度`}>
            <div className="self-evolution-step-progress-meta">
              <span>{`状态：${step.progress.statusText}`}</span>
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
  const href = downloadUrl || fallbackDownloadUrl || undefined;

  return (
    <section className="self-evolution-dataset-static-block" aria-label="数据集结果展示">
      <div className="self-evolution-dataset-static-head">
        <span>数据集结果仅支持下载查看</span>
        <a
          className="self-evolution-dataset-download-link"
          href={href}
          download={getDownloadFileName(downloadUrl || fallbackDownloadUrl, fileName)}
          onClick={onDownload}
        >
          下载查看
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
                  ? "查看评测图表"
                  : isSingleCategory
                    ? "查看评测图表（单分类饼图）"
                    : "查看评测图表（多分类折线图）"}
              </span>
              <a
                className="self-evolution-dataset-download-link"
                href={downloadUrl || undefined}
                download={getDownloadFileName(downloadUrl, "eval-report.json")}
                onClick={onDownload}
              >
                下载查看
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
  return (
    <Collapse
      className="self-evolution-dataset-collapse self-evolution-analysis-collapse"
      bordered={false}
      onChange={onCollapseChange}
      items={[
        {
          key: "analysis-report-preview",
          label: "查看完整分析报告",
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
              <span>查看代码改动详情</span>
              <a
                className="self-evolution-dataset-download-link"
                href={downloadUrl || undefined}
                download={getDownloadFileName(downloadUrl, "code-diff.diff")}
                onClick={onDownload}
              >
                下载查看
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
              <span>查看 ABTest 详情</span>
              <a
                className="self-evolution-dataset-download-link"
                href={href || undefined}
                download={getDownloadFileName(href, "ab-test-comparison.json")}
                onClick={onDownload}
              >
                下载查看
              </a>
            </span>
          ),
          children,
        },
      ]}
    />
  );
}
