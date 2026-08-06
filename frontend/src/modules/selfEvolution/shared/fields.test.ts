import { describe, expect, it } from "vitest";
import {
  getEventArtifactId,
  getEventCaseId,
  getEventCaseProgress,
  getEventDetailField,
  getEventFlowKind,
  getEventPayloadData,
  getEventRuntimeArtifactId,
  getLastItem,
  getNestedArrayField,
  getNestedRecordField,
  getNestedStringField,
  getNumberField,
  getOperationRunId,
  getPayloadCaseTotal,
  getResultDownloadPath,
  getResultItems,
  getResultStringField,
  getStringField,
  getStructuredArrayField,
  getStructuredRecordField,
  getThreadEventContentFromPayload,
  getThreadEventPayloadEnvelope,
  getThreadEventTypeFromPayload,
  isEmptyResultPayload,
  isRecord,
  parseStructuredArray,
  parseStructuredRecord,
  stringifyResultPayload,
} from "./fields";

describe("isRecord", () => {
  it("returns true for plain objects", () => {
    expect(isRecord({ a: 1 })).toBe(true);
  });

  it("returns false for arrays and null", () => {
    expect(isRecord([1, 2])).toBe(false);
    expect(isRecord(null)).toBe(false);
    expect(isRecord("str")).toBe(false);
  });
});

describe("getStringField", () => {
  it("returns the first matching trimmed string field", () => {
    expect(getStringField({ a: "  hello  ", b: "world" }, ["a", "b"])).toBe("hello");
  });

  it("skips empty/blank strings and falls through to next key", () => {
    expect(getStringField({ a: "   ", b: "world" }, ["a", "b"])).toBe("world");
  });

  it("returns undefined when payload is undefined or no key matches", () => {
    expect(getStringField(undefined, ["a"])).toBeUndefined();
    expect(getStringField({ c: "x" }, ["a", "b"])).toBeUndefined();
  });
});

describe("getNumberField", () => {
  it("returns a finite numeric field", () => {
    expect(getNumberField({ a: 42 }, ["a"])).toBe(42);
  });

  it("parses a numeric string field", () => {
    expect(getNumberField({ a: "3.5" }, ["a"])).toBe(3.5);
  });

  it("ignores NaN/non-numeric strings and non-finite numbers", () => {
    expect(getNumberField({ a: "not-a-number", b: Infinity }, ["a", "b"])).toBeUndefined();
  });

  it("returns undefined for undefined payload", () => {
    expect(getNumberField(undefined, ["a"])).toBeUndefined();
  });
});

describe("getResultItems", () => {
  it("returns the value directly when it's already an array", () => {
    expect(getResultItems([1, 2, 3])).toEqual([1, 2, 3]);
  });

  it("extracts nested array from known keys", () => {
    expect(getResultItems({ results: [1, 2] })).toEqual([1, 2]);
    expect(getResultItems({ items: ["a"] })).toEqual(["a"]);
  });

  it("returns empty array for non-record non-array values", () => {
    expect(getResultItems("str")).toEqual([]);
    expect(getResultItems(null)).toEqual([]);
  });

  it("returns empty array when no known array key is present", () => {
    expect(getResultItems({ foo: "bar" })).toEqual([]);
  });
});

describe("isEmptyResultPayload", () => {
  it("treats null/undefined/blank string as empty", () => {
    expect(isEmptyResultPayload(null)).toBe(true);
    expect(isEmptyResultPayload(undefined)).toBe(true);
    expect(isEmptyResultPayload("   ")).toBe(true);
  });

  it("treats a record with cases array as non-empty", () => {
    expect(isEmptyResultPayload({ cases: [1] })).toBe(false);
  });

  it("treats a record with run_id as non-empty", () => {
    expect(isEmptyResultPayload({ run_id: "r1" })).toBe(false);
  });

  it("treats a record with nested content.cases as non-empty", () => {
    expect(isEmptyResultPayload({ content: { cases: [1] } })).toBe(false);
  });

  it("treats an empty object with no items as empty", () => {
    expect(isEmptyResultPayload({})).toBe(true);
  });

  it("treats an array of all-empty items as empty", () => {
    expect(isEmptyResultPayload([null, "", undefined])).toBe(true);
  });
});

