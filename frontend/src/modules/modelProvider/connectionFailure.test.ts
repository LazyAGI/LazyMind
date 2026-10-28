import { describe, expect, it, vi } from "vitest";
import { getProviderConnectionFailureMessage } from "./connectionFailure";

vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: () => "localized-fallback" }));
vi.mock("./api", () => ({ modelProvidersApi: {}, unwrapModelProviderData: (data: unknown) => data }));
const t = (key: string) => key;

describe("provider connection failure messages", () => {
  it.each(["authentication_failed", "permission_denied", "model_not_found", "quota_exceeded", "rate_limited", "timeout"])("shows the safe %s reason for checks and discovery", (reason) => {
    const expected = "modelProvider.probe.reason." + reason;
    expect(getProviderConnectionFailureMessage({ success: false, reason, message: "private-secret" }, t)).toBe(expected);
    expect(getProviderConnectionFailureMessage({ response: { status: 502, data: { data: { reason }, message: "private-secret" } } }, t)).toBe(expected);
  });

  it("maps HTTP and transport failures without showing upstream text", () => {
    expect(getProviderConnectionFailureMessage({ response: { status: 429, data: "secret-body" } }, t)).toBe("modelProvider.probe.reason.rate_limited");
    expect(getProviderConnectionFailureMessage({ code: "ECONNABORTED", message: "secret-url" }, t)).toBe("modelProvider.probe.reason.timeout");
    expect(getProviderConnectionFailureMessage({ reason: "secret-unknown-code", message: "secret-body" }, t, "safe-fallback")).toBe("safe-fallback");
  });

  it("explains a missing model-list endpoint without calling it a missing inference model", () => {
    expect(getProviderConnectionFailureMessage({ success: false, reason: "model_list_unavailable" }, t)).toBe("modelProvider.connection.reason.model_list_unavailable");
  });

  it("unwraps safe check failure metadata nested by the standard Core error envelope", () => {
    expect(getProviderConnectionFailureMessage({ response: { status: 502, data: { data: { data: { success: false, reason: "timeout" }, detail: { message: "private-endpoint" } } } } }, t)).toBe("modelProvider.probe.reason.timeout");
  });
});
