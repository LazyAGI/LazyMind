import i18n from "@/i18n";
import { type PxMetricKey, type ThreadEventStage, type WorkflowResultKind, type WorkflowStep } from "./types";

export function t(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options) as string;
}

export function getWorkflowResultLabels(): Record<WorkflowResultKind, string> {
  return {
    datasets: t("selfEvolutionRun.workflowResultDatasets"),
    "eval-reports": t("selfEvolutionRun.workflowResultEvalReports"),
    "analysis-reports": t("selfEvolutionRun.workflowResultAnalysisReports"),
    diffs: t("selfEvolutionRun.workflowResultDiffs"),
    abtests: t("selfEvolutionRun.workflowResultAbtests"),
  };
}

export function getPxMetricMeta(): Array<{ key: PxMetricKey; label: string; color: string }> {
  return [
    { key: "answer_correctness", label: t("selfEvolutionRun.metricAnswerCorrectness"), color: "#1a73e8" },
    { key: "answer_score", label: t("selfEvolutionRun.metricAnswerScore"), color: "#22a06b" },
    { key: "chunk_recall", label: t("selfEvolutionRun.metricChunkRecall"), color: "#f08c00" },
    { key: "doc_recall", label: t("selfEvolutionRun.metricDocRecall"), color: "#7048e8" },
  ];
}

export function getStageLabels(): Record<ThreadEventStage, string> {
  return {
    dataset: t("selfEvolutionRun.stageDataset"),
    eval: t("selfEvolutionRun.stageEval"),
    analysis: t("selfEvolutionRun.stageAnalysis"),
    repair: t("selfEvolutionRun.stageRepair"),
    abtest: t("selfEvolutionRun.stageAbtest"),
  };
}

export function getCheckpointCommandText(): string {
  return t("selfEvolutionRun.checkpointCommand");
}

export function getEventActionLabels(): Record<string, string> {
  return {
    start: t("selfEvolutionRun.actionStart"),
    progress: t("selfEvolutionRun.actionProgress"),
    finish: t("selfEvolutionRun.actionFinish"),
    failed: t("selfEvolutionRun.actionFailed"),
    cancel: t("selfEvolutionRun.actionCancel"),
    pause: t("selfEvolutionRun.actionPause"),
    resume: t("selfEvolutionRun.actionResume"),
    "indexer.result": t("selfEvolutionRun.actionIndexerResult"),
    "conductor.result": t("selfEvolutionRun.actionConductorResult"),
    "researcher.result": t("selfEvolutionRun.actionResearcherResult"),
    "tool.used": t("selfEvolutionRun.actionToolUsed"),
    "round.diff": t("selfEvolutionRun.actionRoundDiff"),
  };
}

export function getAnalysisCategoryLabels(): Record<string, string> {
  return {
    retrieval_miss: t("selfEvolutionRun.categoryRetrievalMiss"),
    generation_drift: t("selfEvolutionRun.categoryGenerationDrift"),
    score_anomaly: t("selfEvolutionRun.categoryScoreAnomaly"),
  };
}

export function getAnalysisVerdictLabels(): Record<string, string> {
  return {
    confirmed: t("selfEvolutionRun.verdictConfirmed"),
    refuted: t("selfEvolutionRun.verdictRefuted"),
    inconclusive: t("selfEvolutionRun.verdictInconclusive"),
    partial: t("selfEvolutionRun.verdictPartial"),
  };
}

export function getWorkflowStepDefinitions(): Omit<WorkflowStep, "status" | "runtimeText">[] {
  return [
    {
      id: "dataset",
      title: t("selfEvolutionRun.stepDatasetTitle"),
      desc: t("selfEvolutionRun.stepDatasetDesc"),
    },
    {
      id: "px-report",
      title: t("selfEvolutionRun.stepEvalTitle"),
      desc: t("selfEvolutionRun.stepEvalDesc"),
    },
    {
      id: "analysis",
      title: t("selfEvolutionRun.stepAnalysisTitle"),
      desc: t("selfEvolutionRun.stepAnalysisDesc"),
    },
    {
      id: "code-optimize",
      title: t("selfEvolutionRun.stepCodeOptimizeTitle"),
      desc: t("selfEvolutionRun.stepCodeOptimizeDesc"),
    },
    {
      id: "ab-test",
      title: t("selfEvolutionRun.stepAbTestTitle"),
      desc: t("selfEvolutionRun.stepAbTestDesc"),
    },
  ];
}

export function getExistingEvalSetOptions() {
  return [
    { label: t("selfEvolutionRun.noExistingEvalSet"), value: "__none__" },
  ];
}

export function getQuestionTypeLabelMap(): Record<number, string> {
  return {
    1: t("selfEvolutionRun.qtSingleHop"),
    2: t("selfEvolutionRun.qtMultiHop"),
    3: t("selfEvolutionRun.qtFormula"),
    4: t("selfEvolutionRun.qtTable"),
    5: t("selfEvolutionRun.qtCode"),
  };
}
