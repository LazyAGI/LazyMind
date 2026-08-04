import { describe, expect, it } from "vitest";
import { BASE_URL } from "@/components/request";
import {
  AGENT_API_BASE,
  DEFAULT_EVAL_CASE_COUNT,
  DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY,
  EVO_API_BASE,
  FIXED_EVAL_SET,
  FIXED_EXTRA_EVAL_STRATEGY,
  SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY,
  SELF_EVOLUTION_THREAD_COMMAND_STORAGE_PREFIX,
  evalSetPreviewData,
  failedThreadEventTypes,
  inactiveTerminalThreadStatuses,
  pxMetricFieldAliases,
  resultKindGateStepMap,
  stageResultKindMap,
  stageStepMap,
  stepStageMap,
  terminalThreadEventTypes,
  workflowStepOrder,
} from "./constants";

describe("api base constants", () => {
  it("builds AGENT_API_BASE from BASE_URL", () => {
    expect(AGENT_API_BASE).toBe(`${BASE_URL}/api/core/agent`);
  });

  it("builds EVO_API_BASE from BASE_URL", () => {
    expect(EVO_API_BASE).toBe(`${BASE_URL}/api/evo/v1/evo`);
  });
});

describe("fixed defaults", () => {
  it("uses the __none__ sentinel for the fixed eval set", () => {
    expect(FIXED_EVAL_SET).toBe("__none__");
  });

  it("defaults extra eval strategy to generate", () => {
    expect(FIXED_EXTRA_EVAL_STRATEGY).toBe("generate");
  });

  it("defaults the eval case count to 10", () => {
    expect(DEFAULT_EVAL_CASE_COUNT).toBe(10);
  });
});

describe("storage keys", () => {
  it("uses stable prefixed storage keys", () => {
    expect(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY).toBe("lazymind:self-evolution:last-thread");
    expect(SELF_EVOLUTION_THREAD_COMMAND_STORAGE_PREFIX).toBe("lazymind:self-evolution:thread-command:");
    expect(DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY).toBe(
      "lazymind:self-evolution:thread-history",
    );
  });
});

describe("pxMetricFieldAliases", () => {
  it("includes every px metric key with at least the canonical alias", () => {
    expect(pxMetricFieldAliases.answer_correctness).toContain("answer_correctness");
    expect(pxMetricFieldAliases.answer_score).toContain("answer_score");
    expect(pxMetricFieldAliases.chunk_recall).toContain("chunk_recall");
    expect(pxMetricFieldAliases.doc_recall).toContain("doc_recall");
  });
});

describe("stage/step maps", () => {
  it("maps every thread event stage to its workflow step id", () => {
    expect(stageStepMap.dataset).toBe("dataset");
    expect(stageStepMap.eval).toBe("px-report");
    expect(stageStepMap.repair).toBe("code-optimize");
  });

  it("maps every thread event stage to a result kind", () => {
    expect(stageResultKindMap.dataset).toBe("datasets");
    expect(stageResultKindMap.abtest).toBe("abtests");
  });

  it("maps every result kind back to its gate step name", () => {
    expect(resultKindGateStepMap.datasets).toBe("dataset");
    expect(resultKindGateStepMap["eval-reports"]).toBe("eval");
  });

  it("maps every workflow step id back to its stage", () => {
    expect(stepStageMap.dataset).toBe("dataset");
    expect(stepStageMap["ab-test"]).toBe("abtest");
  });

  it("keeps the workflow step order in the expected sequence", () => {
    expect(workflowStepOrder).toEqual(["dataset", "px-report", "analysis", "code-optimize", "ab-test"]);
  });
});

describe("terminal/failed event type sets", () => {
  it("marks known terminal event types as terminal", () => {
    expect(terminalThreadEventTypes.has("done")).toBe(true);
    expect(terminalThreadEventTypes.has("thread.done")).toBe(true);
    expect(terminalThreadEventTypes.has("random.event")).toBe(false);
  });

  it("marks known failure event types as failed", () => {
    expect(failedThreadEventTypes.has("error")).toBe(true);
    expect(failedThreadEventTypes.has("USER_ACTIVE_THREAD_EXISTS")).toBe(true);
    expect(failedThreadEventTypes.has("done")).toBe(false);
  });

  it("marks known inactive terminal statuses", () => {
    expect(inactiveTerminalThreadStatuses.has("cancelled")).toBe(true);
    expect(inactiveTerminalThreadStatuses.has("running")).toBe(false);
  });
});

describe("evalSetPreviewData", () => {
  it("provides preview cases with well-formed shape", () => {
    expect(evalSetPreviewData.cases.length).toBeGreaterThan(0);
    expect(evalSetPreviewData.cases[0]).toHaveProperty("case_id");
    expect(evalSetPreviewData.cases[0]).toHaveProperty("question");
  });
});
