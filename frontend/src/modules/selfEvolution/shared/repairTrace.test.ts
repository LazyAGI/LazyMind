import { describe, expect, it } from "vitest";
import {
  buildRepairTraceAttemptGroups,
  buildRepairTraceEventTitle,
  buildRepairTracePhaseSummaries,
  buildRepairTraceRows,
  getRepairTraceCategory,
  getRepairTraceCategoryLabel,
  getRepairTraceProgress,
  isRepairTraceRawEventType,
  isRepairTraceStageEvent,
} from "./repairTrace";
import type { NormalizedThreadEvent } from "./types";

function makeEvent(overrides: Partial<NormalizedThreadEvent> & { key: string; type: string }): NormalizedThreadEvent {
  return {
    sequence: 0,
    ...overrides,
  } as NormalizedThreadEvent;
}

describe("isRepairTraceRawEventType", () => {
  it("returns true for known repair-trace prefixes", () => {
    expect(isRepairTraceRawEventType("opencode.setup")).toBe(true);
    expect(isRepairTraceRawEventType("verify.command_started")).toBe(true);
  });

  it("returns false for undefined or unrelated event types", () => {
    expect(isRepairTraceRawEventType(undefined)).toBe(false);
    expect(isRepairTraceRawEventType("dataset.generate_case")).toBe(false);
  });
});

describe("isRepairTraceStageEvent", () => {
  it("returns true when event.stage is repair", () => {
    expect(isRepairTraceStageEvent({ stage: "repair", payload: undefined })).toBe(true);
  });

  it("returns true when payload.stage is a repair alias", () => {
    expect(isRepairTraceStageEvent({ stage: undefined, payload: { stage: "apply" } })).toBe(true);
  });

  it("returns false for unrelated stages", () => {
    expect(isRepairTraceStageEvent({ stage: "dataset", payload: undefined })).toBe(false);
  });
});

describe("getRepairTraceCategory", () => {
  it("categorizes by known event type prefixes", () => {
    expect(getRepairTraceCategory("opencode.setup")).toBe("opencode");
    expect(getRepairTraceCategory("verify.command_started")).toBe("verify");
    expect(getRepairTraceCategory("candidate.service_started")).toBe("candidate");
    expect(getRepairTraceCategory("analysis.candidate_started")).toBe("delta");
  });

  it("categorizes legacy repair.* event types", () => {
    expect(getRepairTraceCategory("repair.loop")).toBe("loop");
    expect(getRepairTraceCategory("repair.opencode_tool")).toBe("opencode");
    expect(getRepairTraceCategory("repair.delta")).toBe("delta");
  });

  it("falls back to attempt for unrecognized event types", () => {
    expect(getRepairTraceCategory("repair.attempt_started")).toBe("attempt");
    expect(getRepairTraceCategory("unknown.event")).toBe("attempt");
  });
});

describe("buildRepairTraceEventTitle", () => {
  it("resolves a title from the known title-key map", () => {
    expect(buildRepairTraceEventTitle("repair.attempt_started")).toBe("修复尝试开始");
  });

  it("builds a titled tool name for opencode.tool_use.* events", () => {
    expect(buildRepairTraceEventTitle("opencode.tool_use.custom_tool")).toContain("custom_tool");
  });

  it("falls back to a humanized event type for unknown events", () => {
    expect(buildRepairTraceEventTitle("verify.some_new_step")).toBe("verify · some new step");
  });
});

describe("getRepairTraceCategoryLabel", () => {
  it("returns a non-empty label for every category", () => {
    expect(getRepairTraceCategoryLabel("attempt").length).toBeGreaterThan(0);
    expect(getRepairTraceCategoryLabel("opencode").length).toBeGreaterThan(0);
  });
});

