import { describe, expect, it } from "vitest";

import type { NormalizedThreadEvent } from "./types";
import { buildRepairTraceAttemptGroups, buildRepairTraceRows } from "./repairTrace";

const historicalAnalysisDone: NormalizedThreadEvent = {
  key: "analysis:done",
  type: "done",
  stage: "analysis",
  payload: { type: "done", status: "paused" },
};

const repairAttemptStarted: NormalizedThreadEvent = {
  key: "repair:attempt:1",
  type: "repair.attempt_started",
  stage: "repair",
  action: "start",
  payload: {
    event_type: "repair.attempt_started",
    status: "started",
    summary: { attempt: 1 },
  },
};

const opencodeSetup: NormalizedThreadEvent = {
  key: "repair:opencode:setup",
  type: "opencode.setup",
  stage: "repair",
  action: "finish",
  payload: {
    event_type: "opencode.setup",
    status: "completed",
    summary: { attempt: 1 },
  },
};

describe("repair trace status", () => {
  it("keeps the current attempt running when an earlier stage emitted paused done", () => {
    const rows = buildRepairTraceRows(
      [historicalAnalysisDone, repairAttemptStarted, opencodeSetup],
      { repairStepStatus: "running" },
    );

    expect(rows.find((row) => row.eventType === "repair.attempt_started")?.action).toBe("running");
    expect(buildRepairTraceAttemptGroups(rows)[0]?.status).toBe("running");
  });

  it("does not let an older repair terminal close newer repair activity", () => {
    const oldRepairDone: NormalizedThreadEvent = {
      key: "repair:done:old",
      type: "done",
      stage: "repair",
      payload: { type: "done", status: "paused" },
    };
    const rows = buildRepairTraceRows(
      [oldRepairDone, repairAttemptStarted, opencodeSetup],
    );

    expect(rows.find((row) => row.eventType === "repair.attempt_started")?.action).toBe("running");
  });
});
