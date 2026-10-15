import { describe, expect, it } from "vitest";
import {
  applyChatStreamFailure,
  MODEL_FAILURE_CODES,
  parseCoreChatStreamError,
} from "./chatStreamError";

describe("Core chat stream error mapping", () => {
  it.each([
    { status: "failed", reason: "model_failure", code: "authentication_failed", partial_output: false, model_invoked: true },
    { status: "failed", reason: "runtime_failure", code: "protocol_error", partial_output: true, model_invoked: false },
    { status: "failed", reason: "model_failure", code: "future_provider_code", partial_output: false },
    { status: "interrupted", reason: "model_incomplete", code: "length", partial_output: true },
    { status: "cancelled", reason: "user_cancelled", partial_output: false },
  ])("uses authoritative HTTP terminal over legacy code: $reason/$code", (terminal) => {
    expect(parseCoreChatStreamError({ code: 2001597, message: "private provider message",
      data: { run_terminal: terminal, detail: { history_id: "history-contract" } },
    }, 503)).toEqual({ appCode: 2001597, httpStatus: 503, terminal, historyId: "history-contract" });
  });

  it("does not require message text for an authoritative terminal", () => {
    const terminal = { status: "failed", reason: "model_failure", code: "model_configuration", partial_output: false };
    expect(parseCoreChatStreamError({ code: 2001597, data: { run_terminal: terminal } }, 503))
      .toEqual({ appCode: 2001597, httpStatus: 503, terminal });
  });

  it.each([
    { status: "failed", reason: "model_failure", code: "model_configuration" },
    { status: "failed", reason: "normal", code: "model_configuration", partial_output: false },
    { status: "completed", reason: "normal", partial_output: false },
    { status: "failed", reason: "model_failure", code: "model_configuration", partial_output: "false" },
    { status: "failed", reason: "model_failure", code: "model_configuration", partial_output: false, model_invoked: "false" },
    { status: "failed", reason: ["model_failure"], code: "model_configuration", partial_output: false },
    { status: "failed", reason: "runtime_failure", code: "transport_error", partial_output: false, transport_diagnostic: { code: ["upstream_stream_failed"] } },
  ])("uses safe fallback for malformed terminal without legacy reclassification", (terminal) => {
    expect(parseCoreChatStreamError({ code: 2001597, message: "model config unavailable",
      data: { run_terminal: terminal },
    }, 503)).toEqual({ appCode: 2001597, httpStatus: 503,
      terminal: { status: "failed", reason: "runtime_failure", code: "request_rejected", partial_output: false },
    });
  });

  it("retains public terminal metadata and discards arbitrary provider fields", () => {
    const terminal = { status: "failed", reason: "runtime_failure", code: "transport_error", partial_output: true,
      model_invoked: true, model_call_id: "call-http", diagnostic_id: "diag_http",
      transport_diagnostic: { code: "upstream_stream_timeout" },
    };
    expect(parseCoreChatStreamError({ code: 2000000, message: "private error",
      data: { run_terminal: { ...terminal, provider_message: "secret provider payload" } },
    }, 502)).toEqual({ appCode: 2000000, httpStatus: 502, terminal });
  });

  it.each([
    [2001597, "model config unavailable", 503, "model_configuration"],
    [2001421, "model not found in selected models", 404, "not_found"],
    [2001312, "api_key is required", 400, "model_configuration"],
  ])(
    "maps code %s at HTTP %s to %s",
    (appCode, message, status, semanticCode) => {
      expect(
        parseCoreChatStreamError(
          JSON.stringify({ code: appCode, message }),
          status,
        ),
      ).toEqual({ appCode, httpStatus: status, legacy: true,
        terminal: { status: "failed", code: semanticCode, reason: "model_failure", partial_output: false } });
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
      legacy: true,
      terminal: { status: "failed", code, reason: "model_failure", partial_output: false },
    });
    expect(JSON.stringify(mapped)).not.toContain("provider secret diagnostic");
  });

  it.each([2000959, 2001312, 2001313, 2001314, 2001383, 2001597])(
    "classifies Core configuration code %s without guessing authentication failure", (code) => {
      expect(parseCoreChatStreamError({ code, message: "private diagnostic" }, 400))
        .toMatchObject({ terminal: { code: "model_configuration", reason: "model_failure" } });
    },
  );

  it.each(["failed while loading api_key metadata", "model config unavailable", "workflow model not found"])(
    "does not classify an unknown code from message text: %s", (message) => {
      expect(parseCoreChatStreamError({ code: 2000000, message }, 500))
        .toMatchObject({ terminal: { code: "request_rejected", reason: "runtime_failure" } });
    },
  );

  it.each([
    ["not json", 503],
    [JSON.stringify({ message: "no code" }), 503],
    [JSON.stringify({ code: 2001597, message: "model config unavailable" }), 0],
    [JSON.stringify({ code: 2000000, message: "Internal server error" }), 200],
  ])("leaves transport or unstructured failures to stream recovery", (data, status) => {
    expect(parseCoreChatStreamError(data, status)).toBeUndefined();
  });

  it.each([400, 403, 500, 503])("does not resume a structured HTTP %s rejection", (status) => {
    expect(parseCoreChatStreamError({ code: 2000000, message: "private diagnostic" }, status))
      .toEqual({ appCode: 2000000, httpStatus: status, legacy: true,
        terminal: { status: "failed", code: "request_rejected", reason: "runtime_failure", partial_output: false } });
  });

  it.each([
    [2002022, "at most one workflow mention", 400],
    [2000102, "forbidden", 403],
  ])("preserves upstream rejection mapping for code %s", (appCode, message, status) => {
    expect(parseCoreChatStreamError(JSON.stringify({ code: appCode, message }), status)).toEqual({
      appCode, httpStatus: status, legacy: true,
      terminal: { status: "failed", code: "request_rejected", reason: "runtime_failure", partial_output: false },
    });
  });

  it("distinguishes an invalid environment name from an encryption failure", () => {
    expect(parseCoreChatStreamError({ code: 2003122, message: "Reserved environment name",
      data: { detail: { reason: "user_env_invalid_name", name: "NODE_TLS_REJECT_UNAUTHORIZED" } },
    }, 409)).toEqual({
      appCode: 2003122, httpStatus: 409, legacy: true,
      terminal: { status: "failed", code: "user_env_invalid_name", reason: "runtime_failure", partial_output: false },
    });
  });

  it.each([
    { code: 2000000, message: "Internal error", data: { detail: { reason: "user_env_unavailable" } } },
    { code: 2000000, message: "Unable to access user environment variables; check the credential key configuration" },
  ])("classifies unavailable environment credentials without exposing diagnostics", (payload) => {
    expect(parseCoreChatStreamError(payload, 500)).toEqual({
      appCode: 2000000, httpStatus: 500, legacy: true,
      terminal: { status: "failed", code: "user_env_unavailable", reason: "runtime_failure", partial_output: false },
    });
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
