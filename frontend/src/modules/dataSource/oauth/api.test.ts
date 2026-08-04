import { beforeEach, describe, expect, it, vi } from "vitest";

const authorizeMock = vi.fn();
const callbackMock = vi.fn();
const patchConnectionMock = vi.fn();

vi.mock("../api/clients", () => ({
  dataSourceCloudOauthApi: {
    oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost: (...args: unknown[]) =>
      authorizeMock(...args),
    oauthCallbackApiAuthserviceV1CloudProviderOauthCallbackPost: (...args: unknown[]) =>
      callbackMock(...args),
    patchConnectionApiAuthserviceV1CloudConnectionsConnectionIdPatch: (...args: unknown[]) =>
      patchConnectionMock(...args),
  },
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => `localized:${JSON.stringify(error)}`,
}));

vi.mock("@/i18n", () => ({
  default: { t: (key: string) => key },
}));

const savePendingMock = vi.fn();
const loadPendingMock = vi.fn();
const clearPendingMock = vi.fn();

vi.mock("./storage", () => ({
  savePendingCloudOAuthSession: (...args: unknown[]) => savePendingMock(...args),
  loadPendingCloudOAuthSession: (...args: unknown[]) => loadPendingMock(...args),
  clearPendingCloudOAuthSession: (...args: unknown[]) => clearPendingMock(...args),
}));

vi.mock("./urls", () => ({
  getCloudDataSourceCallbackUrl: (provider: string) => `https://app.example.com/oauth/${provider}/callback`,
  normalizeSameOriginReturnUrl: (value?: string) => value || "https://app.example.com/",
}));

import {
  enableCloudConnectionForChat,
  finishCloudDataSourceOAuth,
  finishFeishuDataSourceOAuth,
  requestCloudDataSourceAuthorizeUrl,
  requestFeishuDataSourceAuthorizeUrl,
} from "./api";

describe("requestCloudDataSourceAuthorizeUrl", () => {
  beforeEach(() => {
    authorizeMock.mockReset();
    savePendingMock.mockReset();
  });

  it("requests an authorize url and persists the pending session on success", async () => {
    authorizeMock.mockResolvedValue({
      data: { authorize_url: "https://feishu.example.com/authorize", connection_id: "conn-1", state: "state-1" },
    });

    const url = await requestCloudDataSourceAuthorizeUrl("feishu", {
      tenantId: "tenant-1",
      appId: "app-id",
      appSecret: "app-secret",
      scopes: ["a", "b"],
    });

    expect(url).toBe("https://feishu.example.com/authorize");
    expect(authorizeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: "feishu",
        cloudOAuthAuthorizeURLBody: expect.objectContaining({
          tenant_id: "tenant-1",
          scope: "a b",
          client_id: "app-id",
          client_secret: "app-secret",
        }),
      }),
    );
    expect(savePendingMock).toHaveBeenCalledWith(
      "feishu",
      expect.objectContaining({ tenantId: "tenant-1", connectionId: "conn-1", state: "state-1" }),
    );
  });

  it("uses the reauthorize connection id instead of tenant/scope when provided", async () => {
    authorizeMock.mockResolvedValue({
      data: { authorize_url: "https://x.example.com/authorize", connection_id: "conn-2", state: "state-2" },
    });

    await requestCloudDataSourceAuthorizeUrl("notion", {
      tenantId: "tenant-1",
      scopes: ["a"],
      reauthorizeConnectionId: "conn-existing",
    });

    expect(authorizeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        cloudOAuthAuthorizeURLBody: expect.objectContaining({
          reauthorize_connection_id: "conn-existing",
        }),
      }),
    );
  });

  it("throws a localized error when the response is missing an authorize url", async () => {
    authorizeMock.mockResolvedValue({ data: { connection_id: "conn-1", state: "state-1" } });

    await expect(
      requestCloudDataSourceAuthorizeUrl("feishu", { tenantId: "t", scopes: [] }),
    ).rejects.toThrow();
    expect(savePendingMock).not.toHaveBeenCalled();
  });

  it("throws when the response carries a business error code", async () => {
    authorizeMock.mockResolvedValue({
      data: { code: "1001", authorize_url: "https://x", connection_id: "c", state: "s" },
    });

    await expect(
      requestCloudDataSourceAuthorizeUrl("feishu", { tenantId: "t", scopes: [] }),
    ).rejects.toThrow();
  });

  it("delegates requestFeishuDataSourceAuthorizeUrl to the feishu provider", async () => {
    authorizeMock.mockResolvedValue({
      data: { authorize_url: "https://feishu.example.com/authorize", connection_id: "conn-1", state: "state-1" },
    });
    await requestFeishuDataSourceAuthorizeUrl({ tenantId: "t", scopes: [] });
    expect(authorizeMock).toHaveBeenCalledWith(expect.objectContaining({ provider: "feishu" }));
  });
});

