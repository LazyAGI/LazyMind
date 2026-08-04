import { describe, expect, it } from "vitest";
import {
  buildTerminalStatusByStage,
  compareNormalizedThreadEvents,
  dedupeNormalizedEvents,
  getChatStreamDeltaKind,
  getFlowStatusFromPayload,
  getNextStageFromOperation,
  getNormalizedEventDedupeKey,
  getStageLabel,
  isCheckpointGateFlowStatus,
  isDoneSSEFrame,
  isEventStreamTerminalFlowPayload,
  isEventStreamTerminalFlowStatus,
  isFailedThreadEvent,
  isInactiveTerminalThreadEvent,
  isMessageStreamAssistantEvent,
  isPausedFlowEvent,
  isPausedFlowPayload,
  isTerminalThreadEvent,
  isThreadEventAfter,
  normalizeThreadEvent,
  parseSSEFrame,
  parseThreadEventPayload,
  resolveCompletedStageFromDonePayload,
  resolveTerminalStepStatusFromFlowStatus,
  shouldDisconnectThreadEventStream,
  toThreadEventStage,
} from "./threadEvents";
import type { NormalizedThreadEvent } from "./types";

describe("resolveCompletedStageFromDonePayload", () => {
  it("returns undefined for an undefined payload", () => {
    expect(resolveCompletedStageFromDonePayload(undefined)).toBeUndefined();
  });

  it("prefers retry_from_step when present", () => {
    expect(resolveCompletedStageFromDonePayload({ retry_from_step: "eval" })).toBe("eval");
  });

  it("resolves the previous stage when paused mid-current_step", () => {
    expect(
      resolveCompletedStageFromDonePayload({ current_step: "analysis", status: "paused" }),
    ).toBe("eval");
  });

  it("falls back to last_released_step", () => {
    expect(resolveCompletedStageFromDonePayload({ last_released_step: "dataset" })).toBe("dataset");
  });
});

describe("isCheckpointGateFlowStatus", () => {
  it("returns true for paused/waiting_checkpoint/completed", () => {
    expect(isCheckpointGateFlowStatus("paused")).toBe(true);
    expect(isCheckpointGateFlowStatus("waiting_checkpoint")).toBe(true);
    expect(isCheckpointGateFlowStatus("completed")).toBe(true);
  });

  it("returns false for running/failed", () => {
    expect(isCheckpointGateFlowStatus("running")).toBe(false);
    expect(isCheckpointGateFlowStatus(undefined)).toBe(false);
  });
});

describe("isEventStreamTerminalFlowStatus / Payload", () => {
  it("treats completed/paused/failed as terminal statuses", () => {
    expect(isEventStreamTerminalFlowStatus("completed")).toBe(true);
    expect(isEventStreamTerminalFlowStatus("paused")).toBe(true);
    expect(isEventStreamTerminalFlowStatus("failed")).toBe(true);
    expect(isEventStreamTerminalFlowStatus("running")).toBe(false);
  });

  it("requires a done-like event_type plus a terminal status", () => {
    expect(isEventStreamTerminalFlowPayload({ event_type: "done", status: "completed" })).toBe(true);
    expect(isEventStreamTerminalFlowPayload({ event_type: "progress", status: "completed" })).toBe(false);
    expect(isEventStreamTerminalFlowPayload(undefined)).toBe(false);
  });
});

describe("getFlowStatusFromPayload / isPausedFlowPayload / isPausedFlowEvent", () => {
  it("extracts and normalizes the flow status", () => {
    expect(getFlowStatusFromPayload({ status: " Paused " })).toBe("paused");
    expect(getFlowStatusFromPayload(undefined)).toBeUndefined();
  });

  it("detects paused payloads and events", () => {
    expect(isPausedFlowPayload({ status: "paused" })).toBe(true);
    expect(isPausedFlowEvent({ payload: { state: "paused" } })).toBe(true);
    expect(isPausedFlowEvent({ payload: { status: "running" } })).toBe(false);
  });
});

