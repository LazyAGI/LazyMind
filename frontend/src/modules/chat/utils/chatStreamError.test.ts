import { describe, expect, it } from "vitest";
import {
  applyChatStreamFailure,
  MODEL_FAILURE_CODES,
  parseCoreChatStreamError,
} from "./chatStreamError";

describe("Core chat stream error mapping", () => {
  it.each([
    [2001597, "model config unavailable", 503, "not_found"],
    [2001421, "model not found in selected models", 404, "not_found"],
    [2001312, "api_key is required", 400, "authentication_failed"],
  ])(
    "maps code %s at HTTP %s to %s",
    (appCode, message, status, semanticCode) => {
      expect(
        parseCoreChatStreamError(
          JSON.stringify({ code: appCode, message }),
          status,
        ),
      ).toEqual({ appCode, httpStatus: status, semanticCode });
    },
  );

  it.each([...MODEL_FAILURE_CODES])("accepts semantic code %s without returning provider text", (code) => {
    const mapped = parseCoreChatStreamError(
      {
        code,
        message: "provider secret diagnostic",
      },
      400,
    );

    expect(mapped).toEqual({
      appCode: code,
      httpStatus: 400,
      semanticCode: code,
    });
    expect(JSON.stringify(mapped)).not.toContain("provider secret diagnostic");
  });

  it.each([
    ["not json", 503],
    [JSON.stringify({ message: "no code" }), 503],
    [JSON.stringify({ code: 2001597, message: "model config unavailable" }), 0],
    [JSON.stringify({ code: 2002022, message: "at most one workflow mention" }), 400],
    [JSON.stringify({ code: 2000102, message: "forbidden" }), 403],
    [JSON.stringify({ code: 2000000, message: "Internal server error" }), 500],
  ])("leaves transport or unstructured failures to stream recovery", (data, status) => {
    expect(parseCoreChatStreamError(data, status)).toBeUndefined();
  });
});

describe("applyChatStreamFailure", () => {
  it("keeps the user turn and converts the empty assistant into a failure card", () => {
    const original = [
      {
        role: "user",
        delta: "question",
        files: [{ name: "brief.pdf" }],
        inputs: [{ input_type: "file", uri: "upload://brief" }],
      },
      { role: "assistant", delta: "", answers: [] },
    ];

    const next = applyChatStreamFailure(
      original,
      "assistant",
      "authentication_failed",
    );

    expect(next).not.toBe(original);
    expect(next[0]).toBe(original[0]);
    expect(next[1]).toMatchObject({
      role: "assistant",
      delta: "",
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "model_failure",
        code: "authentication_failed",
        partial_output: false,
      },
    });
  });

  it("preserves partial assistant output and marks it as partial", () => {
    const original = [
      { role: "user", delta: "question" },
      { role: "assistant", delta: "partial answer", history_id: "history-1" },
    ];

    const next = applyChatStreamFailure(
      original,
      "assistant",
      "service_unavailable",
    );

    expect(next[1]).toMatchObject({
      delta: "partial answer",
      history_id: "history-1",
      run_terminal: { partial_output: true },
    });
  });
});