describe("stringifyResultPayload", () => {
  it("returns the string unchanged when input is a string", () => {
    expect(stringifyResultPayload("hello")).toBe("hello");
  });

  it("JSON-stringifies objects with indentation", () => {
    expect(stringifyResultPayload({ a: 1 })).toBe(JSON.stringify({ a: 1 }, null, 2));
  });

  it("falls back to String() on circular references", () => {
    const circular: Record<string, unknown> = {};
    circular.self = circular;
    expect(stringifyResultPayload(circular)).toBe(String(circular));
  });
});

describe("getResultStringField", () => {
  it("returns a direct string value", () => {
    expect(getResultStringField("hello", ["x"])).toBe("hello");
  });

  it("finds a matching field in the first array item", () => {
    expect(getResultStringField([{ name: "first" }, { name: "second" }], ["name"])).toBe("first");
  });

  it("searches nested containers like data/result/report", () => {
    expect(getResultStringField({ data: { name: "nested" } }, ["name"])).toBe("nested");
  });

  it("returns undefined when nothing matches", () => {
    expect(getResultStringField({ other: "x" }, ["name"])).toBeUndefined();
  });
});

describe("getResultDownloadPath", () => {
  it("resolves a known download path key", () => {
    expect(getResultDownloadPath({ file_path: "/tmp/a.csv" })).toBe("/tmp/a.csv");
  });

  it("resolves from first truthy array item", () => {
    expect(getResultDownloadPath([undefined, { file_url: "/tmp/b.csv" }])).toBe("/tmp/b.csv");
  });

  it("returns undefined when no path field present", () => {
    expect(getResultDownloadPath({ foo: "bar" })).toBeUndefined();
  });
});

describe("getNestedStringField", () => {
  it("returns the direct field when present", () => {
    expect(getNestedStringField({ a: "direct" }, ["a"])).toBe("direct");
  });

  it("falls back to payload.data field", () => {
    expect(getNestedStringField({ data: { a: "nested" } }, ["a"])).toBe("nested");
  });

  it("returns undefined when neither location has the field", () => {
    expect(getNestedStringField({ data: {} }, ["a"])).toBeUndefined();
  });
});

describe("getNestedRecordField", () => {
  it("returns a direct record field", () => {
    expect(getNestedRecordField({ a: { x: 1 } }, ["a"])).toEqual({ x: 1 });
  });

  it("recurses into payload.data when top-level key missing", () => {
    expect(getNestedRecordField({ data: { a: { x: 2 } } }, ["a"])).toEqual({ x: 2 });
  });

  it("returns undefined for undefined payload", () => {
    expect(getNestedRecordField(undefined, ["a"])).toBeUndefined();
  });
});

describe("getNestedArrayField", () => {
  it("returns the payload directly if it's already an array", () => {
    expect(getNestedArrayField([1, 2], ["a"])).toEqual([1, 2]);
  });

  it("returns a direct array field", () => {
    expect(getNestedArrayField({ a: [1, 2] }, ["a"])).toEqual([1, 2]);
  });

  it("recurses through nested containers to find the array", () => {
    expect(getNestedArrayField({ data: { a: [3] } }, ["a"])).toEqual([3]);
  });

  it("returns empty array when payload is not a record/array", () => {
    expect(getNestedArrayField("str" as any, ["a"])).toEqual([]);
  });
});

describe("getEventPayloadData", () => {
  it("prefers payload.payload when it's a record", () => {
    expect(getEventPayloadData({ payload: { a: 1 }, data: { b: 2 } })).toEqual({ a: 1 });
  });

  it("falls back to payload.data", () => {
    expect(getEventPayloadData({ data: { b: 2 } })).toEqual({ b: 2 });
  });

  it("returns the payload itself when neither key is a record", () => {
    const input = { foo: "bar" };
    expect(getEventPayloadData(input)).toBe(input);
  });
});