describe("shouldDisconnectThreadEventStream", () => {
  it("returns true for a terminal event type", () => {
    expect(shouldDisconnectThreadEventStream({ type: "done", payload: undefined })).toBe(true);
  });

  it("returns true when the payload itself signals a terminal/paused flow", () => {
    expect(shouldDisconnectThreadEventStream({ type: "custom", payload: { status: "paused" } })).toBe(true);
  });

  it("returns false for a normal in-flight event", () => {
    expect(shouldDisconnectThreadEventStream({ type: "dataset.progress", payload: { status: "running" } })).toBe(false);
  });
});

describe("resolveTerminalStepStatusFromFlowStatus", () => {
  it("maps paused to done", () => {
    expect(resolveTerminalStepStatusFromFlowStatus("paused")).toBe("done");
  });

  it("maps failed/error to failed", () => {
    expect(resolveTerminalStepStatusFromFlowStatus("failed")).toBe("failed");
    expect(resolveTerminalStepStatusFromFlowStatus("error")).toBe("failed");
  });

  it("maps cancelled/canceled to canceled", () => {
    expect(resolveTerminalStepStatusFromFlowStatus("cancelled")).toBe("canceled");
  });

  it("defaults to done for unknown/undefined status", () => {
    expect(resolveTerminalStepStatusFromFlowStatus(undefined)).toBe("done");
  });
});

describe("buildTerminalStatusByStage", () => {
  it("collects the terminal status for each stage from terminal events", () => {
    const events: NormalizedThreadEvent[] = [
      { key: "e1", type: "done", stage: "dataset", payload: { status: "completed" } },
      { key: "e2", type: "dataset.progress", stage: "dataset", payload: { status: "running" } },
    ];
    expect(buildTerminalStatusByStage(events)).toEqual({ dataset: "done" });
  });

  it("returns an empty object when no events are terminal", () => {
    const events: NormalizedThreadEvent[] = [
      { key: "e1", type: "dataset.progress", stage: "dataset", payload: { status: "running" } },
    ];
    expect(buildTerminalStatusByStage(events)).toEqual({});
  });
});

describe("toThreadEventStage", () => {
  it("maps known raw values to their thread event stage", () => {
    expect(toThreadEventStage("candidate_eval")).toBe("abtest");
    expect(toThreadEventStage("run")).toBe("analysis");
    expect(toThreadEventStage("apply")).toBe("repair");
  });

  it("returns undefined for unmapped values or non-strings", () => {
    expect(toThreadEventStage("unknown")).toBeUndefined();
    expect(toThreadEventStage(42)).toBeUndefined();
  });
});

describe("getStageLabel", () => {
  it("returns a localized label for a mappable stage value", () => {
    expect(getStageLabel("dataset")).toBe("数据集");
  });

  it("returns the raw trimmed string when it can't be mapped to a stage", () => {
    expect(getStageLabel("custom stage")).toBe("custom stage");
  });

  it("returns undefined for non-string/blank values", () => {
    expect(getStageLabel(undefined)).toBeUndefined();
  });
});

describe("getNextStageFromOperation", () => {
  it("extracts the stage from a dotted operation string", () => {
    expect(getNextStageFromOperation("eval.run")).toBe("eval");
  });

  it("returns undefined for undefined input", () => {
    expect(getNextStageFromOperation(undefined)).toBeUndefined();
  });
});

describe("parseSSEFrame", () => {
  it("parses id/event/data lines from a raw SSE frame", () => {
    const frame = parseSSEFrame("id: 1\nevent: dataset.progress\ndata: {\"a\":1}");
    expect(frame).toEqual({ id: "1", eventName: "dataset.progress", data: '{"a":1}' });
  });

  it("defaults eventName to message when omitted", () => {
    const frame = parseSSEFrame("data: hello");
    expect(frame?.eventName).toBe("message");
  });

  it("returns undefined when there are no data lines", () => {
    expect(parseSSEFrame("event: ping")).toBeUndefined();
  });
});

