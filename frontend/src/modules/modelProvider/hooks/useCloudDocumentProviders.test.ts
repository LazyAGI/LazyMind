import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { useCloudDocumentProviders } from "./useCloudDocumentProviders";

const stableT = (key: string) => key;
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: stableT }),
}));

const mockNavigate = vi.fn();
vi.mock("react-router-dom", () => ({
  useNavigate: () => mockNavigate,
}));

vi.mock("antd", () => ({
  Form: { useForm: () => [{ setFieldsValue: vi.fn(), validateFields: vi.fn().mockResolvedValue({ appId: "app-1", appSecret: "secret-1" }) }] },
  message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet: vi.fn(),
    listConnectionsApiAuthserviceV1CloudConnectionsGet: vi.fn(),
  },
}));

vi.mock("@/modules/dataSource/common/feishuAccounts", () => ({
  createFeishuAccountId: () => "acc-new",
  getOAuthStateFromConnection: (connection?: { status?: string } | null) =>
    connection?.status === "connected" ? "connected" : "pending",
  loadFeishuAppSetup: vi.fn(() => null),
  loadFeishuAuthAccounts: vi.fn(() => []),
  persistFeishuAuthAccounts: vi.fn(),
  persistFeishuAppSetup: vi.fn(),
}));

vi.mock("@/modules/dataSource/common/feishuOAuth", () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: "feishu-oauth",
  consumeCloudDataSourceOAuthResult: vi.fn(() => null),
  consumeFeishuDataSourceOAuthResult: vi.fn(() => null),
}));

vi.mock("@/modules/dataSource/hooks/management/createOAuthEngine", () => ({
  createOAuthEngine: vi.fn(() => ({
    refreshFeishuAuthAccounts: vi.fn(),
    refreshNotionAuthConnection: vi.fn(),
    upsertFeishuAuthAccount: vi.fn(() => ({ id: "acc-new" })),
    saveCloudAppCredentials: vi.fn(),
    startCloudOAuth: vi.fn(),
    applyOauthResult: vi.fn(),
    clearOauthAttempt: vi.fn(),
  })),
}));

vi.mock("@/modules/dataSource/utils/notionSetup", () => ({
  loadNotionAppSetup: vi.fn(() => null),
  persistNotionAppSetup: vi.fn(),
}));

vi.mock("@/modules/dataSource/mappers/dataSourceConnection", () => ({
  getCloudConnectionItems: (payload: { items?: unknown[] }) => payload.items || [],
  mapCloudConnectionToDataSourceConnection: (item: Record<string, unknown>) => ({
    connectionId: item.connection_id,
    status: item.status,
  }),
}));

vi.mock("./useLocalDataSourceSettings", () => ({
  useLocalDataSourceSettings: () => ({
    loading: false,
    canCreateLocalSource: true,
    localSourceCount: 3,
  }),
}));

const mockedGetCredentials =
  dataSourceCloudOauthApi.getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet as unknown as ReturnType<
    typeof vi.fn
  >;
const mockedListConnections =
  dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
  mockedGetCredentials.mockResolvedValue({ data: { app_id: "", secret_configured: false } });
  mockedListConnections.mockResolvedValue({ data: { items: [] } });
});

describe("useCloudDocumentProviders", () => {
  it("finishes the initial page load and exposes the local source count", async () => {
    const { result } = renderHook(() => useCloudDocumentProviders());

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.localSourceCount).toBe(3);
    expect(result.current.cloudDocumentsPath).toBe("/cloud-documents");
  });

  it("navigates to the feishu management route", async () => {
    const { result } = renderHook(() => useCloudDocumentProviders());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.handleManageFeishuAuth();
    });

    expect(mockNavigate).toHaveBeenCalledWith("/cloud-documents/feishu");
  });

  it("marks google drive auth valid once a connected connection is found", async () => {
    mockedListConnections.mockResolvedValue({
      data: { items: [{ connection_id: "gd-1", status: "connected" }] },
    });

    const { result } = renderHook(() => useCloudDocumentProviders());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await waitFor(() => expect(result.current.isGoogleDriveAuthValid).toBe(true));

    expect(result.current.googleDriveConnection).toEqual(
      expect.objectContaining({ connectionId: "gd-1", status: "connected" }),
    );
  });

  it("opens the cloud setup modal with the requested provider and intent", async () => {
    const { result } = renderHook(() => useCloudDocumentProviders());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.openCloudSetupModal("notion", "auth");
    });

    expect(result.current.feishuSetupModalOpen).toBe(true);
    expect(result.current.cloudSetupProvider).toBe("notion");
    expect(result.current.feishuSetupIntent).toBe("auth");
  });

  it("saves feishu setup credentials and shows a success message", async () => {
    const { result } = renderHook(() => useCloudDocumentProviders());
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.openCloudSetupModal("feishu", "manage");
    });

    await act(async () => {
      await result.current.handleSaveFeishuSetup();
    });

    expect(message.success).toHaveBeenCalledWith("modelProvider.cloudDocuments.feishuCredentialSaved");
    expect(result.current.feishuSetupModalOpen).toBe(false);
  });
});