describe("getThreadEventPayloadEnvelope", () => {
  it("returns payload.payload when it's a record", () => {
    expect(getThreadEventPayloadEnvelope({ payload: { a: 1 } })).toEqual({ a: 1 });
  });

  it("returns undefined when payload.payload is not a record", () => {
    expect(getThreadEventPayloadEnvelope({ payload: "str" })).toBeUndefined();
  });
});

describe("getThreadEventTypeFromPayload", () => {
  it("prefers a direct tag field", () => {
    expect(getThreadEventTypeFromPayload({ tag: "dataset.step" })).toBe("dataset.step");
  });

  it("falls back to nested envelope tag", () => {
    expect(getThreadEventTypeFromPayload({ payload: { tag: "eval.step" } })).toBe("eval.step");
  });

  it("derives message.user/assistant from stage+event when no tag present", () => {
    expect(getThreadEventTypeFromPayload({ stage: "message", event: "user" })).toBe("message.user");
    expect(getThreadEventTypeFromPayload({ stage: "message", event: "assistant" })).toBe("message.assistant");
  });

  it("falls back to event_type field when no tag/message pattern matches", () => {
    expect(getThreadEventTypeFromPayload({ event_type: "custom.event" })).toBe("custom.event");
  });
});

describe("getThreadEventContentFromPayload", () => {
  it("extracts content from the top-level payload", () => {
    expect(getThreadEventContentFromPayload({ content: "hello" })).toBe("hello");
  });

  it("extracts content from a nested envelope", () => {
    expect(getThreadEventContentFromPayload({ payload: { text: "nested" } })).toBe("nested");
  });

  it("returns undefined when no content-like field is found", () => {
    expect(getThreadEventContentFromPayload({ foo: "bar" })).toBeUndefined();
  });
});

describe("getOperationRunId", () => {
  it("reads operation_run_id from event data", () => {
    expect(getOperationRunId({ payload: { operation_run_id: "run-1" } })).toBe("run-1");
  });

  it("reads operation_run_id from nested after/before record", () => {
    expect(
      getOperationRunId({ payload: { after: { operation_run_id: "run-2" } } }),
    ).toBe("run-2");
  });

  it("falls back to top-level payload field", () => {
    expect(getOperationRunId({ operation_run_id: "run-3" })).toBe("run-3");
  });
});

describe("getEventFlowKind", () => {
  it("maps a known event_type value to a namespaced flow kind", () => {
    expect(getEventFlowKind({ event_type: "generate_case" })).toBe("dataset.generate_case");
  });

  it("falls back to raw flow_kind from data when event_type unmapped", () => {
    expect(getEventFlowKind({ payload: { flow_kind: "custom_flow" } })).toBe("custom_flow");
  });

  it("returns undefined when nothing is present", () => {
    expect(getEventFlowKind({})).toBeUndefined();
  });
});

describe("getEventCaseId", () => {
  it("reads case_id from event data", () => {
    expect(getEventCaseId({ payload: { case_id: "c1" } })).toBe("c1");
  });

  it("reads id from nested case record", () => {
    expect(getEventCaseId({ payload: { case: { id: "c2" } } })).toBe("c2");
  });

  it("returns undefined when absent", () => {
    expect(getEventCaseId({})).toBeUndefined();
  });
});

describe("getEventCaseProgress", () => {
  it("returns a progress object when case_index is numeric", () => {
    expect(getEventCaseProgress({ payload: { case_index: 3 } })).toEqual({ current: 3 });
  });

  it("returns undefined when case_index is missing", () => {
    expect(getEventCaseProgress({})).toBeUndefined();
  });
});

describe("getEventArtifactId", () => {
  it("reads artifact_id directly from event data", () => {
    expect(getEventArtifactId({ payload: { artifact_id: "a1" } })).toBe("a1");
  });

  it("reads writes_artifact_id from nested detail", () => {
    expect(getEventArtifactId({ payload: { detail: { writes_artifact_id: "a2" } } })).toBe("a2");
  });

  it("returns undefined when neither location has an id", () => {
    expect(getEventArtifactId({})).toBeUndefined();
  });
});

