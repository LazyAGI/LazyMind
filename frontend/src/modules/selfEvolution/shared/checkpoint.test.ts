import { describe, expect, it } from "vitest";
import {
  buildCheckpointWaitPrompt,
  buildFailureRetryPrompt,
  formatCheckpointCapability,
  formatCheckpointOperation,
  getFriendlyFailureReason,
  getPendingCheckpointWaitPrompt,
  isTerminalAbtestCheckpoint,
  requiresManualCheckpointAction,
  sanitizeCheckpointMessage,
} from "./checkpoint";
import type { NormalizedThreadEvent } from "./types";

describe("formatCheckpointOperation", () => {
  it("returns undefined for undefined input", () => {
    expect(formatCheckpointOperation(undefined)).toBeUndefined();
  });

  it("combines stage label and mapped action label", () => {
    expect(formatCheckpointOperation("abtest.candidate_cutover")).toBe("A/B 测试 · 候选切流");
  });

  it("falls back to underscore-replaced text for unknown actions", () => {
    expect(formatCheckpointOperation("dataset.some_custom_action")).toBe("数据集 · some custom action");
  });
});

describe("formatCheckpointCapability", () => {
  it("returns undefined for undefined input", () => {
    expect(formatCheckpointCapability(undefined)).toBeUndefined();
  });

  it("maps a known capability id to its label", () => {
    expect(formatCheckpointCapability("patch_dataset_case")).toBe("修补数据集 case");
  });

  it("falls back to underscore-replaced text for unknown capability ids", () => {
    expect(formatCheckpointCapability("custom_capability")).toBe("custom capability");
  });
});

describe("sanitizeCheckpointMessage", () => {
  it("strips task/dataset id parentheticals and collapses whitespace", () => {
    const message = sanitizeCheckpointMessage(
      "已完成数据集生成 (task_id=abc123)  ，请确认",
      undefined,
      undefined,
    );
    expect(message).not.toContain("task_id");
    expect(message).not.toMatch(/\s{2,}/);
  });

  it("falls back to a stage+next-op confirmation when cleaned text is too long", () => {
    const longText = "详细信息".repeat(60);
    const message = sanitizeCheckpointMessage(longText, "数据集", "评测");
    expect(message).toBe("数据集已完成，请确认是否进入下一步。");
  });

  it("falls back to a generic paused confirmation when no stage label is available", () => {
    const longText = "详细信息".repeat(60);
    expect(sanitizeCheckpointMessage(longText, undefined, undefined)).toBe("流程已暂停，请确认是否继续。");
  });
});

describe("buildCheckpointWaitPrompt", () => {
  it("builds a manual_cutover prompt with the cutover command", () => {
    const prompt = buildCheckpointWaitPrompt({
      payload: { checkpoint_kind: "manual_cutover", message: "请确认切流" },
    });
    expect(prompt.checkpointKind).toBe("manual_cutover");
    expect(prompt.command).toBe("确认切流");
  });

  it("builds an intent_confirmation prompt referencing the capability", () => {
    const prompt = buildCheckpointWaitPrompt({
      payload: { checkpoint_kind: "intent_confirmation", capability_id: "patch_dataset_case" },
    });
    expect(prompt.command).toBe("确认执行");
    expect(prompt.message).toContain("修补数据集 case");
  });

  it("resolves completedStage and nextStage from nested fields", () => {
    const prompt = buildCheckpointWaitPrompt({
      payload: {
        completed_stage: "dataset",
        next_op: { op: "eval.run" },
        message: "数据集已完成",
      },
    });
    expect(prompt.completedStage).toBe("dataset");
    expect(prompt.nextStage).toBe("eval");
  });

  it("uses the default paused-waiting message when none is provided", () => {
    const prompt = buildCheckpointWaitPrompt(undefined);
    expect(prompt.message).toBe("流程已暂停，等待确认");
  });
});

describe("isTerminalAbtestCheckpoint", () => {
  it("returns true when completedStage is abtest with no next stage", () => {
    expect(isTerminalAbtestCheckpoint({ completedStage: "abtest", message: "", command: "" })).toBe(true);
  });

  it("returns false when there is a next stage", () => {
    expect(
      isTerminalAbtestCheckpoint({ completedStage: "abtest", nextStage: "dataset", message: "", command: "" }),
    ).toBe(false);
  });

  it("returns false for undefined prompt", () => {
    expect(isTerminalAbtestCheckpoint(undefined)).toBe(false);
  });
});

describe("requiresManualCheckpointAction", () => {
  it("returns false for undefined prompt", () => {
    expect(requiresManualCheckpointAction(undefined)).toBe(false);
  });

  it("returns true for a failure prompt", () => {
    expect(requiresManualCheckpointAction({ kind: "failure", message: "", command: "" })).toBe(true);
  });

  it("returns true for manual_cutover/intent_confirmation checkpoint kinds", () => {
    expect(
      requiresManualCheckpointAction({ checkpointKind: "manual_cutover", message: "", command: "" }),
    ).toBe(true);
    expect(
      requiresManualCheckpointAction({ checkpointKind: "intent_confirmation", message: "", command: "" }),
    ).toBe(true);
  });

  it("returns false for a plain checkpoint prompt", () => {
    expect(requiresManualCheckpointAction({ kind: "checkpoint", message: "", command: "" })).toBe(false);
  });
});

describe("buildFailureRetryPrompt", () => {
  it("builds a failure message including stage label and reason", () => {
    const prompt = buildFailureRetryPrompt("dataset", { error_code: "2000509" });
    expect(prompt.kind).toBe("failure");
    expect(prompt.nextStage).toBe("dataset");
    expect(prompt.command).toBe("重试");
  });

  it("falls back to the generic current-step label when stage is undefined", () => {
    const prompt = buildFailureRetryPrompt(undefined, {});
    expect(prompt.message).toContain("当前步骤");
  });
});

describe("getFriendlyFailureReason", () => {
  it("resolves a localized message for an unmapped code by falling back to the generic error", () => {
    expect(getFriendlyFailureReason("9999999")).toBe(getFriendlyFailureReason(undefined));
  });
});

describe("getPendingCheckpointWaitPrompt", () => {
  const baseCheckpoint: NormalizedThreadEvent = {
    key: "checkpoint-1",
    type: "checkpoint.wait",
    sequence: 1,
    checkpointWait: {
      message: "等待确认",
      command: "继续",
      nextStage: "eval",
    },
  };

  it("returns undefined when there is an inactive terminal event", () => {
    const events: NormalizedThreadEvent[] = [
      baseCheckpoint,
      { key: "done-1", type: "done", sequence: 2, payload: { status: "cancelled" } },
    ];
    expect(getPendingCheckpointWaitPrompt(events)).toBeUndefined();
  });

  it("returns undefined when there is no checkpoint.wait event", () => {
    expect(getPendingCheckpointWaitPrompt([{ key: "e1", type: "message", sequence: 1 }])).toBeUndefined();
  });

  it("returns the latest checkpoint prompt when it has not been continued", () => {
    const events: NormalizedThreadEvent[] = [baseCheckpoint];
    expect(getPendingCheckpointWaitPrompt(events)).toEqual(baseCheckpoint.checkpointWait);
  });

  it("returns undefined once a later continue event for the next stage occurs", () => {
    const events: NormalizedThreadEvent[] = [
      baseCheckpoint,
      { key: "eval-1", type: "eval.start", stage: "eval", sequence: 2 },
    ];
    expect(getPendingCheckpointWaitPrompt(events)).toBeUndefined();
  });
});