describe("buildRepairTraceRows", () => {
  it("returns an empty array when there are no repair-trace events", () => {
    const events = [makeEvent({ key: "e1", type: "dataset.start", stage: "dataset" })];
    expect(buildRepairTraceRows(events)).toEqual([]);
  });

  it("builds a running row for a started lifecycle event and closes it on completion", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({
        key: "e1",
        type: "repair.attempt_started",
        stage: "repair",
        sequence: 1,
        payload: { lifecycle: { id: "attempt-1", phase: "start" }, summary: { attempt: 1 } },
      }),
      makeEvent({
        key: "e2",
        type: "repair.attempt_completed",
        stage: "repair",
        sequence: 2,
        payload: {
          lifecycle: { id: "attempt-1", phase: "finish", terminal: true },
          summary: { attempt: 1 },
          status: "completed",
        },
      }),
    ];
    const rows = buildRepairTraceRows(events);
    expect(rows).toHaveLength(1);
    expect(rows[0].action).toBe("done");
    expect(rows[0].attempt).toBe(1);
  });

  it("marks a row failed when the finish event reports a failed status", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({
        key: "e1",
        type: "candidate.service_started",
        stage: "repair",
        sequence: 1,
        payload: { lifecycle: { id: "svc-1", phase: "start" } },
      }),
      makeEvent({
        key: "e2",
        type: "candidate.service_failed",
        stage: "repair",
        sequence: 2,
        payload: { lifecycle: { id: "svc-1", phase: "finish", terminal: true }, status: "failed" },
      }),
    ];
    const rows = buildRepairTraceRows(events);
    expect(rows).toHaveLength(1);
    expect(rows[0].action).toBe("failed");
  });

  it("closes any still-running rows once the repair step itself is marked done", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({
        key: "e1",
        type: "opencode.process_start",
        stage: "repair",
        sequence: 1,
        payload: { lifecycle: { id: "proc-1", phase: "start" } },
      }),
    ];
    const rows = buildRepairTraceRows(events, { repairStepStatus: "done" });
    expect(rows[0].action).toBe("done");
  });
});

describe("buildRepairTraceAttemptGroups", () => {
  it("groups rows by attempt number and assigns an aggregate status", () => {
    const rows = buildRepairTraceRows([
      makeEvent({
        key: "e1",
        type: "repair.attempt_started",
        stage: "repair",
        sequence: 1,
        payload: { lifecycle: { id: "a1", phase: "start" }, summary: { attempt: 1 } },
      }),
      makeEvent({
        key: "e2",
        type: "repair.attempt_completed",
        stage: "repair",
        sequence: 2,
        payload: {
          lifecycle: { id: "a1", phase: "finish", terminal: true },
          summary: { attempt: 1 },
          status: "completed",
        },
      }),
    ]);
    const groups = buildRepairTraceAttemptGroups(rows);
    expect(groups).toHaveLength(1);
    expect(groups[0].attempt).toBe(1);
    expect(groups[0].status).toBe("done");
  });

  it("puts rows without an attempt number into the unknown/other group", () => {
    const rows = buildRepairTraceRows([
      makeEvent({
        key: "e1",
        type: "opencode.message",
        stage: "repair",
        sequence: 1,
        payload: {},
      }),
    ]);
    const groups = buildRepairTraceAttemptGroups(rows);
    expect(groups.some((group) => group.key === "attempt-unknown")).toBe(true);
  });
});

describe("buildRepairTracePhaseSummaries", () => {
  it("summarizes rows into per-category status counts", () => {
    const rows = buildRepairTraceRows([
      makeEvent({
        key: "e1",
        type: "verify.command_started",
        stage: "repair",
        sequence: 1,
        payload: { lifecycle: { id: "v1", phase: "start" } },
      }),
      makeEvent({
        key: "e2",
        type: "verify.command_completed",
        stage: "repair",
        sequence: 2,
        payload: { lifecycle: { id: "v1", phase: "finish", terminal: true }, status: "completed" },
      }),
    ]);
    const summaries = buildRepairTracePhaseSummaries(rows);
    expect(summaries).toHaveLength(1);
    expect(summaries[0].category).toBe("verify");
    expect(summaries[0].count).toBe(1);
  });

  it("returns an empty array for no rows", () => {
    expect(buildRepairTracePhaseSummaries([])).toEqual([]);
  });
});

describe("getRepairTraceProgress", () => {
  it("computes counts of done/failed/running rows", () => {
    const rows = buildRepairTraceRows([
      makeEvent({
        key: "e1",
        type: "repair.attempt_started",
        stage: "repair",
        sequence: 1,
        payload: { lifecycle: { id: "a1", phase: "start" }, summary: { attempt: 1 } },
      }),
    ]);
    const progress = getRepairTraceProgress(rows);
    expect(progress.total).toBe(1);
    expect(progress.running).toBe(1);
    expect(progress.hasEvents).toBe(true);
  });

  it("reports hasEvents=false for an empty row list", () => {
    expect(getRepairTraceProgress([])).toEqual({
      total: 0,
      completed: 0,
      failed: 0,
      running: 0,
      hasEvents: false,
    });
  });
});