describe("getEventRuntimeArtifactId", () => {
  it("reads runtime_artifact_id from event data", () => {
    expect(getEventRuntimeArtifactId({ payload: { runtime_artifact_id: "r1" } })).toBe("r1");
  });

  it("reads source_artifact_id from nested detail", () => {
    expect(
      getEventRuntimeArtifactId({ payload: { detail: { source_artifact_id: "r2" } } }),
    ).toBe("r2");
  });
});

describe("getEventDetailField", () => {
  it("resolves a field directly from event data", () => {
    expect(getEventDetailField({ payload: { foo: "bar" } }, ["foo"])).toBe("bar");
  });

  it("resolves a field from nested detail record", () => {
    expect(getEventDetailField({ payload: { detail: { foo: "baz" } } }, ["foo"])).toBe("baz");
  });

  it("falls back to the raw payload field", () => {
    expect(getEventDetailField({ foo: "top" }, ["foo"])).toBe("top");
  });
});

describe("getPayloadCaseTotal", () => {
  it("reads total from event data", () => {
    expect(getPayloadCaseTotal({ total: 10 })).toBe(10);
  });

  it("reads case_count from nested detail", () => {
    expect(getPayloadCaseTotal({ detail: { case_count: 5 } })).toBe(5);
  });

  it("returns undefined when no count field present", () => {
    expect(getPayloadCaseTotal({})).toBeUndefined();
  });
});

describe("parseStructuredRecord", () => {
  it("returns the value directly when it's already a record", () => {
    expect(parseStructuredRecord({ a: 1 })).toEqual({ a: 1 });
  });

  it("parses a JSON object embedded in a fenced code block", () => {
    const text = "```json\n{\"a\":1}\n```";
    expect(parseStructuredRecord(text)).toEqual({ a: 1 });
  });

  it("parses a plain JSON object string", () => {
    expect(parseStructuredRecord('{"b":2}')).toEqual({ b: 2 });
  });

  it("returns undefined for unparsable input", () => {
    expect(parseStructuredRecord("not json at all")).toBeUndefined();
    expect(parseStructuredRecord(42)).toBeUndefined();
  });
});

describe("parseStructuredArray", () => {
  it("returns the value directly when already an array", () => {
    expect(parseStructuredArray([1, 2])).toEqual([1, 2]);
  });

  it("parses a JSON array string", () => {
    expect(parseStructuredArray("[1,2,3]")).toEqual([1, 2, 3]);
  });

  it("returns undefined for non-array JSON or invalid JSON", () => {
    expect(parseStructuredArray('{"a":1}')).toBeUndefined();
    expect(parseStructuredArray("not json")).toBeUndefined();
  });
});

describe("getStructuredRecordField", () => {
  it("parses a stringified JSON object field", () => {
    expect(getStructuredRecordField({ detail: '{"x":1}' }, ["detail"])).toEqual({ x: 1 });
  });

  it("returns undefined when payload is undefined or no field parses", () => {
    expect(getStructuredRecordField(undefined, ["detail"])).toBeUndefined();
    expect(getStructuredRecordField({ detail: "not json" }, ["detail"])).toBeUndefined();
  });
});

describe("getStructuredArrayField", () => {
  it("returns a direct array field", () => {
    expect(getStructuredArrayField({ items: [1, 2] }, ["items"])).toEqual([1, 2]);
  });

  it("parses a stringified JSON array field", () => {
    expect(getStructuredArrayField({ items: "[1,2]" }, ["items"])).toEqual([1, 2]);
  });

  it("returns undefined when payload is undefined or nothing matches", () => {
    expect(getStructuredArrayField(undefined, ["items"])).toBeUndefined();
    expect(getStructuredArrayField({ items: "not json" }, ["items"])).toBeUndefined();
  });
});

describe("getLastItem", () => {
  it("returns the last element of a non-empty array", () => {
    expect(getLastItem([1, 2, 3])).toBe(3);
  });

  it("returns undefined for an empty array", () => {
    expect(getLastItem([])).toBeUndefined();
  });
});
