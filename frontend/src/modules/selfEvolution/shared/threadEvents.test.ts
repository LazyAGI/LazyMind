import { describe, expect, it } from "vitest";

import {
  normalizeThreadEvent,
  resolveCompletedStageFromDonePayload,
  resolveTerminalStepStatusFromFlowStatus,
} from "./threadEvents";
import { applyThreadStreamTerminalToState, createThreadRestoreWorkflowRuntimeState } from "./runtimeState";

describe("terminal step status", () => {
  it("preserves a real pause instead of reporting completion", () => {
    expect(resolveTerminalStepStatusFromFlowStatus("paused")).toBe("paused");
  });

  it("still maps completed and failed flow states", () => {
    expect(resolveTerminalStepStatusFromFlowStatus("completed")).toBe("done");
    expect(resolveTerminalStepStatusFromFlowStatus("failed")).toBe("failed");
  });
});

describe("terminal runtime state", () => {
  it("does not turn a technical pause into a completed stage", () => {
    const state = applyThreadStreamTerminalToState(
      createThreadRestoreWorkflowRuntimeState(),
      {
        key: "repair-paused",
        type: "done",
        stage: "repair",
        payload: { status: "paused", current_step: "repair" },
      },
    );

    expect(state["code-optimize"].status).toBe("paused");
  });
});

describe("completed stage resolution", () => {
  it("uses the released stage when the flow already advanced to the next step", () => {
    expect(
      resolveCompletedStageFromDonePayload({
        status: "running",
        reason: "step_completed",
        current_step: "repair",
        last_released_step: "analysis",
        retry_from_step: "repair",
      }),
    ).toBe("analysis");
  });

  it("normalizes the real analysis completion frame onto analysis, not repair", () => {
    const event = normalizeThreadEvent({
      eventName: "done",
      data: JSON.stringify({
        thread_id: "thr-567a91f0",
        step_id: "analysis-step",
        status: "running",
        reason: "step_completed",
        current_step: "repair",
        checkpoint_state: "valid",
        first_missing_step: "repair",
        last_released_step: "analysis",
        retry_from_step: "repair",
      }),
    });

    expect(event.stage).toBe("analysis");
  });

  it("uses the pending checkpoint stage before it is approved", () => {
    expect(
      resolveCompletedStageFromDonePayload({
        status: "paused",
        reason: "checkpoint_wait",
        checkpoint_state: "pending",
        current_step: "analysis",
        last_released_step: "eval",
        retry_from_step: "analysis",
      }),
    ).toBe("analysis");
  });

  it("keeps a technical pause on the current repair stage", () => {
    expect(
      resolveCompletedStageFromDonePayload({
        status: "paused",
        reason: "user_paused",
        checkpoint_state: "valid",
        current_step: "repair",
        last_released_step: "analysis",
        retry_from_step: "repair",
      }),
    ).toBe("repair");
  });
});
