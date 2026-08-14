import { describe, expect, it } from "vitest";

import {
  formatProviderDetail,
  nextProviderEventState,
  shouldShowProviderDiagnostic,
} from "./providerStatus";

describe("provider status state", () => {
  it("keeps the raw provider reason separate from the lifecycle finish reason", () => {
    const next = nextProviderEventState(
      {},
      {
        provider_status: {
          model_call_id: "call-1",
          http_status: 200,
          finish_reason: "tool_calls",
        },
        finish_reason: "FINISH_REASON_UNSPECIFIED",
      },
    );

    expect(next.provider_status?.finish_reason).toBe("tool_calls");
    expect((next as Record<string, unknown>).finish_reason).toBeUndefined();
    expect(shouldShowProviderDiagnostic(next.provider_status)).toBe(false);
  });

  it("clears retry state when new content or a final lifecycle event arrives", () => {
    const previous = {
      model_retry: {
        model_call_id: "call-1",
        retry_index: 2,
        max_retries: 5,
        delay_ms: 2000,
      },
    };

    expect(
      nextProviderEventState(previous, { delta: "partial" }).model_retry,
    ).toBeUndefined();
    expect(
      nextProviderEventState(previous, {
        finish_reason: "FINISH_REASON_STOP",
      }).model_retry,
    ).toBeUndefined();
  });

  it("clears retry state when a final provider event arrives", () => {
    const previous = {
      model_retry: {
        model_call_id: "call-1",
        retry_index: 1,
        max_retries: 5,
        delay_ms: 1000,
      },
    };

    expect(
      nextProviderEventState(previous, {
        provider_status: {
          model_call_id: "call-1",
          http_status: 429,
          finish_reason: null,
        },
      }).model_retry,
    ).toBeUndefined();
    expect(
      nextProviderEventState(previous, {
        model_transport_error: {
          model_call_id: "call-1",
          http_status: null,
          finish_reason: null,
          error_type: "ReadTimeout",
        },
      }).model_retry,
    ).toBeUndefined();
  });

  it("shows custom and error reasons without normalizing them", () => {
    expect(
      shouldShowProviderDiagnostic({
        model_call_id: "call-1",
        http_status: 200,
        finish_reason: "VendorLimit",
      }),
    ).toBe(true);
    expect(
      shouldShowProviderDiagnostic({
        model_call_id: "call-1",
        http_status: 200,
        finish_reason: "stop",
      }),
    ).toBe(false);
  });

  it("pretty prints JSON and leaves plain text untouched", () => {
    expect(formatProviderDetail('{"error":{"code":429}}')).toContain(
      '\n  "error"',
    );
    expect(formatProviderDetail("plain upstream error")).toBe(
      "plain upstream error",
    );
    expect(formatProviderDetail()).toContain("原始响应仅在错误发生时可用");
  });
});
