import { type EvoCaseProgressGroup, type EvoCaseProgressItem, type NormalizedThreadEvent, type StepStatus, type WorkflowResultKind } from "./types";
import { t } from "./i18n";
import { getEventArtifactId, getEventCaseId, getEventFlowKind, getEventPayloadData, getEventRuntimeArtifactId, getNestedRecordField, getOperationRunId, getStringField } from "./fields";

export type CaseProgressState = {
  caseId: string;
  steps: Record<string, StepStatus>;
  artifactId?: string;
  updatedAt?: string;
};

export const datasetCaseSteps = ["load_corpus", "build_snapshot", "generate", "assemble"] as const;
export const evalCaseSteps = ["rag", "judge"] as const;
export const analysisCaseSteps = ["coarse", "fine"] as const;
export const caseStepLabels: Record<string, string> = {
  load_corpus: "load_corpus",
  build_snapshot: "build_snapshot",
  generate: "generate",
  assemble: "assemble",
  rag: "RAG",
  judge: "judge",
  coarse: "coarse",
  fine: "fine",
};

export function getCaseProgressActionStatus(event: NormalizedThreadEvent): StepStatus | undefined {
  const eventData = getEventPayloadData(event.payload);
  const after = getNestedRecordField(eventData, ["after"]);
  const status = getStringField(eventData, ["status"]) || getStringField(after, ["status"]);
  if (event.action === "finish" || status === "success" || status === "ended" || status === "skipped") {
    return "done";
  }
  if (event.action === "failed" || status === "failed") {
    return "failed";
  }
  if (event.action === "pause" || status === "checkpointed") {
    return "paused";
  }
  if (event.action === "cancel" || status === "cancelled") {
    return "canceled";
  }
  if (event.action === "progress" || status === "running") {
    return "running";
  }
  return undefined;
}

export function updateCaseStep(
  cases: Map<string, CaseProgressState>, caseId: string, step: string,
  status: StepStatus | undefined, updatedAt?: string, artifactId?: string,
) {
  if (!status) {
    return;
  }
  const item = cases.get(caseId) || { caseId, steps: {} };
  const previous = item.steps[step];
  if (previous !== "done" || status === "done") {
    item.steps[step] = status;
  }
  item.artifactId = artifactId || item.artifactId;
  item.updatedAt = updatedAt || item.updatedAt;
  cases.set(caseId, item);
}

export function getOperationCaseId(payload: Record<string, unknown> | undefined) {
  return getEventCaseId(payload) || getStringField(getEventPayloadData(payload), ["current_item"]);
}

export function resolveAnalysisCaseStep(flowKind: string | undefined, operationRunId: string | undefined): "coarse" | "fine" | undefined {
  if (
    flowKind === "analysis.trace_summary" ||
    flowKind === "analysis.coarse_classify" ||
    operationRunId === "analysis.trace_summary"
  ) {
    return "coarse";
  }
  if (
    flowKind === "analysis.fine_classify" ||
    flowKind === "analysis.classification" ||
    operationRunId === "analysis.classify_case"
  ) {
    return "fine";
  }
  return undefined;
}

export function applyGlobalDatasetStep(cases: Map<string, CaseProgressState>, step: string, status: StepStatus | undefined, updatedAt?: string, artifactId?: string) {
  cases.forEach((item) => updateCaseStep(cases, item.caseId, step, status, updatedAt, artifactId));
}

export function buildCaseItem(item: CaseProgressState, steps: readonly string[], artifactKind: WorkflowResultKind, artifactId: string | undefined, artifactLabel: string): EvoCaseProgressItem {
  const builtSteps = steps.map((key) => ({ key, label: caseStepLabels[key] || key, status: item.steps[key] || "pending" }));
  const completed = builtSteps.filter((step) => step.status === "done").length;
  const status: StepStatus = completed === builtSteps.length ? "done" :
    builtSteps.some((step) => step.status === "failed") ? "failed" :
      builtSteps.some((step) => step.status === "canceled") ? "canceled" :
        builtSteps.some((step) => step.status === "paused") ? "paused" :
          builtSteps.some((step) => step.status === "running" || step.status === "done") ? "running" : "pending";
  return { caseId: item.caseId, title: item.caseId.replace(/^case_0*/, "Case "), completed, total: builtSteps.length, status, steps: builtSteps, artifactKind, artifactId, artifactLabel, updatedAt: item.updatedAt };
}

export const areCaseStepsDone = (item: CaseProgressState, steps: readonly string[]) => steps.every((step) => item.steps[step] === "done");

