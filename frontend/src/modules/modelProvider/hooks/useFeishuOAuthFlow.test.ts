import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { useFeishuOAuthFlow } from "./useFeishuOAuthFlow";

vi.mock("antd", () => ({
  message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: () => "localized-error",
  localizeErrorCode: (code?: string) => `localized:${code}`,
}));

const dataSourceMocks = vi.hoisted(() => ({
  requestFeishuDataSourceAuthorizeUrl: vi.fn(),
  finishFeishuDataSourceOAuth: vi.fn(),
  consumeFeishuDataSourceOAuthResult: vi.fn(() => null),
  openCenteredPopup: vi.fn(),
}));

vi.mock("@/modules/dataSource/common/feishuOAuth", () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: "feishu-oauth",
  consumeFeishuDataSourceOAuthResult: dataSourceMocks.consumeFeishuDataSourceOAuthResult,
  finishFeishuDataSourceOAuth: dataSourceMocks.finishFeishuDataSourceOAuth,
  openCenteredPopup: dataSourceMocks.openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl: dataSourceMocks.requestFeishuDataSourceAuthorizeUrl,
}));

vi.mock("@/modules/dataSource/common/feishuAccounts", () => ({
  getOAuthStateFromConnection: (connection?: { status?: string } | null) =>
    connection?.status === "connected" ? "connected" : "pending",
}));

vi.mock("@/modules/dataSource/constants/options", () => ({
  FEISHU_DEFAULT_SCOPES: ["scope1"],
}));

vi.mock("@/modules/dataSource/utils/scanAccessors", () => ({
  getScanTenantId: () => "tenant-1",
}));

vi.mock("@/modules/dataSource/utils/feishuAccount", () => ({
  parseFeishuOAuthCallbackInput: (value: string) => {
    if (!value.includes("code=")) return null;
    return { code: "code-1", state: "state-1" };
  },
}));

const buildAccount = (overrides: Partial<Record<string, unknown>> = {}) => ({
  id: "acc-1",
  name: "Account 1",
  appId: "app-1",
  appSecret: "secret-1",
  chatEnabled: false,
  status: "pending" as const,
  connection: null,
  createdAt: "2024-01-01T00:00:00Z",
  ...overrides,
});

const buildParams = (initialAccounts: ReturnType<typeof buildAccount>[] = []) => {
  let accounts = initialAccounts;
  const setAccounts = vi.fn((updater: unknown) => {
    accounts =
      typeof updater === "function"
        ? (updater as (current: typeof accounts) => typeof accounts)(accounts)
        : (updater as typeof accounts);
  });
  const refreshAccounts = vi.fn();
  return {
    t: ((key: string) => key) as never,
    setAccounts: setAccounts as never,
    refreshAccounts,
    getAccounts: () => accounts,
  };
};

beforeEach(() => {
  vi.clearAllMocks();
  dataSourceMocks.consumeFeishuDataSourceOAuthResult.mockReturnValue(null);
});

describe("startFeishuOAuth", () => {
  it("opens a popup with the authorize url when the popup is allowed", async () => {
    const account = buildAccount();
    const params = buildParams([account]);
    dataSourceMocks.requestFeishuDataSourceAuthorizeUrl.mockResolvedValue("http://auth-url");
    dataSourceMocks.openCenteredPopup.mockReturnValue({ closed: false, focus: vi.fn() });

    const { result } = renderHook(() => useFeishuOAuthFlow(params));

    let success: boolean | undefined;
    await act(async () => {
      success = await result.current.startFeishuOAuth(account);
    });

    expect(success).toBe(true);
    expect(dataSourceMocks.requestFeishuDataSourceAuthorizeUrl).toHaveBeenCalledWith(
      expect.objectContaining({ tenantId: "tenant-1", appId: "app-1", appSecret: "secret-1" }),
    );
    expect(dataSourceMocks.openCenteredPopup).toHaveBeenCalled();
  });

  it("navigates directly with location.assign when reauthorizing an existing connection", async () => {
    const account = buildAccount({ connection: { connectionId: "conn-1" } });
    const params = buildParams([account]);
    dataSourceMocks.requestFeishuDataSourceAuthorizeUrl.mockResolvedValue("http://reauth-url");
    const assignSpy = vi.fn();
    const originalLocation = window.location;
    // jsdom's window.location.assign is non-configurable; swap the whole location object.
    vi.stubGlobal("location", { ...originalLocation, assign: assignSpy });

    const { result } = renderHook(() => useFeishuOAuthFlow(params));
    await act(async () => {
      await result.current.startFeishuOAuth(account, { reauthorizeConnectionId: "conn-1" });
    });

    expect(assignSpy).toHaveBeenCalledWith("http://reauth-url");
    vi.stubGlobal("location", originalLocation);
  });

  it("returns false without applying a partial state update when the authorize request fails before an attempt is recorded", async () => {
    const account = buildAccount();
    const params = buildParams([account]);
    dataSourceMocks.requestFeishuDataSourceAuthorizeUrl.mockRejectedValue(new Error("boom"));

    const { result } = renderHook(() => useFeishuOAuthFlow(params));
    let success: boolean | undefined;
    await act(async () => {
      success = await result.current.startFeishuOAuth(account);
    });

    expect(success).toBe(false);
    // The pending attempt ref is only populated after the authorize URL request
    // resolves, so a failure here has nothing to restore and no message is shown.
    expect(message.error).not.toHaveBeenCalled();
    expect(message.warning).not.toHaveBeenCalled();
  });
});