describe("finishCloudDataSourceOAuth", () => {
  beforeEach(() => {
    callbackMock.mockReset();
    loadPendingMock.mockReset();
    clearPendingMock.mockReset();
  });

  it("throws when there is no pending session for the given state", async () => {
    loadPendingMock.mockReturnValue(null);
    await expect(finishCloudDataSourceOAuth("feishu", "code", "state")).rejects.toThrow();
  });

  it("throws on state mismatch between pending session and callback", async () => {
    loadPendingMock.mockReturnValue({
      connectionId: "conn-1",
      redirectUri: "https://cb",
      tenantId: "t",
      state: "expected-state",
    });
    await expect(finishCloudDataSourceOAuth("feishu", "code", "different-state")).rejects.toThrow();
  });

  it("completes the callback, clears the pending session and normalizes the connection", async () => {
    loadPendingMock.mockReturnValue({
      connectionId: "conn-1",
      redirectUri: "https://cb",
      tenantId: "t",
      state: "state-1",
    });
    callbackMock.mockResolvedValue({
      data: { connection: { connection_id: "conn-1", status: "connected", account_name: "acct" } },
    });

    const result = await finishCloudDataSourceOAuth("feishu", "code", "state-1");
    expect(callbackMock).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: "feishu",
        cloudOAuthCallbackBody: expect.objectContaining({
          tenant_id: "t",
          connection_id: "conn-1",
          code: "code",
          state: "state-1",
        }),
      }),
    );
    expect(clearPendingMock).toHaveBeenCalledWith("feishu", "state-1");
    expect(result.status).toBe("connected");
    expect(result.provider).toBe("feishu");
  });

  it("throws a localized error when the callback response has a business error", async () => {
    loadPendingMock.mockReturnValue({
      connectionId: "conn-1",
      redirectUri: "https://cb",
      tenantId: "t",
      state: "state-1",
    });
    callbackMock.mockResolvedValue({ data: { code: "1001" } });

    await expect(finishCloudDataSourceOAuth("feishu", "code", "state-1")).rejects.toThrow();
    expect(clearPendingMock).not.toHaveBeenCalled();
  });

  it("delegates finishFeishuDataSourceOAuth to the feishu provider", async () => {
    loadPendingMock.mockReturnValue({
      connectionId: "conn-1",
      redirectUri: "https://cb",
      tenantId: "t",
      state: "state-1",
    });
    callbackMock.mockResolvedValue({ data: { connection: { status: "connected" } } });

    await finishFeishuDataSourceOAuth("code", "state-1");
    expect(callbackMock).toHaveBeenCalledWith(expect.objectContaining({ provider: "feishu" }));
  });
});

describe("enableCloudConnectionForChat", () => {
  beforeEach(() => {
    patchConnectionMock.mockReset();
  });

  it("patches the connection to enable chat and returns the unwrapped payload", async () => {
    patchConnectionMock.mockResolvedValue({ data: { data: { chat_enabled: true } } });
    const result = await enableCloudConnectionForChat("conn-1");
    expect(patchConnectionMock).toHaveBeenCalledWith(
      expect.objectContaining({
        connectionId: "conn-1",
        cloudConnectionUpdateBody: { chat_enabled: true, chatEnabled: true },
      }),
    );
    expect(result).toEqual({ chat_enabled: true });
  });

  it("throws a localized error when the response has a business error code", async () => {
    patchConnectionMock.mockResolvedValue({ data: { code: "1001" } });
    await expect(enableCloudConnectionForChat("conn-1")).rejects.toThrow();
  });
});