describe("parseThreadEventPayload", () => {
  it("parses a JSON object payload directly", () => {
    expect(parseThreadEventPayload('{"a":1}')).toEqual({ a: 1 });
  });

  it("wraps a non-object JSON value under a value key", () => {
    expect(parseThreadEventPayload("42")).toEqual({ value: 42 });
  });

  it("returns undefined for invalid JSON", () => {
    expect(parseThreadEventPayload("not json")).toBeUndefined();
  });
});

describe("getChatStreamDeltaKind", () => {
  it("maps thinking/answer delta types", () => {
    expect(getChatStreamDeltaKind("thinking_delta")).toBe("thinking");
    expect(getChatStreamDeltaKind("intent.answer_delta")).toBe("answer");
  });

  it("returns undefined for unrelated types", () => {
    expect(getChatStreamDeltaKind("dataset.progress")).toBeUndefined();
  });
});

describe("isTerminalThreadEvent / isFailedThreadEvent", () => {
  it("recognizes known terminal and failed event types", () => {
    expect(isTerminalThreadEvent("done")).toBe(true);
    expect(isTerminalThreadEvent("dataset.progress")).toBe(false);
    expect(isFailedThreadEvent("error")).toBe(true);
    expect(isFailedThreadEvent("done")).toBe(false);
  });
});

describe("isMessageStreamAssistantEvent", () => {
  it("recognizes assistant message types/event names", () => {
    expect(isMessageStreamAssistantEvent("message.assistant", "message", undefined)).toBe(true);
    expect(isMessageStreamAssistantEvent("custom", "assistant_response", undefined)).toBe(true);
    expect(isMessageStreamAssistantEvent("custom", "message", { original_type: "assistant_response" })).toBe(true);
  });

  it("returns false for unrelated types", () => {
    expect(isMessageStreamAssistantEvent("dataset.progress", "message", undefined)).toBe(false);
  });
});

describe("isDoneSSEFrame", () => {
  it("returns true for a terminal event name", () => {
    expect(isDoneSSEFrame({ eventName: "done", data: "{}" })).toBe(true);
  });

  it("returns true for a [DONE] sentinel frame", () => {
    expect(isDoneSSEFrame({ eventName: "message", data: "[DONE]" })).toBe(true);
  });

  it("returns true when the payload contains a terminal event type", () => {
    expect(isDoneSSEFrame({ eventName: "message", data: '{"event_type":"done"}' })).toBe(true);
  });

  it("returns false for a normal progress frame", () => {
    expect(isDoneSSEFrame({ eventName: "message", data: '{"event_type":"dataset.progress"}' })).toBe(false);
  });
});

describe("isInactiveTerminalThreadEvent", () => {
  it("returns true for a terminal event with an inactive status", () => {
    expect(
      isInactiveTerminalThreadEvent({ key: "k1", type: "done", payload: { status: "cancelled" } }),
    ).toBe(true);
  });

  it("returns false for a terminal event with a non-inactive status", () => {
    expect(
      isInactiveTerminalThreadEvent({ key: "k1", type: "done", payload: { status: "completed" } }),
    ).toBe(false);
  });

  it("returns false for a non-terminal event", () => {
    expect(
      isInactiveTerminalThreadEvent({ key: "k1", type: "dataset.progress", payload: {} }),
    ).toBe(false);
  });
});

