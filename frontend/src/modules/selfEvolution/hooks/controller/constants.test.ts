import { describe, expect, it } from "vitest";
import {
  analysisCategoryColors,
  artifactStepIdMap,
  EVAL_REPORT_BAD_CASES_FETCH_CHUNK_SIZE,
  EVAL_REPORT_BAD_CASES_PAGE_SIZE,
  INITIAL_THREAD_STEP_ID,
  legacyPlanningThinkingText,
  stageArtifactKindMap,
  THREAD_STEP_SUBSCRIBE_POLL_INTERVAL_MS,
  THREAD_STEP_SUBSCRIBE_POLL_MAX_ATTEMPTS,
  workflowStepStageMap,
} from "./constants";
import type { WorkflowResultKind } from "../../shared";

describe("stageArtifactKindMap / artifactStepIdMap", () => {
  it("maps every workflow stage to a result kind and back to a step id", () => {
    expect(stageArtifactKindMap.dataset).toBe("datasets");
    expect(stageArtifactKindMap.abtest).toBe("abtests");
    const kind: WorkflowResultKind = stageArtifactKindMap.dataset;
    expect(artifactStepIdMap[kind]).toBe("dataset");
  });

  it("covers all result kinds in artifactStepIdMap", () => {
    const kinds: WorkflowResultKind[] = ["datasets", "eval-reports", "analysis-reports", "diffs", "abtests"];
    kinds.forEach((kind) => {
      expect(typeof artifactStepIdMap[kind]).toBe("string");
    });
  });
});

describe("workflowStepStageMap", () => {
  it("maps each workflow step id to its stage name", () => {
    expect(workflowStepStageMap.dataset).toBe("dataset");
    expect(workflowStepStageMap["px-report"]).toBe("eval");
    expect(workflowStepStageMap["ab-test"]).toBe("abtest");
  });
});

describe("numeric and string constants", () => {
  it("exposes stable pagination and polling constants", () => {
    expect(EVAL_REPORT_BAD_CASES_PAGE_SIZE).toBe(10);
    expect(EVAL_REPORT_BAD_CASES_FETCH_CHUNK_SIZE).toBe(100);
    expect(THREAD_STEP_SUBSCRIBE_POLL_INTERVAL_MS).toBeGreaterThan(0);
    expect(THREAD_STEP_SUBSCRIBE_POLL_MAX_ATTEMPTS).toBeGreaterThan(0);
  });

  it("exposes a fixed initial thread step id and legacy planning text", () => {
    expect(INITIAL_THREAD_STEP_ID).toBe("00000000-0000-0000-0000-000000000001");
    expect(legacyPlanningThinkingText.length).toBeGreaterThan(0);
  });

  it("exposes a non-empty palette for analysis category colors", () => {
    expect(analysisCategoryColors.length).toBeGreaterThan(0);
    analysisCategoryColors.forEach((color) => expect(color.startsWith("#")).toBe(true));
  });
});
