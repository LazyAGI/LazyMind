import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  createConnection: vi.fn(),
  listConnections: vi.fn(),
  refreshConnectionToken: vi.fn(),
  updateConnection: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    createConnectionApiAuthserviceV1CloudProviderConnectionsPost: apiMocks.createConnection,
    listConnectionsApiAuthserviceV1CloudConnectionsGet: apiMocks.listConnections,
    refreshConnectionTokenApiAuthserviceV1CloudConnectionsConnectionIdTokenRefreshPost:
      apiMocks.refreshConnectionToken,
    updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut:
      apiMocks.updateConnection,
  },
}));

import WeChatOfficialAccountPage from "./WeChatOfficialAccountPage";

describe("WeChatOfficialAccountPage", () => {
  beforeAll(() => {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }),
    });
  });

  beforeEach(() => {
    apiMocks.createConnection.mockReset().mockResolvedValue({
      data: { connection_id: "conn-wechat" },
    });
    apiMocks.listConnections.mockReset().mockResolvedValue({
      data: { data: { items: [] } },
    });
    apiMocks.refreshConnectionToken.mockReset().mockResolvedValue({ data: {} });
    apiMocks.updateConnection.mockReset().mockResolvedValue({ data: {} });
  });

  it("saves, lists, and verifies a new account with the backend response envelope", async () => {
    apiMocks.listConnections
      .mockResolvedValueOnce({ data: { data: { items: [] } } })
      .mockResolvedValue({
        data: {
          data: {
            items: [
              {
                connection_id: "conn-wechat",
                provider: "wechat",
                auth_mode: "service_account",
                app_id: "wx-app-id",
                display_name: "公众号测试账号",
                provider_options: {},
                status: "PENDING",
                created_at: "2026-08-31T00:00:00Z",
              },
            ],
          },
        },
      });

    render(
      <MemoryRouter>
        <WeChatOfficialAccountPage />
      </MemoryRouter>,
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: /modelProvider\.wechatOfficialAccount\.createAccount$/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(
      within(dialog).getByLabelText("modelProvider.wechatOfficialAccount.accountName"),
      { target: { value: "公众号测试账号" } },
    );
    fireEvent.change(within(dialog).getByLabelText("admin.dataSourceAppId"), {
      target: { value: "wx-app-id" },
    });
    fireEvent.change(within(dialog).getByLabelText("admin.dataSourceAppSecret"), {
      target: { value: "wx-app-secret" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(apiMocks.createConnection).toHaveBeenCalledWith({
        provider: "wechat",
        cloudConnectionCreateBody: {
          auth_mode: "service_account",
          client_id: "wx-app-id",
          client_secret: "wx-app-secret",
          display_name: "公众号测试账号",
        },
      });
    });
    expect(apiMocks.updateConnection).not.toHaveBeenCalled();

    expect(await screen.findByText("公众号测试账号")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", {
        name: /modelProvider\.wechatOfficialAccount\.verifyConnection$/,
      }),
    );
    await waitFor(() => {
      expect(apiMocks.refreshConnectionToken).toHaveBeenCalledWith(
        { connectionId: "conn-wechat" },
        { silentError: true },
      );
    });
  });
});
