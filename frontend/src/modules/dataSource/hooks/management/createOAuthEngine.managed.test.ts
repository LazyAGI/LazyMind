import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ManagementContext } from "./context";
import { createOAuthEngine, startFeishuCLISession, startManagedOAuthSession } from "./createOAuthEngine";

const mocks = vi.hoisted(() => ({
  getSession: vi.fn(),
  reauthorize: vi.fn(),
  reconcileConnections: vi.fn(),
  createSession: vi.fn(),
  closeManagedAuthorizationPopup: vi.fn(),
  enableCloudConnectionForChat: vi.fn(),
  listConnections: vi.fn(),
  modalConfirm: vi.fn(),
  openFeishuCLIAuthorization: vi.fn(),
  openManagedAuthorization: vi.fn(),
  reserveManagedAuthorizationPopup: vi.fn(),
  requestFeishuAuthorizeUrl: vi.fn(),
  openLegacyPopup: vi.fn(),
}));

vi.mock("antd", () => ({
  Modal: { confirm: mocks.modalConfirm },
  message: { error: vi.fn(), warning: vi.fn(), success: vi.fn() },
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: vi.fn(() => "error"),
  localizeErrorCode: vi.fn(() => "error"),
}));

vi.mock("../../api/clients", () => ({
  dataSourceProviderConnectionsApi: {
    apiCoreProviderConnectionsSessionsPost: mocks.createSession,
    apiCoreProviderConnectionsSessionsSessionIdGet: mocks.getSession,
    apiCoreProviderConnectionsAuthConnectionIdReauthorizePost: mocks.reauthorize,
    apiCoreProviderConnectionsGet: mocks.reconcileConnections,
  },
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.listConnections,
  },
}));

vi.mock("../../common/feishuAccounts", () => ({
  createFeishuAccountId: vi.fn(() => "account"),
  getOAuthStateFromConnection: vi.fn(() => "connected"),
  loadFeishuAuthAccounts: vi.fn(() => []),
  persistFeishuAuthAccounts: vi.fn(),
}));

vi.mock("@/modules/dataSource/common/feishuOAuth", () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: "feishu-oauth",
  clearFeishuDataSourceWizardDraft: vi.fn(),
  consumeCloudDataSourceOAuthResult: vi.fn(),
  consumeFeishuDataSourceOAuthResult: vi.fn(),
  enableCloudConnectionForChat: mocks.enableCloudConnectionForChat,
  peekFeishuDataSourceWizardDraft: vi.fn(() => null),
  requestCloudDataSourceAuthorizeUrl: vi.fn(),
  openCenteredPopup: mocks.openLegacyPopup,
  requestFeishuDataSourceAuthorizeUrl: mocks.requestFeishuAuthorizeUrl,
  saveFeishuDataSourceWizardDraft: vi.fn(),
}));

vi.mock("../../utils/scanAccessors", () => ({
  getScanTenantId: vi.fn(() => "tenant"),
}));

vi.mock("../../utils/cloudSync", () => ({
  pickScanAgent: vi.fn(() => null),
}));

vi.mock("../../mappers/dataSourceConnection", () => ({
  getCloudConnectionItems: vi.fn(() => [{}]),
  mapCloudConnectionToDataSourceConnection: vi.fn(),
  mapCloudConnectionToFeishuAccount: vi.fn(() => ({
    status: "connected",
    connection: { connectionId: "managed-connection" },
  })),
  mapCloudConnectionToNotionAccount: vi.fn(() => ({
    status: "connected",
    connection: { connectionId: "managed-connection" },
  })),
}));

vi.mock("../../oauth/openManagedAuthorization", () => ({
  closeManagedAuthorizationPopup: mocks.closeManagedAuthorizationPopup,
  openFeishuCLIAuthorization: mocks.openFeishuCLIAuthorization,
  openManagedAuthorization: mocks.openManagedAuthorization,
  reserveManagedAuthorizationPopup: mocks.reserveManagedAuthorizationPopup,
}));

vi.mock("../../oauth/legacyOAuthCredentials", () => ({
  buildLegacyOAuthCredentialBody: vi.fn(),
}));

