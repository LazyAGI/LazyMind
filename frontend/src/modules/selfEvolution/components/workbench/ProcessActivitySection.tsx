import { useState, type WheelEvent } from "react";
import { Typography } from "antd";
import { useTranslation } from "react-i18next";
import type {
  EvoCaseProgressItem,
  EvoProcessDashboard,
  WorkflowResultKind,
  WorkflowStep as SelfEvolutionRuntimeWorkflowStep,
} from "../../shared";

const { Paragraph, Text } = Typography;

type CaseProgressGroup = EvoProcessDashboard["caseProgressGroups"][number];

export function ProcessActivitySection({
  processDashboard,
  activeCaseProgressGroup,
  selectedStageActivities,
  visibleKeyActivities,
  activeStageLabel,
  getStepStatusLabel,
  onOpenArtifact,
  onOpenCaseArtifact,
}: {
  processDashboard: EvoProcessDashboard;
  activeCaseProgressGroup?: CaseProgressGroup;
  selectedStageActivities: EvoProcessDashboard["recentActivities"];
  visibleKeyActivities: EvoProcessDashboard["recentActivities"];
  activeStageLabel: string;
  getStepStatusLabel: (status: SelfEvolutionRuntimeWorkflowStep["status"]) => string;
  onOpenArtifact: (kind: WorkflowResultKind) => void;
  onOpenCaseArtifact: (kind: WorkflowResultKind, artifactId: string, title: string, caseId?: string) => void;
}) {
  const { t } = useTranslation();
  const [caseProgressPageByStage, setCaseProgressPageByStage] = useState<Record<string, number>>({});

  const handleActivityListWheel = (event: WheelEvent<HTMLDivElement>) => {
    const maxScrollTop = event.currentTarget.scrollHeight - event.currentTarget.clientHeight;
    if (maxScrollTop <= 0 || event.deltaY === 0) return;
    const nextScrollTop = Math.max(0, Math.min(maxScrollTop, event.currentTarget.scrollTop + event.deltaY));
    if (nextScrollTop === event.currentTarget.scrollTop) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.scrollTop = nextScrollTop;
  };

  const renderActivityRows = (activities: EvoProcessDashboard["recentActivities"], emptyText: string) => (
    activities.length === 0 ? (
      <Paragraph className="self-evolution-process-activity-empty">
        {emptyText}
      </Paragraph>
    ) : (
      activities.map((item) => {
        const activityStageDone = item.stage && processDashboard.overview.find((overviewItem) => overviewItem.stage === item.stage)?.step.status === "done";
        return (
          <div key={item.key} className={`self-evolution-process-activity-row is-${item.tone}`}>
            <span className="self-evolution-process-activity-dot" />
            <div className="self-evolution-process-activity-content">
              <div className="self-evolution-process-activity-title">
                <strong>{item.title}</strong>
                <span>{item.time}</span>
              </div>
              <Paragraph>{item.detail}</Paragraph>
            </div>
            {item.artifactKind && activityStageDone && (
              <button
                type="button"
                className="self-evolution-process-activity-action"
                onClick={(event) => {
                  event.stopPropagation();
                  onOpenArtifact(item.artifactKind!);
                }}
              >
                {item.artifactLabel || t("selfEvolutionRun.viewActivity")}
              </button>
            )}
          </div>
        );
      })
    )
  );

  const renderCaseProgressRow = (item: EvoCaseProgressItem) => (
    <div key={item.caseId} className={`self-evolution-case-row is-${item.status}`}>
      <strong className="self-evolution-case-title">{item.title}</strong>
      <div className="self-evolution-case-step-list" aria-label={t("selfEvolutionRun.caseProgressAria", { caseId: item.caseId })}>
        {item.steps.map((step) => (
          <span key={step.key} className={`self-evolution-case-step is-${step.status}`} title={`${step.label} · ${getStepStatusLabel(step.status)}`}>
            {step.label}
          </span>
        ))}
      </div>
      <span className="self-evolution-case-count">{`${item.completed}/${item.total}`}</span>
      <span className={`self-evolution-case-status is-${item.status}`}>{getStepStatusLabel(item.status)}</span>
      <button
        type="button"
        disabled={!item.artifactId}
        title={item.artifactLabel}
        onClick={(event) => {
          event.stopPropagation();
          if (item.artifactId) {
            onOpenCaseArtifact(item.artifactKind, item.artifactId, `${item.title} · ${item.artifactLabel}`, item.caseId);
          }
        }}
      >
        {t("selfEvolutionRun.viewDetail")}
      </button>
    </div>
  );

  const renderCaseProgressPanel = () => {
    if (!activeCaseProgressGroup) {
      return renderActivityRows(selectedStageActivities.length ? selectedStageActivities : visibleKeyActivities, t("selfEvolutionRun.activityEmptyDefault"));
    }
    const pageSize = activeCaseProgressGroup.pageSize;
    const totalPages = Math.max(1, Math.ceil(activeCaseProgressGroup.cases.length / pageSize));
    const currentPage = Math.min(caseProgressPageByStage[activeCaseProgressGroup.stage] || 1, totalPages);
    const pageCases = activeCaseProgressGroup.cases.slice((currentPage - 1) * pageSize, currentPage * pageSize);
    const completedCases = activeCaseProgressGroup.cases.filter((item) => item.status === "done").length;
    const setPage = (page: number) => setCaseProgressPageByStage((prev) => ({ ...prev, [activeCaseProgressGroup.stage]: Math.max(1, Math.min(totalPages, page)) }));
    return (
      <div className="self-evolution-case-progress">
        <div className="self-evolution-case-progress-summary">
          <span>{t("selfEvolutionRun.caseCompletedSummary", { title: activeCaseProgressGroup.title, completed: completedCases, total: activeCaseProgressGroup.cases.length })}</span>
          <div className="self-evolution-case-progress-pager">
            <button type="button" disabled={currentPage <= 1} onClick={() => setPage(currentPage - 1)}>{t("selfEvolutionRun.prevPage")}</button>
            <span>{`${currentPage}/${totalPages}`}</span>
            <button type="button" disabled={currentPage >= totalPages} onClick={() => setPage(currentPage + 1)}>{t("selfEvolutionRun.nextPage")}</button>
          </div>
        </div>
        <div className="self-evolution-case-list">
          {pageCases.map(renderCaseProgressRow)}
        </div>
      </div>
    );
  };

  return (
    <div className="self-evolution-process-activity">
      <div className="self-evolution-process-activity-head">
        <Text>{activeCaseProgressGroup ? t("selfEvolutionRun.caseProgressSectionTitle") : t("selfEvolutionRun.keyEventsSectionTitle")}</Text>
        <span>{activeCaseProgressGroup ? t("selfEvolutionRun.displayByCasePaged") : activeStageLabel}</span>
      </div>
      <div className="self-evolution-process-activity-list is-key" onWheel={handleActivityListWheel}>
        {renderCaseProgressPanel()}
      </div>
      <details className="self-evolution-process-debug-log">
        <summary>{t("selfEvolutionRun.debugLogTitle", { count: processDashboard.recentActivityTotal })}</summary>
        <div className="self-evolution-process-activity-list is-debug" onWheel={handleActivityListWheel}>
          {renderActivityRows(processDashboard.recentActivities, t("selfEvolutionRun.debugLogEmptyHint"))}
        </div>
      </details>
    </div>
  );
}
