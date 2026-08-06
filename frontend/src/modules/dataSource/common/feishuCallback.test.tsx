import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen, waitFor } from "@/test/testUtils";
import FeishuDataSourceCallback from "./feishuCallback";

const finishCloudDataSourceOAuthMock = vi.fn();
const saveCloudDataSourceOAuthResultMock = vi.fn();

vi.mock("./feishuOAuth", () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: "lazymind:datasource:feishu-oauth",
  finishCloudDataSourceOAuth: (...args: unknown[]) => finishCloudDataSourceOAuthMock(...args),
  getDataSourceManagementUrl: () => "https://app.example.com/data-sources",
  getCloudDataSourceOAuthReturnUrl: () => "https://app.example.com/return",
  saveCloudDataSourceOAuthResult: (...args: unknown[]) => saveCloudDataSourceOAuthResultMock(...args),
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => `localized-error:${(error as Error)?.message || error}`,
  localizeErrorCode: (code?: string) => `localized-code:${code}`,
}));

describe("FeishuDataSourceCallback", () => {
  const locationReplaceMock = vi.fn();

  beforeEach(() => {
    finishCloudDataSourceOAuthMock.mockReset();
    saveCloudDataSourceOAuthResultMock.mockReset();
    locationReplaceMock.mockReset();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...window.location, replace: locationReplaceMock },
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows an error result when the provider redirects with an error param", async () => {
    renderWithProviders(<FeishuDataSourceCallback />, {
      route: "/oauth/feishu/callback?error=access_denied",
    });

    await waitFor(() => {
      expect(screen.getByText("localized-code:2000509")).toBeInTheDocument();
    });
    expect(saveCloudDataSourceOAuthResultMock).toHaveBeenCalledWith(
      "feishu",
      expect.objectContaining({ status: "error" }),
    );
    expect(finishCloudDataSourceOAuthMock).not.toHaveBeenCalled();
  });

  it("shows an error result when code or state is missing", async () => {
    renderWithProviders(<FeishuDataSourceCallback />, { route: "/oauth/feishu/callback" });

    await waitFor(() => {
      expect(screen.getByText("localized-code:2000509")).toBeInTheDocument();
    });
    expect(finishCloudDataSourceOAuthMock).not.toHaveBeenCalled();
  });

  it("shows a success result once the oauth callback resolves", async () => {
    finishCloudDataSourceOAuthMock.mockResolvedValue({
      provider: "feishu",
      accountName: "My Feishu Workspace",
      status: "connected",
    });

    renderWithProviders(<FeishuDataSourceCallback />, {
      route: "/oauth/feishu/callback?code=abc&state=state-1",
    });

    await waitFor(() => {
      expect(screen.getByText("admin.dataSourceCallbackSuccessTitle")).toBeInTheDocument();
    });
    expect(finishCloudDataSourceOAuthMock).toHaveBeenCalledWith("feishu", "abc", "state-1");
    expect(saveCloudDataSourceOAuthResultMock).toHaveBeenCalledWith(
      "feishu",
      expect.objectContaining({ status: "success" }),
    );
  });

  it("shows an error result when the oauth callback rejects", async () => {
    finishCloudDataSourceOAuthMock.mockRejectedValue(new Error("callback failed"));

    renderWithProviders(<FeishuDataSourceCallback />, {
      route: "/oauth/feishu/callback?code=abc&state=state-1",
    });

    await waitFor(() => {
      expect(screen.getByText("localized-error:callback failed")).toBeInTheDocument();
    });
    expect(saveCloudDataSourceOAuthResultMock).toHaveBeenCalledWith(
      "feishu",
      expect.objectContaining({ status: "error", message: "localized-error:callback failed" }),
    );
  });
});
