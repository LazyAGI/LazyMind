import { describe, expect, it } from "vitest";
import {
  enrichProjectionEventPayload,
  getProjectionEventType,
  isProjectionEventName,
  mapProjectionAction,
} from "./projectionEvents";

describe("mapProjectionAction", () => {
  it("maps known raw actions to normalized actions", () => {
    expect(mapProjectionAction("completed")).toBe("finish");
    expect(mapProjectionAction("running")).toBe("progress");
    expect(mapProjectionAction("cancelled")).toBe("cancel");
  });

  it("returns the raw action unchanged when unmapped", () => {
    expect(mapProjectionAction("weird")).toBe("weird");
  });

  it("returns undefined for undefined input", () => {
    expect(mapProjectionAction(undefined)).toBeUndefined();
  });
});

describe("isProjectionEventName", () => {
  it("returns false for message/done and checkpoint/autooperator events", () => {
    expect(isProjectionEventName("message")).toBe(false);
    expect(isProjectionEventName("done")).toBe(false);
    expect(isProjectionEventName("checkpoint.wait")).toBe(false);
    expect(isProjectionEventName("autooperator.start")).toBe(false);
  });

  it("returns false for undefined/plain names without a dot", () => {
    expect(isProjectionEventName(undefined)).toBe(false);
    expect(isProjectionEventName("plainname")).toBe(false);
  });

  it("returns true for a dotted projection-style event name", () => {
    expect(isProjectionEventName("dataset.generate_case")).toBe(true);
  });
});

describe("getProjectionEventType", () => {
  it("prefers event_type from the payload when it's a valid projection name", () => {
    expect(getProjectionEventType({ event_type: "dataset.generate_case" })).toBe(
      "dataset.generate_case",
    );
  });

  it("returns undefined when payload event_type is done or not a projection name", () => {
    expect(getProjectionEventType({ event_type: "done" })).toBeUndefined();
    expect(getProjectionEventType({ event_type: "checkpoint.wait" })).toBeUndefined();
  });

  it("falls back to the frame event name when no payload event_type is present", () => {
    expect(getProjectionEventType(undefined, "eval.judge_case")).toBe("eval.judge_case");
  });

  it("returns undefined when neither payload nor event name qualifies", () => {
    expect(getProjectionEventType(undefined, "message")).toBeUndefined();
  });
});

describe("enrichProjectionEventPayload", () => {
  it("sets flow_kind/operation_run_id and merges case fields into data", () => {
    const payload = {
      case: { id: "c1", question: "q?", answer: "a", question_type: "1" },
      action: "completed",
    };
    const enriched = enrichProjectionEventPayload(payload, "dataset.generate_case");

    expect(enriched.event_type).toBe("dataset.generate_case");
    expect(enriched.action).toBe("finish");
    expect(enriched.stage).toBe("dataset");
    expect((enriched.data as Record<string, unknown>).flow_kind).toBe("dataset.generate_case");
    expect((enriched.data as Record<string, unknown>).case_id).toBe("c1");
    expect((enriched.data as Record<string, unknown>).question).toBe("q?");
  });

  it("carries over progress current/total into the data record", () => {
    const payload = { progress: { current: 3, total: 10 } };
    const enriched = enrichProjectionEventPayload(payload, "eval.judge_case");
    const data = enriched.data as Record<string, unknown>;
    expect(data.current).toBe(3);
    expect(data.total).toBe(10);
    expect(data.case_num).toBe(10);
  });

  it("maps artifact ref/id into artifact_id and runtime_artifact_id", () => {
    const payload = { artifact: { ref: "artifact-ref-1" } };
    const enriched = enrichProjectionEventPayload(payload, "dataset.assemble");
    const data = enriched.data as Record<string, unknown>;
    expect(data.artifact_id).toBe("artifact-ref-1");
    expect(data.runtime_artifact_id).toBe("artifact-ref-1");
  });

  it("does not overwrite an existing explicit stage field", () => {
    const payload = { stage: "custom_stage" };
    const enriched = enrichProjectionEventPayload(payload, "dataset.generate_case");
    expect(enriched.stage).toBe("custom_stage");
  });

  it("merges with pre-existing payload.data instead of replacing it", () => {
    const payload = { data: { existing: "value" } };
    const enriched = enrichProjectionEventPayload(payload, "dataset.generate_case");
    const data = enriched.data as Record<string, unknown>;
    expect(data.existing).toBe("value");
    expect(data.flow_kind).toBe("dataset.generate_case");
  });
});