export function sortCaseItems(a: EvoCaseProgressItem, b: EvoCaseProgressItem) {
  const left = Number(a.caseId.match(/\d+/)?.[0] || 0);
  const right = Number(b.caseId.match(/\d+/)?.[0] || 0);
  return left - right || a.caseId.localeCompare(b.caseId);
}

export function buildCaseProgressGroups(events: NormalizedThreadEvent[]): EvoCaseProgressGroup[] {
  const datasetCases = new Map<string, CaseProgressState>();
  const evalCases = new Map<string, CaseProgressState>();
  const analysisCases = new Map<string, CaseProgressState>();
  const abtestCases = new Map<string, CaseProgressState>();
  const datasetGlobal: Record<string, StepStatus | undefined> = {};
  events.forEach((event) => {
    const operationRunId = getOperationRunId(event.payload);
    const flowKind = getEventFlowKind(event.payload);
    const artifactId = getEventRuntimeArtifactId(event.payload) || getEventArtifactId(event.payload);
    const status = getCaseProgressActionStatus(event);
    if (!operationRunId || !status) {
      return;
    }
    const caseId = getOperationCaseId(event.payload);
    if (flowKind === "dataset.load_corpus") {
      datasetGlobal.load_corpus = status;
      applyGlobalDatasetStep(datasetCases, "load_corpus", status, event.timestamp);
    } else if (flowKind === "dataset.build_corpus_snapshot") {
      datasetGlobal.build_snapshot = status;
      applyGlobalDatasetStep(datasetCases, "build_snapshot", status, event.timestamp);
    } else if (caseId && flowKind === "dataset.assemble" && status === "running") {
      updateCaseStep(datasetCases, caseId, "assemble", "done", event.timestamp);
    } else if (flowKind === "dataset.assemble") {
      datasetGlobal.assemble = status;
      applyGlobalDatasetStep(datasetCases, "assemble", status, event.timestamp);
    } else if (caseId && flowKind === "dataset.generate_case") {
      Object.entries(datasetGlobal).forEach(([step, value]) => updateCaseStep(datasetCases, caseId, step, value, event.timestamp));
      updateCaseStep(datasetCases, caseId, "generate", status, event.timestamp, artifactId);
    } else if (caseId && event.stage === "eval" && flowKind === "eval.answer_and_judge") {
      updateCaseStep(evalCases, caseId, "rag", status, event.timestamp, artifactId);
      updateCaseStep(evalCases, caseId, "judge", status, event.timestamp, artifactId);
    } else if (caseId && flowKind === "abtest.candidate_rag_answer") {
      updateCaseStep(abtestCases, caseId, "rag", status, event.timestamp, artifactId);
    } else if (caseId && flowKind === "abtest.candidate_judge") {
      updateCaseStep(abtestCases, caseId, "judge", status, event.timestamp, artifactId);
    } else if (caseId) {
      const analysisStep = resolveAnalysisCaseStep(flowKind, operationRunId);
      if (analysisStep) {
        updateCaseStep(analysisCases, caseId, analysisStep, status, event.timestamp, artifactId);
      }
    }
  });
  const groups: EvoCaseProgressGroup[] = [
    { stage: "dataset", title: t("selfEvolutionRun.caseGroupDataset"), pageSize: 10, cases: Array.from(datasetCases.values()).map((item) => buildCaseItem(item, datasetCaseSteps, "datasets", areCaseStepsDone(item, datasetCaseSteps) ? item.artifactId : undefined, t("selfEvolutionRun.viewCaseDetail"))).sort(sortCaseItems) },
    { stage: "eval", title: t("selfEvolutionRun.caseGroupEval"), pageSize: 10, cases: Array.from(evalCases.values()).map((item) => buildCaseItem(item, evalCaseSteps, "eval-reports", areCaseStepsDone(item, evalCaseSteps) ? item.artifactId : undefined, t("selfEvolutionRun.viewCaseResult"))).sort(sortCaseItems) },
    { stage: "analysis", title: t("selfEvolutionRun.caseGroupAnalysis"), pageSize: 10, cases: Array.from(analysisCases.values()).map((item) => buildCaseItem(item, analysisCaseSteps, "analysis-reports", areCaseStepsDone(item, analysisCaseSteps) ? item.artifactId : undefined, t("selfEvolutionRun.viewCaseCategory"))).sort(sortCaseItems) },
    { stage: "abtest", title: t("selfEvolutionRun.caseGroupAbtest"), pageSize: 10, cases: Array.from(abtestCases.values()).map((item) => buildCaseItem(item, evalCaseSteps, "abtests", areCaseStepsDone(item, evalCaseSteps) ? item.artifactId : undefined, t("selfEvolutionRun.viewCaseCompare"))).sort(sortCaseItems) },
  ];
  return groups.filter((group) => group.cases.length > 0);
}
