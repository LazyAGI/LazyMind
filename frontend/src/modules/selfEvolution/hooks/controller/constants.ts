import type { WorkflowResultKind, WorkflowStep } from "../../shared";
import type { ArtifactPanelItem } from "./types";

export const INITIAL_THREAD_STEP_ID = "00000000-0000-0000-0000-000000000001";

export const stageArtifactKindMap: Record<string, WorkflowResultKind> = {
  dataset: "datasets",
  eval: "eval-reports",
  analysis: "analysis-reports",
  repair: "diffs",
  abtest: "abtests",
};

export const artifactStepIdMap: Record<WorkflowResultKind, ArtifactPanelItem["stepId"]> = {
  datasets: "dataset",
  "eval-reports": "px-report",
  "analysis-reports": "analysis",
  diffs: "code-optimize",
  abtests: "ab-test",
};

export const workflowStepStageMap: Record<WorkflowStep["id"], string> = {
  dataset: "dataset",
  "px-report": "eval",
  analysis: "analysis",
  "code-optimize": "repair",
  "ab-test": "abtest",
};

export const EVAL_REPORT_BAD_CASES_PAGE_SIZE = 10;

export const legacyPlanningThinkingText = "正在理解你的请求并规划下一步。";

export const analysisCategoryColors = ["#2f7fe5", "#22a06b", "#f5a623", "#8b5cf6", "#e85d75", "#14a8b5"];