describe("createOAuthEngine managed OAuth", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    mocks.createSession.mockReset().mockResolvedValue({ data: {} });
    mocks.getSession.mockReset();
    mocks.reauthorize.mockReset();
    mocks.reconcileConnections.mockReset();
    mocks.modalConfirm.mockReset();
    mocks.openManagedAuthorization.mockReset().mockResolvedValue({ ok: true });
    mocks.openFeishuCLIAuthorization.mockReset().mockResolvedValue({ ok: true });
    mocks.reserveManagedAuthorizationPopup
      .mockReset()
      .mockReturnValue({ kind: "popup" });
    mocks.listConnections.mockResolvedValue({ data: { items: [{}] } });
    mocks.requestFeishuAuthorizeUrl.mockReset().mockResolvedValue("https://accounts.feishu.cn/fixture");
    mocks.openLegacyPopup.mockReset().mockReturnValue({ closed: false, focus: vi.fn() });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  function legacyContext(available: boolean | undefined, setup?: { appId: string; appSecret: string }) {
    return {
      t: (key: string) => key,
      form: { getFieldsValue: vi.fn(() => ({})) },
      oauthAttemptRef: { current: null },
      setOauthState: vi.fn(), setConnectionVerified: vi.fn(),
      setOauthConnection: vi.fn(), setNotionOauthConnection: vi.fn(),
      setNotionAuthAccounts: vi.fn(), setFeishuAuthAccounts: vi.fn(),
      setWizardStep: vi.fn(), setValidatedAgentId: vi.fn(),
      setAuthSelectModalOpen: vi.fn(), setAuthSelectProvider: vi.fn(),
      openCloudSetupModal: vi.fn(),
      feishuAuthAccountsLoadedRef: { current: false },
      scanAgents: [], notionAuthAccounts: [], selectedType: "feishu",
      oauthState: "pending", connectionVerified: false, oauthConnection: null,
      cloudManagedOAuthAvailable: available, feishuAppSetup: setup,
    } as unknown as ManagementContext;
  }

  it.each([false, undefined])("uses saved BYO credentials without starting CLI when Cloud availability is %s", async (available) => {
    const setup = { appId: "cli_fixture_byo", appSecret: "fixture-secret-not-real" };
    const ctx = legacyContext(available, setup);
    const started = await createOAuthEngine(ctx).startCloudOAuth("feishu");
    expect(mocks.createSession).not.toHaveBeenCalled();
    expect(started).toBe(true);
    expect(mocks.requestFeishuAuthorizeUrl).toHaveBeenCalledWith(expect.objectContaining({
      appId: setup.appId, appSecret: setup.appSecret,
      scopes: expect.arrayContaining(["drive:drive", "wiki:wiki", "docx:document", "offline_access"]),
    }));
    expect(mocks.reauthorize).not.toHaveBeenCalled();
    expect(mocks.openLegacyPopup).toHaveBeenCalledOnce();
  });

  it.each([false, undefined])("opens BYO setup without starting CLI when credentials are absent and Cloud availability is %s", async (available) => {
    const ctx = legacyContext(available);
    expect(await createOAuthEngine(ctx).startCloudOAuth("feishu")).toBe(false);
    expect(ctx.openCloudSetupModal).toHaveBeenCalledWith("feishu", "create");
    expect(mocks.createSession).not.toHaveBeenCalled();
    expect(mocks.requestFeishuAuthorizeUrl).not.toHaveBeenCalled();
  });

  it.each([false, true])("keeps explicit BYO authorization independent of Cloud availability (%s)", async (available) => {
    const setup = { appId: "cli_fixture_byo", appSecret: "fixture-secret-not-real" };
    expect(await createOAuthEngine(legacyContext(available)).startCloudOAuth("feishu", { setup })).toBe(true);
    expect(mocks.requestFeishuAuthorizeUrl).toHaveBeenCalledWith(expect.objectContaining({
      appId: setup.appId,
      scopes: expect.arrayContaining(["drive:drive", "wiki:wiki", "docx:document"]),
    }));
    expect(mocks.createSession).not.toHaveBeenCalled();
  });

  it("keeps the CLI entry available when Cloud is explicitly available", async () => {
    mocks.createSession.mockResolvedValue({ data: { session_id: "fixture-cli", authorization_start_url: "https://accounts.feishu.cn/fixture" } });
    mocks.getSession.mockResolvedValue({ data: { status: "COMPLETED", auth_connection_id: "fixture-connection" } });
    const pending = createOAuthEngine(legacyContext(true)).startCloudOAuth("feishu");
    await vi.advanceTimersByTimeAsync(1000);
    expect(await pending).toBe(true);
    expect(mocks.createSession).toHaveBeenCalledWith({ providerConnectionCreateRequest: { provider: "feishu" } });
    expect(mocks.requestFeishuAuthorizeUrl).not.toHaveBeenCalled();
  });

  it("enables the completed Notion connection for Chat before reporting success", async () => {
    mocks.createSession.mockResolvedValueOnce({
      data: {
          session_id: "managed-session",
          authorization_start_url:
            "https://localhost:8443/v1/provider-connections/authorize/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      },
    });
    mocks.getSession.mockResolvedValueOnce({
      data: {
          status: "COMPLETED",
          auth_connection_id: "managed-connection",
      },
    });
    const fetchMock = vi.fn().mockResolvedValue({ ok: false });
    vi.stubGlobal("fetch", fetchMock);

    const context = {
      t: (key: string) => key,
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
      notionAuthAccounts: [],
      selectedType: "notion",
      oauthState: "idle",
      connectionVerified: false,
      oauthConnection: null,
    } as unknown as ManagementContext;

    const result = createOAuthEngine(context).startCloudOAuth("notion");
    await vi.advanceTimersByTimeAsync(1000);

    await expect(result).resolves.toBe(true);
    expect(mocks.enableCloudConnectionForChat).toHaveBeenCalledWith(
      "managed-connection",
    );
    expect(mocks.createSession).toHaveBeenCalledWith(
      { providerConnectionCreateRequest: { provider: "notion" } },
    );
    expect(mocks.getSession).toHaveBeenCalledWith(
      { sessionId: "managed-session" },
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("keeps polling for the full Cloud OAuth session lifetime", async () => {
    mocks.createSession.mockResolvedValueOnce({
      data: {
        session_id: "slow-managed-session",
        authorization_start_url:
          "https://localhost:8443/v1/provider-connections/authorize/ccccccccccccccccccccccccccccccccccccccccccc",
      },
    });
    let polls = 0;
    mocks.getSession.mockImplementation(async () => {
      polls += 1;
      return {
        data: polls > 120
          ? { status: "COMPLETED", auth_connection_id: "slow-managed-connection" }
          : { status: "WAITING_USER" },
      };
    });

    const context = {
      t: (key: string) => key,
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
      notionAuthAccounts: [],
      selectedType: "notion",
      oauthState: "idle",
      connectionVerified: false,
      oauthConnection: null,
    } as unknown as ManagementContext;

    const result = createOAuthEngine(context).startCloudOAuth("notion");
    await vi.advanceTimersByTimeAsync(121_000);

    await expect(result).resolves.toBe(true);
    expect(polls).toBe(121);
    expect(mocks.enableCloudConnectionForChat).toHaveBeenCalledWith(
      "slow-managed-connection",
    );
  });

  it("reserves the Web popup before the managed session request settles", async () => {
    let resolveSession!: (value: { data: Record<string, never> }) => void;
    const sessionResponse = new Promise<{ data: Record<string, never> }>((resolve) => {
      resolveSession = resolve;
    });
    mocks.createSession.mockReturnValue(sessionResponse);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false }));

    const context = {
      t: (key: string) => key,
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
      notionAuthAccounts: [],
      selectedType: "notion",
      oauthState: "idle",
      connectionVerified: false,
      oauthConnection: null,
    } as unknown as ManagementContext;

    const pending = createOAuthEngine(context).startCloudOAuth("notion");
    expect.soft(mocks.reserveManagedAuthorizationPopup).toHaveBeenCalledOnce();
    expect.soft(mocks.createSession).toHaveBeenCalledOnce();
    resolveSession({ data: {} });
    await expect(pending).resolves.toBe(false);
  });

  it("recovers Feishu authorization when the browser blocks the initial popup", async () => {
    const recoveryPopup = { kind: "recovery-popup" };
    mocks.reserveManagedAuthorizationPopup
      .mockReturnValueOnce(null)
      .mockReturnValueOnce(recoveryPopup);
    mocks.openFeishuCLIAuthorization
      .mockResolvedValueOnce({ ok: false, reason: "blocked" })
      .mockResolvedValueOnce({ ok: true });
    mocks.createSession.mockResolvedValueOnce({
      data: {
        session_id: "feishu-cli-session",
        authorization_start_url: "https://accounts.feishu.cn/open",
      },
    });
    mocks.getSession.mockResolvedValueOnce({
      data: {
        status: "COMPLETED",
        auth_connection_id: "feishu-cli-connection",
      },
    });
    mocks.modalConfirm.mockImplementation((options) => {
      void options.onOk();
      return { destroy: vi.fn() };
    });

    const pending = startFeishuCLISession();
    await vi.advanceTimersByTimeAsync(1000);

    await expect(pending).resolves.toBe("feishu-cli-connection");
    expect(mocks.createSession).toHaveBeenCalledWith(
      { providerConnectionCreateRequest: { provider: "feishu" } },
    );
    expect(mocks.modalConfirm).toHaveBeenCalledOnce();
    expect(mocks.openFeishuCLIAuthorization).toHaveBeenNthCalledWith(
      2,
      "https://accounts.feishu.cn/open",
      recoveryPopup,
    );
  });

  it("reauthorizes an existing managed connection without entering the legacy BYO flow", async () => {
    mocks.reauthorize.mockResolvedValueOnce({
      data: {
        session_id: "managed-reauthorize-session",
        authorization_start_url:
          "https://localhost:8443/v1/provider-connections/authorize/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      },
    });
    mocks.getSession.mockResolvedValueOnce({
      data: {
        status: "COMPLETED",
        auth_connection_id: "managed-connection",
      },
    });

    const context = {
      t: (key: string) => key,
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
      notionAuthAccounts: [],
      selectedType: "feishu",
      oauthState: "connected",
      connectionVerified: true,
      oauthConnection: { connectionId: "managed-connection" },
    } as unknown as ManagementContext;

    const pending = createOAuthEngine(context).startCloudOAuth("notion", {
      reauthorizeConnectionId: "managed-connection",
    } as never);
    await vi.advanceTimersByTimeAsync(1000);

    await expect(pending).resolves.toBe(true);
    expect(mocks.reauthorize).toHaveBeenCalledWith(
      { authConnectionId: "managed-connection" },
    );
    expect(mocks.createSession).not.toHaveBeenCalledWith(
      { providerConnectionCreateRequest: { provider: "notion" } },
    );
  });

  it.each([false, true])("reads local Feishu accounts after Cloud reconciliation (offline=%s)", async (offline) => {
    if (offline) mocks.reconcileConnections.mockRejectedValueOnce(new Error("offline"));
    else mocks.reconcileConnections.mockResolvedValueOnce({ data: { items: [] } });
    const context = {
      t: (key: string) => key,
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
      notionAuthAccounts: [],
      selectedType: "feishu",
      oauthState: "idle",
      connectionVerified: false,
      oauthConnection: null,
      cloudManagedOAuthAvailable: true,
    } as unknown as ManagementContext;

    await createOAuthEngine(context).refreshFeishuAuthAccounts();

    expect(mocks.reconcileConnections).toHaveBeenCalledWith(
      expect.objectContaining({ silentError: true }),
    );
    expect(mocks.reconcileConnections.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.listConnections.mock.invocationCallOrder[0],
    );
  });

  it("loads local Feishu accounts without Cloud reconciliation when managed OAuth is unavailable", async () => {
    const context = {
      t: (key: string) => key,
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
      notionAuthAccounts: [],
      selectedType: "feishu",
      oauthState: "idle",
      connectionVerified: false,
      oauthConnection: null,
      cloudManagedOAuthAvailable: false,
    } as unknown as ManagementContext;

    await createOAuthEngine(context).refreshFeishuAuthAccounts();

    expect(mocks.reconcileConnections).not.toHaveBeenCalled();
    expect(mocks.listConnections).toHaveBeenCalledWith({
      provider: "feishu",
      status: null,
    });
  });

  const sessionFlows = [
    { name: "managed OAuth", start: () => startManagedOAuthSession("notion"), failure: "DENIED" },
    { name: "Feishu CLI", start: () => startFeishuCLISession(), failure: "CLI_UNAVAILABLE" },
  ];

  it.each(sessionFlows)("retries transient polling errors for $name", async ({ start }) => {
    mocks.createSession.mockResolvedValue({ data: { session_id: "session", authorization_start_url: "https://example.com/authorize" } });
    mocks.getSession.mockRejectedValueOnce({ response: { status: 503 } }).mockResolvedValueOnce({ data: { status: "COMPLETED", auth_connection_id: "connection" } });
    const pending = start();
    await vi.advanceTimersByTimeAsync(2000);
    await expect(pending).resolves.toBe("connection");
    expect(mocks.getSession).toHaveBeenCalledTimes(2);
  });

  it.each(sessionFlows)("stops polling terminal failures for $name", async ({ start, failure }) => {
    mocks.createSession.mockResolvedValue({ data: { session_id: "session", authorization_start_url: "https://example.com/authorize" } });
    mocks.getSession.mockResolvedValue({ data: { status: failure } });
    const pending = start();
    await vi.advanceTimersByTimeAsync(2000);
    await expect(pending).resolves.toBeNull();
    expect(mocks.getSession).toHaveBeenCalledOnce();
  });
});
