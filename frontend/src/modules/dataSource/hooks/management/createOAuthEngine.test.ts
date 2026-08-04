import { act } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

const listConnectionsMock = vi.fn();
const saveOauthAppCredentialsMock = vi.fn();

vi.mock("../../api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: (...args: unknown[]) =>
      listConnectionsMock(...args),
    saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut: (
      ...args: unknown[]
    ) => saveOauthAppCredentialsMock(...args),
  },
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => `localized:${(error as Error)?.message || error}`,
  localizeErrorCode: (code?: string) => `localized-code:${code}`,
}));

const requestFeishuDataSourceAuthorizeUrlMock = vi.fn();
const requestCloudDataSourceAuthorizeUrlMock = vi.fn();
const openCenteredPopupMock = vi.fn();

vi.mock("@/modules/dataSource/common/feishuOAuth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/modules/dataSource/common/feishuOAuth")>();
  return {
    ...actual,
    requestFeishuDataSourceAuthorizeUrl: (...args: unknown[]) =>
      requestFeishuDataSourceAuthorizeUrlMock(...args),
    requestCloudDataSourceAuthorizeUrl: (...args: unknown[]) =>
      requestCloudDataSourceAuthorizeUrlMock(...args),
    openCenteredPopup: (...args: unknown[]) => openCenteredPopupMock(...args),
  };
});

import { createOAuthEngine } from "./createOAuthEngine";
import type { ManagementContext } from "./context";

const t = ((key: string) => key) as TFunction;

function makeContext(overrides: Partial<ManagementContext> = {}): ManagementContext {
  const base = {
    t,
    form: { getFieldsValue: vi.fn(() => ({})) },
    oauthAttemptRef: { current: null },
    setOauthState: vi.fn(),
    setConnectionVerified: vi.fn(),
    setOauthConnection: vi.fn(),
    setNotionOauthConnection: vi.fn(),
    setNotionAuthAccounts: vi.fn(),
    setFeishuAuthAccounts: vi.fn(),
    setWizardStep: vi.fn(),
    setValidatedAgentId: vi.fn(),
    setAuthSelectModalOpen: vi.fn(),
    setAuthSelectProvider: vi.fn(),
    feishuAuthAccountsLoadedRef: { current: false },
    scanAgents: [],
    editingFeishuAccountId: null,
    feishuAuthAccounts: [],
    notionAuthAccounts: [],
    selectedType: "feishu",
    validatedAgentId: null,
    feishuAppSetup: null,
    notionAppSetup: null,
    oauthState: "pending",
    connectionVerified: false,
    oauthConnection: null,
    wizardStep: 0,
    wizardMode: "create",
    editingId: null,
    openCloudSetupModal: vi.fn(),
    setWizardOpen: vi.fn(),
    ...overrides,
  };
  return base as unknown as ManagementContext;
}