describe("applyOauthResult", () => {
  it("ignores payloads from a different channel", () => {
    const params = buildParams([buildAccount({ status: "waiting" })]);
    const { result } = renderHook(() => useFeishuOAuthFlow(params));

    act(() => {
      result.current.applyOauthResult({
        channel: "other-channel",
        source: "feishu-data-source",
        status: "success",
        connection: { connectionId: "conn-1", status: "connected" },
      } as never);
    });

    expect(params.setAccounts).not.toHaveBeenCalled();
  });

  it("updates the matched account to connected and shows a success message", () => {
    const account = buildAccount({ status: "waiting" });
    const params = buildParams([account]);
    const { result } = renderHook(() => useFeishuOAuthFlow(params));

    act(() => {
      result.current.applyOauthResult({
        channel: "feishu-oauth",
        source: "feishu-data-source",
        status: "success",
        connection: { connectionId: "conn-1", status: "connected" },
      } as never);
    });

    expect(message.success).toHaveBeenCalledWith("admin.dataSourceOauthSuccess");
    expect(params.getAccounts()[0]).toEqual(
      expect.objectContaining({ status: "connected", connection: expect.objectContaining({ connectionId: "conn-1" }) }),
    );
  });

  it("marks the account as errored on failure without a previous connection", () => {
    const account = buildAccount({ status: "waiting" });
    const params = buildParams([account]);
    const { result } = renderHook(() => useFeishuOAuthFlow(params));

    act(() => {
      result.current.applyOauthResult({
        channel: "feishu-oauth",
        source: "feishu-data-source",
        status: "error",
        message: "failed",
      } as never);
    });

    expect(message.error).toHaveBeenCalledWith("localized:2000509");
    expect(params.getAccounts()[0]).toEqual(expect.objectContaining({ status: "error", connection: null }));
  });
});

describe("handleSubmitManualOauthCallback", () => {
  it("warns when the callback value cannot be parsed", async () => {
    const params = buildParams([]);
    const { result } = renderHook(() => useFeishuOAuthFlow(params));

    await act(async () => {
      await result.current.handleSubmitManualOauthCallback();
    });

    expect(message.warning).toHaveBeenCalledWith("admin.dataSourceOauthManualCallbackInvalid");
    expect(dataSourceMocks.finishFeishuDataSourceOAuth).not.toHaveBeenCalled();
  });

  it("applies a success result and closes the modal when parsing and exchange succeed", async () => {
    const params = buildParams([]);
    dataSourceMocks.finishFeishuDataSourceOAuth.mockResolvedValue({
      connectionId: "conn-2",
      status: "connected",
    });

    const { result } = renderHook(() => useFeishuOAuthFlow(params));
    act(() => {
      result.current.setManualOauthCallbackValue("http://cb?code=abc&state=xyz");
    });

    await act(async () => {
      await result.current.handleSubmitManualOauthCallback();
    });

    expect(dataSourceMocks.finishFeishuDataSourceOAuth).toHaveBeenCalledWith("code-1", "state-1");
    expect(result.current.manualOauthModalOpen).toBe(false);
    expect(result.current.manualOauthCallbackValue).toBe("");
  });
});
