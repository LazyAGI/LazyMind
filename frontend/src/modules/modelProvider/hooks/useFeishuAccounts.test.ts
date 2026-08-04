import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { useFeishuAccounts } from "./useFeishuAccounts";

vi.mock("antd", () => ({
  Form: { useForm: () => [{ setFieldsValue: vi.fn(), validateFields: vi.fn() }] },
  Modal: { confirm: vi.fn() },
  message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}));

const stableT = (key: string) => key;
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: stableT }),
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: vi.fn(),
    updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut: vi.fn(),
    deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete: vi.fn(),
  },
}));

vi.mock("@/modules/dataSource/common/feishuOAuth", () => ({
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL: "feishu-oauth",
  consumeFeishuDataSourceOAuthResult: vi.fn(() => null),
  getFeishuDataSourceCallbackUrl: () => "http://callback",
  requestFeishuDataSourceAuthorizeUrl: vi.fn(),
  finishFeishuDataSourceOAuth: vi.fn(),
  openCenteredPopup: vi.fn(),
}));

vi.mock("@/modules/dataSource/common/feishuAccounts", () => ({
  createFeishuAccountId: () => "acc-new",
  getOAuthStateFromConnection: (connection?: { status?: string } | null) =>
    connection?.status === "connected" ? "connected" : "pending",
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: () => "localized-error",
  localizeErrorCode: (code?: string) => `localized:${code}`,
}));

vi.mock("@/modules/dataSource/constants/options", () => ({
  FEISHU_DEFAULT_SCOPES: ["scope1"],
}));

vi.mock("@/modules/dataSource/utils/scanAccessors", () => ({
  getScanTenantId: () => "tenant-1",
}));

vi.mock("@/modules/dataSource/mappers/cloudConnection", () => ({
  getCloudConnectionItems: (payload: { items?: unknown[] }) => payload.items || [],
  mapCloudConnectionToFeishuAccount: (item: Record<string, unknown>) => ({
    id: item.connection_id,
    name: item.display_name,
    appId: item.app_id,
    appSecret: "",
    chatEnabled: false,
    status: item.status === "connected" ? "connected" : "pending",
    connection: { connectionId: item.connection_id, status: item.status },
    createdAt: "2024-01-01T00:00:00Z",
  }),
}));

vi.mock("@/modules/dataSource/utils/feishuAccount", () => ({
  isFeishuAccountAuthValid: (account: { status: string; connection?: { connectionId?: string } }) =>
    account.status === "connected" && Boolean(account.connection?.connectionId),
  parseFeishuOAuthCallbackInput: vi.fn(() => null),
}));

const mockedList = dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet as unknown as ReturnType<
  typeof vi.fn
>;
const mockedUpdate =
  dataSourceCloudOauthApi.updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut as unknown as ReturnType<
    typeof vi.fn
  >;

beforeEach(() => {
  vi.clearAllMocks();
  mockedList.mockResolvedValue({
    data: {
      items: [
        { connection_id: "conn-1", display_name: "Acc 1", app_id: "app-1", status: "connected" },
      ],
    },
  });
});

describe("useFeishuAccounts", () => {
  it("loads accounts from the cloud connections list on mount", async () => {
    const { result } = renderHook(() => useFeishuAccounts());

    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    expect(result.current.accounts).toEqual([
      expect.objectContaining({ id: "conn-1", name: "Acc 1", status: "connected" }),
    ]);
  });

  it("toggles chat enabled state optimistically and persists the change", async () => {
    mockedUpdate.mockResolvedValue({});
    const { result } = renderHook(() => useFeishuAccounts());
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    await act(async () => {
      result.current.handleToggleChat(result.current.accounts[0], true);
    });

    expect(result.current.accounts[0].chatEnabled).toBe(true);
    expect(mockedUpdate).toHaveBeenCalledWith({
      connectionId: "conn-1",
      cloudConnectionUpdateBody: { chat_enabled: true, chatEnabled: true },
    });
  });

  it("warns and skips the toggle when enabling chat on an unauthenticated account", async () => {
    mockedList.mockResolvedValue({
      data: {
        items: [{ connection_id: "conn-2", display_name: "Acc 2", app_id: "app-2", status: "pending" }],
      },
    });
    const { result } = renderHook(() => useFeishuAccounts());
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    act(() => {
      result.current.handleToggleChat(result.current.accounts[0], true);
    });

    expect(message.warning).toHaveBeenCalledWith("admin.dataSourceFeishuAccountChatAuthRequired");
    expect(mockedUpdate).not.toHaveBeenCalled();
  });

  it("opens the account modal in edit mode when an existing account is passed", async () => {
    const { result } = renderHook(() => useFeishuAccounts());
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    act(() => {
      result.current.openAccountModal(result.current.accounts[0]);
    });

    expect(result.current.modalOpen).toBe(true);
    expect(result.current.editingAccountId).toBe("conn-1");
  });
});