describe("normalizeThreadEvent", () => {
  it("normalizes a dataset progress event into a stage/action/type", () => {
    const event = normalizeThreadEvent({
      eventName: "message",
      data: JSON.stringify({
        event_type: "generate_case",
        stage: "dataset",
        action: "progress",
        data: { current: 2, total: 10 },
      }),
    });
    expect(event.stage).toBe("dataset");
    expect(event.action).toBe("progress");
    expect(event.type).toBe("dataset.progress");
    expect(event.progress?.percent).toBeGreaterThanOrEqual(0);
  });

  it("normalizes a terminal done event with an end-of-stream display text", () => {
    const event = normalizeThreadEvent({
      eventName: "done",
      data: JSON.stringify({ status: "completed", current_step: "abtest" }),
    });
    expect(event.type).toBe("done");
    expect(event.displayText).toBe("事件流已结束，线程停止信号已收到。");
  });

  it("normalizes a failed event with a localized error message", () => {
    const event = normalizeThreadEvent({
      eventName: "error",
      data: JSON.stringify({ error_code: "2000509" }),
    });
    expect(event.role).toBe("assistant");
    expect(event.content).toBeTruthy();
  });

  it("normalizes a message.user event with the role and content set", () => {
    const event = normalizeThreadEvent({
      eventName: "message",
      data: JSON.stringify({ stage: "message", event: "user", content: "hello" }),
    });
    expect(event.type).toBe("message.user");
    expect(event.role).toBe("user");
    expect(event.content).toBe("hello");
  });

  it("normalizes a checkpoint.wait event with a checkpointWait prompt", () => {
    const event = normalizeThreadEvent({
      eventName: "message",
      data: JSON.stringify({ event_type: "checkpoint.wait", message: "请确认" }),
    });
    expect(event.type).toBe("checkpoint.wait");
    expect(event.checkpointWait).toBeDefined();
  });

  it("falls back to a compact fallback text when the event has no stage", () => {
    const event = normalizeThreadEvent({
      eventName: "message",
      data: JSON.stringify({ custom_field: "value" }),
    });
    expect(event.stage).toBeUndefined();
  });
});

describe("compareNormalizedThreadEvents", () => {
  it("orders by sequence number first", () => {
    const a: NormalizedThreadEvent = { key: "a", type: "x", sequence: 2 };
    const b: NormalizedThreadEvent = { key: "b", type: "x", sequence: 1 };
    expect(compareNormalizedThreadEvents(a, b)).toBeGreaterThan(0);
  });

  it("falls back to timestamp when sequences are equal/absent", () => {
    const a: NormalizedThreadEvent = { key: "a", type: "x", timestamp: "2026-01-02T00:00:00Z" };
    const b: NormalizedThreadEvent = { key: "b", type: "x", timestamp: "2026-01-01T00:00:00Z" };
    expect(compareNormalizedThreadEvents(a, b)).toBeGreaterThan(0);
  });

  it("falls back to key comparison when neither sequence nor timestamp differs", () => {
    const a: NormalizedThreadEvent = { key: "a", type: "x" };
    const b: NormalizedThreadEvent = { key: "b", type: "x" };
    expect(compareNormalizedThreadEvents(a, b)).toBeLessThan(0);
  });
});

describe("getNormalizedEventDedupeKey / dedupeNormalizedEvents", () => {
  it("builds a stable dedupe key from event fields", () => {
    const event: NormalizedThreadEvent = {
      key: "fallback",
      type: "dataset.progress",
      sequence: 1,
      payload: { thread_id: "t1", event_id: "ev1" },
    };
    expect(getNormalizedEventDedupeKey(event)).toContain("t1");
    expect(getNormalizedEventDedupeKey(event)).toContain("ev1");
  });

  it("removes duplicate events sharing the same dedupe key and sorts the rest", () => {
    const events: NormalizedThreadEvent[] = [
      { key: "e2", type: "dataset.progress", sequence: 1, payload: { event_id: "same" } },
      { key: "e1", type: "dataset.progress", sequence: 1, payload: { event_id: "same" } },
    ];
    const result = dedupeNormalizedEvents(events);
    expect(result).toHaveLength(1);
  });
});

describe("isThreadEventAfter", () => {
  it("uses sequence numbers when both are present and differ", () => {
    expect(
      isThreadEventAfter({ sequence: 1, key: "a" }, { sequence: 2, key: "b" }),
    ).toBe(true);
  });

  it("falls back to timestamp comparison when sequences are absent", () => {
    expect(
      isThreadEventAfter(
        { timestamp: "2026-01-01T00:00:00Z", key: "a" },
        { timestamp: "2026-01-02T00:00:00Z", key: "b" },
      ),
    ).toBe(true);
  });
});
