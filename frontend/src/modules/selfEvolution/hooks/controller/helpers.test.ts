import { describe, expect, it } from "vitest";

import type { NormalizedThreadEvent, ThreadEventStage } from "../../shared";
import {
  buildStreamingEvalCaseRows,
  getStreamingAnalysisProgress,
  getStreamingEvalProgress,
  mergeThreadStepStatuses,
} from "./helpers";

function caseEvent(
  stage: ThreadEventStage,
  eventType: string,
  caseId: string,
  action: "progress" | "finish",
  current?: number,
  total?: number,
): NormalizedThreadEvent {
  return {
    key: `${eventType}:${caseId}:${action}`,
    type: eventType,
    stage,
    action,
    payload: {
      event_type: eventType,
      case_id: caseId,
      ...(typeof current === "number" || typeof total === "number"
        ? { progress: { current, total } }
        : {}),
    },
  };
}

describe("streaming EVO progress", () => {
  it("uses every eval case for the denominator and only completed judges for the numerator", () => {
    const answered = new Set([1, 2, 4, 5, 6, 8, 9, 10]);
    const events = Array.from({ length: 10 }, (_, index) => {
      const caseNumber = index + 1;
      const caseId = `case_${String(caseNumber).padStart(4, "0")}`;
      return caseEvent(
        "eval",
        "eval.answer",
        caseId,
        answered.has(caseNumber) ? "finish" : "progress",
      );
    });
    events.push(
      caseEvent("eval", "eval.judge", "case_0001", "progress", 0, 2),
      caseEvent("eval", "eval.judge", "case_0002", "progress", 0, 2),
    );

    expect(buildStreamingEvalCaseRows(events)).toHaveLength(10);
    expect(getStreamingEvalProgress(events)).toEqual({ current: 0, total: 10 });
  });

  it("keeps the eval total stable while judge results arrive", () => {
    const events = Array.from({ length: 10 }, (_, index) => {
      const caseId = `case_${String(index + 1).padStart(4, "0")}`;
      return caseEvent("eval", "eval.answer", caseId, "finish");
    });
    events.push(
      ...Array.from({ length: 4 }, (_, index) => {
        const caseId = `case_${String(index + 1).padStart(4, "0")}`;
        return caseEvent("eval", "eval.judge", caseId, "finish", index + 1, 4);
      }),
    );

    expect(getStreamingEvalProgress(events)).toEqual({ current: 4, total: 10 });
  });

  it("does not complete analysis when only trace summaries are done", () => {
    const traceEvents = Array.from({ length: 10 }, (_, index) => {
      const caseId = `case_${String(index + 1).padStart(4, "0")}`;
      return caseEvent(
        "analysis",
        "analysis.trace_summary",
        caseId,
        "finish",
        index + 1,
        10,
      );
    });

    expect(getStreamingAnalysisProgress(traceEvents)).toEqual({ current: 0, total: 10 });

    const classifyEvents = Array.from({ length: 10 }, (_, index) => {
      const caseId = `case_${String(index + 1).padStart(4, "0")}`;
      return caseEvent(
        "analysis",
        "analysis.classify_case",
        caseId,
        "finish",
        index + 1,
        10,
      );
    });
    expect(getStreamingAnalysisProgress([...traceEvents, ...classifyEvents])).toEqual({
      current: 10,
      total: 10,
    });
  });
});

describe("thread step status precedence", () => {
  it("keeps the live running step ahead of a stale completed event", () => {
    expect(
      mergeThreadStepStatuses(
        { repair: "running" },
        { repair: "done" },
        "running",
      ),
    ).toEqual({ repair: "running" });
  });

  it("uses the terminal event after the flow has actually stopped", () => {
    expect(
      mergeThreadStepStatuses(
        { analysis: "running" },
        { analysis: "done" },
        "completed",
      ),
    ).toEqual({ analysis: "done" });
  });
});