describe("createOAuthEngine", () => {
  beforeEach(() => {
    listConnectionsMock.mockReset();
    saveOauthAppCredentialsMock.mockReset();
    requestFeishuDataSourceAuthorizeUrlMock.mockReset();
    requestCloudDataSourceAuthorizeUrlMock.mockReset();
    openCenteredPopupMock.mockReset();
    localStorage.clear();
  });

  it("refreshes feishu auth accounts and marks a connected account as the active connection", async () => {
    listConnectionsMock.mockResolvedValue({
      data: {
        items: [
          {
            connection_id: "conn-1",
            provider: "feishu",
            status: "connected",
            display_name: "My Feishu",
          },
        ],
      },
    });

    const setFeishuAuthAccounts = vi.fn();
    const setOauthConnection = vi.fn();
    const setOauthState = vi.fn();
    const setConnectionVerified = vi.fn();
    const ctx = makeContext({
      setFeishuAuthAccounts,
      setOauthConnection,
      setOauthState,
      setConnectionVerified,
    });
    const { refreshFeishuAuthAccounts } = createOAuthEngine(ctx);

    await refreshFeishuAuthAccounts();

    expect(setFeishuAuthAccounts).toHaveBeenCalledWith([
      expect.objectContaining({ id: "conn-1", status: "connected" }),
    ]);
    expect(setOauthConnection).toHaveBeenCalledWith(
      expect.objectContaining({ connectionId: "conn-1" }),
    );
    expect(setOauthState).toHaveBeenCalledWith("connected");
    expect(setConnectionVerified).toHaveBeenCalledWith(true);
  });

  it("logs and does not throw when refreshing feishu accounts fails", async () => {
    listConnectionsMock.mockRejectedValue(new Error("network down"));
    const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    const ctx = makeContext();
    const { refreshFeishuAuthAccounts } = createOAuthEngine(ctx);

    await expect(refreshFeishuAuthAccounts()).resolves.toBeUndefined();
    expect(consoleErrorSpy).toHaveBeenCalled();
    consoleErrorSpy.mockRestore();
  });

  it("creates a new feishu auth account with pending status via upsertFeishuAuthAccount", () => {
    const setFeishuAuthAccounts = vi.fn();
    const ctx = makeContext({ setFeishuAuthAccounts, feishuAuthAccounts: [] });
    const { upsertFeishuAuthAccount } = createOAuthEngine(ctx);

    const account = upsertFeishuAuthAccount({ name: "New App", appId: "app-1", appSecret: "secret-1" });

    expect(account.appId).toBe("app-1");
    expect(account.status).toBe("pending");
    expect(setFeishuAuthAccounts).toHaveBeenCalledWith([
      expect.objectContaining({ appId: "app-1" }),
    ]);
  });

  it("updates an existing feishu account in place when editingFeishuAccountId matches", () => {
    const existing = {
      id: "acc-1",
      name: "Old",
      appId: "app-1",
      appSecret: "old-secret",
      chatEnabled: false,
      status: "pending" as const,
      connection: null,
      createdAt: "2024-01-01T00:00:00.000Z",
    };
    const setFeishuAuthAccounts = vi.fn();
    const ctx = makeContext({
      setFeishuAuthAccounts,
      feishuAuthAccounts: [existing],
      editingFeishuAccountId: "acc-1",
    });
    const { upsertFeishuAuthAccount } = createOAuthEngine(ctx);

    const account = upsertFeishuAuthAccount(
      { name: "Renamed", appId: "app-1", appSecret: "new-secret" },
      "connected",
    );

    expect(account.id).toBe("acc-1");
    expect(account.name).toBe("Renamed");
    expect(account.status).toBe("connected");
  });

  it("saves cloud app credentials for the given provider", async () => {
    saveOauthAppCredentialsMock.mockResolvedValue({ data: {} });
    const ctx = makeContext();
    const { saveCloudAppCredentials } = createOAuthEngine(ctx);

    await saveCloudAppCredentials("feishu", { appId: "app-1", appSecret: "secret-1" });

    expect(saveOauthAppCredentialsMock).toHaveBeenCalledWith({
      provider: "feishu",
      cloudOAuthAppCredentialBody: { client_id: "app-1", client_secret: "secret-1" },
    });
  });

  it("warns and does not call the authorize endpoint when app credentials are missing", async () => {
    const messageModule = await import("antd");
    const warnSpy = vi.spyOn(messageModule.message, "warning").mockImplementation(() => "" as any);

    const ctx = makeContext({ feishuAppSetup: null });
    const { startCloudOAuth } = createOAuthEngine(ctx);

    let result: boolean | undefined;
    await act(async () => {
      result = await startCloudOAuth("feishu");
    });

    expect(result).toBe(false);
    expect(warnSpy).toHaveBeenCalledWith("admin.dataSourceFeishuCredentialRequired");
    expect(requestFeishuDataSourceAuthorizeUrlMock).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });

  it("opens a popup and marks oauth state as waiting when credentials are present", async () => {
    requestFeishuDataSourceAuthorizeUrlMock.mockResolvedValue("https://feishu.example.com/authorize");
    openCenteredPopupMock.mockReturnValue({ closed: false, focus: vi.fn() });

    const setOauthState = vi.fn();
    const ctx = makeContext({
      feishuAppSetup: { appId: "app-1", appSecret: "secret-1" },
      setOauthState,
    });
    const { startCloudOAuth } = createOAuthEngine(ctx);

    let result: boolean | undefined;
    await act(async () => {
      result = await startCloudOAuth("feishu", { setup: { appId: "app-1", appSecret: "secret-1" } });
    });

    expect(result).toBe(true);
    expect(setOauthState).toHaveBeenCalledWith("waiting");
    expect(openCenteredPopupMock).toHaveBeenCalled();
  });

  it("restores the previous oauth state and shows a message when there is a pending attempt", () => {
    const setOauthState = vi.fn();
    const setConnectionVerified = vi.fn();
    const setOauthConnection = vi.fn();
    const ctx = makeContext({
      setOauthState,
      setConnectionVerified,
      setOauthConnection,
      oauthAttemptRef: {
        current: {
          timerId: null,
          previousState: "pending",
          previousVerified: false,
          previousConnection: null,
          resolved: false,
        },
      },
    });
    const { restorePreviousOauthState } = createOAuthEngine(ctx);

    act(() => {
      restorePreviousOauthState("closed by user", "warning");
    });

    expect(setOauthState).toHaveBeenCalledWith("pending");
    expect(setConnectionVerified).toHaveBeenCalledWith(false);
    expect(setOauthConnection).toHaveBeenCalledWith(null);
  });

  it("does nothing when restorePreviousOauthState is called with no pending attempt", () => {
    const setOauthState = vi.fn();
    const ctx = makeContext({ setOauthState, oauthAttemptRef: { current: null } });
    const { restorePreviousOauthState } = createOAuthEngine(ctx);

    restorePreviousOauthState();

    expect(setOauthState).not.toHaveBeenCalled();
  });

  it("applies a successful oauth result and updates connection state", () => {
    const setOauthConnection = vi.fn();
    const setOauthState = vi.fn();
    const setConnectionVerified = vi.fn();
    const setWizardStep = vi.fn();
    const ctx = makeContext({
      setOauthConnection,
      setOauthState,
      setConnectionVerified,
      setWizardStep,
    });
    const { applyOauthResult } = createOAuthEngine(ctx);

    applyOauthResult({
      channel: "lazymind:datasource:feishu-oauth",
      source: "feishu-data-source",
      status: "success",
      connection: {
        provider: "feishu",
        connectionId: "conn-1",
        status: "connected",
        accountName: "My Feishu",
      },
    });

    expect(setOauthConnection).toHaveBeenCalledWith(
      expect.objectContaining({ connectionId: "conn-1" }),
    );
    expect(setOauthState).toHaveBeenCalledWith("connected");
    expect(setConnectionVerified).toHaveBeenCalledWith(true);
    expect(setWizardStep).toHaveBeenCalledWith(1);
  });

  it("applies an error oauth result and resets the connection", () => {
    const setOauthConnection = vi.fn();
    const setOauthState = vi.fn();
    const setConnectionVerified = vi.fn();
    const ctx = makeContext({ setOauthConnection, setOauthState, setConnectionVerified });
    const { applyOauthResult } = createOAuthEngine(ctx);

    applyOauthResult({
      channel: "lazymind:datasource:feishu-oauth",
      source: "feishu-data-source",
      status: "error",
      provider: "feishu",
      message: "denied",
    });

    expect(setOauthConnection).toHaveBeenCalledWith(null);
    expect(setOauthState).toHaveBeenCalledWith("error");
    expect(setConnectionVerified).toHaveBeenCalledWith(false);
  });

  it("ignores oauth messages from a different channel", () => {
    const setOauthConnection = vi.fn();
    const ctx = makeContext({ setOauthConnection });
    const { applyOauthResult } = createOAuthEngine(ctx);

    applyOauthResult({
      channel: "some-other-channel" as never,
      source: "feishu-data-source",
      status: "success",
      connection: { provider: "feishu", connectionId: "conn-1", status: "connected" },
    } as never);

    expect(setOauthConnection).not.toHaveBeenCalled();
  });
});
