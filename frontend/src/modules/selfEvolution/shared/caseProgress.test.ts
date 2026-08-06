import { describe, expect, it } from "vitest";
import {
  applyGlobalDatasetStep,
  areCaseStepsDone,
  buildCaseItem,
  buildCaseProgressGroups,
  datasetCaseSteps,
  evalCaseSteps,
  getCaseProgressActionStatus,
  getOperationCaseId,
  resolveAnalysisCaseStep,
  sortCaseItems,
  updateCaseStep,
  type CaseProgressState,
} from "./caseProgress";
import type { NormalizedThreadEvent } from "./types";

function makeEvent(overrides: Partial<NormalizedThreadEvent>): NormalizedThreadEvent {
  return { key: "k", type: "x", ...overrides };
}

describe("getCaseProgressActionStatus", () => {
  it("returns done for a finish action or a completed status", () => {
    expect(getCaseProgressActionStatus(makeEvent({ action: "finish" }))).toBe("done");
    expect(getCaseProgressActionStatus(makeEvent({ payload: { data: { status: "succeeded" } } }))).toBe("done");
  });

  it("returns failed for a failed action or status", () => {
    expect(getCaseProgressActionStatus(makeEvent({ action: "failed" }))).toBe("failed");
  });

  it("returns paused for a checkpointed status", () => {
    expect(getCaseProgressActionStatus(makeEvent({ payload: { data: { status: "checkpointed" } } }))).toBe("paused");
  });

  it("returns undefined for an unrelated action/status", () => {
    expect(getCaseProgressActionStatus(makeEvent({ action: "unknown" }))).toBeUndefined();
  });
});

describe("updateCaseStep", () => {
  it("creates a new case entry and records the step status", () => {
    const cases = new Map<string, CaseProgressState>();
    updateCaseStep(cases, "case_1", "generate", "running", "t1", "artifact-1");
    expect(cases.get("case_1")).toMatchObject({ steps: { generate: "running" }, artifactId: "artifact-1" });
  });

  it("does not downgrade a done step back to a non-done status", () => {
    const cases = new Map<string, CaseProgressState>([["case_1", { caseId: "case_1", steps: { generate: "done" } }]]);
    updateCaseStep(cases, "case_1", "generate", "running");
    expect(cases.get("case_1")?.steps.generate).toBe("done");
  });

  it("does nothing when status is undefined", () => {
    const cases = new Map<string, CaseProgressState>();
    updateCaseStep(cases, "case_1", "generate", undefined);
    expect(cases.has("case_1")).toBe(false);
  });
});

describe("getOperationCaseId", () => {
  it("prefers the case id, falling back to current_item", () => {
    expect(getOperationCaseId({ data: { case_id: "c1" } })).toBe("c1");
    expect(getOperationCaseId({ data: { current_item: "c2" } })).toBe("c2");
  });
});

describe("resolveAnalysisCaseStep", () => {
  it("maps trace_summary-like flow kinds", () => {
    expect(resolveAnalysisCaseStep("analysis.trace_summary", undefined)).toBe("trace_summary");
    expect(resolveAnalysisCaseStep(undefined, "coarse")).toBe("trace_summary");
  });

  it("maps classify_case-like flow kinds", () => {
    expect(resolveAnalysisCaseStep("analysis.classify_case", undefined)).toBe("classify_case");
  });

  it("returns undefined for unrelated flow kinds", () => {
    expect(resolveAnalysisCaseStep("analysis.other", undefined)).toBeUndefined();
  });
});

describe("applyGlobalDatasetStep / areCaseStepsDone", () => {
  it("applies the same status to every existing case", () => {
    const cases = new Map<string, CaseProgressState>([
      ["c1", { caseId: "c1", steps: {} }],
      ["c2", { caseId: "c2", steps: {} }],
    ]);
    applyGlobalDatasetStep(cases, "load_corpus", "done");
    expect(cases.get("c1")?.steps.load_corpus).toBe("done");
    expect(cases.get("c2")?.steps.load_corpus).toBe("done");
  });

  it("checks that all required steps are done", () => {
    const item: CaseProgressState = { caseId: "c1", steps: { load_corpus: "done", build_snapshot: "done", generate: "done", assemble: "done" } };
    expect(areCaseStepsDone(item, datasetCaseSteps)).toBe(true);
    expect(areCaseStepsDone({ caseId: "c1", steps: { load_corpus: "done" } }, datasetCaseSteps)).toBe(false);
  });
});

describe("buildCaseItem", () => {
  it("marks a case done when all steps are done and strips the case_ prefix from the title", () => {
    const item: CaseProgressState = { caseId: "case_003", steps: { rag: "done", judge: "done" } };
    const result = buildCaseItem(item, evalCaseSteps, "eval-reports", "artifact-1", "查看结果");
    expect(result.status).toBe("done");
    expect(result.title).toBe("Case 3");
    expect(result.completed).toBe(2);
  });

  it("marks a case running when at least one step is done/running but not all", () => {
    const item: CaseProgressState = { caseId: "case_001", steps: { rag: "done" } };
    const result = buildCaseItem(item, evalCaseSteps, "eval-reports", undefined, "查看结果");
    expect(result.status).toBe("running");
  });

  it("marks a case failed when any step failed", () => {
    const item: CaseProgressState = { caseId: "case_001", steps: { rag: "failed" } };
    const result = buildCaseItem(item, evalCaseSteps, "eval-reports", undefined, "查看结果");
    expect(result.status).toBe("failed");
  });
});

describe("sortCaseItems", () => {
  it("sorts numerically by the numeric portion of the case id", () => {
    const a = { caseId: "case_2" } as ReturnType<typeof buildCaseItem>;
    const b = { caseId: "case_10" } as ReturnType<typeof buildCaseItem>;
    expect(sortCaseItems(a, b)).toBeLessThan(0);
  });
});

describe("buildCaseProgressGroups", () => {
  it("tracks dataset generate_case progress and inherits prior global steps", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ key: "e1", stage: "dataset", action: "finish", payload: { event_type: "load_corpus", operation_run_id: "dataset.load_corpus" } }),
      makeEvent({
        key: "e2",
        stage: "dataset",
        action: "progress",
        payload: { event_type: "generate_case", operation_run_id: "dataset.generate_case", data: { case_id: "case_001" } },
      }),
    ];
    const groups = buildCaseProgressGroups(events);
    const datasetGroup = groups.find((group) => group.stage === "dataset");
    expect(datasetGroup?.cases[0].steps.find((step) => step.key === "load_corpus")?.status).toBe("done");
  });

  it("marks running eval steps as failed once a terminal failed event arrives", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({
        key: "e1",
        stage: "eval",
        action: "progress",
        payload: { event_type: "eval.answer", operation_run_id: "eval.answer", data: { case_id: "case_001", status: "running" } },
      }),
      makeEvent({ key: "e2", type: "done", stage: "eval", payload: { status: "failed" } }),
    ];
    const groups = buildCaseProgressGroups(events);
    const evalGroup = groups.find((group) => group.stage === "eval");
    expect(evalGroup?.cases[0].status).toBe("failed");
  });

  it("omits groups that end up with no tracked cases", () => {
    const groups = buildCaseProgressGroups([]);
    expect(groups).toEqual([]);
  });
});
