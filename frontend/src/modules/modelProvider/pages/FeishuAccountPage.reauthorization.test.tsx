import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { message } from "antd";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import FeishuAccountPage from "./FeishuAccountPage";
import { mapCloudConnectionToFeishuAccount } from "@/modules/dataSource/mappers/cloudConnection";
import { isFeishuAccountAuthValid } from "@/modules/dataSource/utils/feishuAccount";

const mocks = vi.hoisted(() => ({
  pending: false, list: vi.fn(), update: vi.fn(), reauthorize: vi.fn(), poll: vi.fn(), close: vi.fn(),
  t: (key: string) => ({
    "modelProvider.cloudDocuments.authValid": "认证有效", "modelProvider.cloudDocuments.authPending": "待授权",
    "admin.dataSourceFeishuReconnectAction": "重新授权",
  } as Record<string, string>)[key] || key,
}));
vi.mock("react-i18next", async (original) => ({
  ...await original<typeof import("react-i18next")>(), useTranslation: () => ({ t: mocks.t }),
}));
vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: () => "fixture error", localizeErrorCode: () => "fixture error" }));
vi.mock("@/runtime/cloud/session", () => ({ getCloudSession: async () => null, isCloudBusinessAvailable: () => false }));
vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
    updateConnectionApiAuthserviceV1CloudConnectionsConnectionIdPut: mocks.update,
  },
  dataSourceProviderConnectionsApi: {
    apiCoreProviderConnectionsAuthConnectionIdReauthorizePost: mocks.reauthorize,
    apiCoreProviderConnectionsSessionsSessionIdGet: mocks.poll,
  },
}));
vi.mock("@/modules/dataSource/oauth/openManagedAuthorization", () => ({
  reserveManagedAuthorizationPopup: () => ({ closed: false }),
  openFeishuCLIAuthorization: async () => ({ ok: true }), openManagedAuthorization: async () => ({ ok: true }),
  closeManagedAuthorizationPopup: mocks.close,
}));
const page = () => render(<MemoryRouter><FeishuAccountPage /></MemoryRouter>);
beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  mocks.pending = false;
  mocks.list.mockImplementation(async () => ({ data: { items: [{
    connection_id: "fixture-account", provider: "feishu", status: "ACTIVE", display_name: "Fixture",
    provider_account_meta: { authorization_pending: mocks.pending },
    connection_method: "cli_personal_app", credential_location: "local", created_at: "2026-01-01T00:00:00Z",
  }] } }));
  // This owner has user.write/model.read, but not model.write.
  mocks.update.mockRejectedValue({ response: { status: 403 } });
  mocks.reauthorize.mockImplementation(async () => {
    mocks.pending = true;
    return { data: { session_id: "fixture-session", authorization_start_url: "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture" } };
  });
  mocks.poll.mockResolvedValue({ data: { status: "AUTH_CANCELED" } });
});
afterEach(() => { cleanup(); act(() => message.destroy()); });

it("shows pending after interrupted authorization and keeps it after page reload", async () => {
  const initial = page();
  expect(await screen.findByText("认证有效")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /重新授权/ }));
  await waitFor(() => expect(mocks.close).toHaveBeenCalledOnce(), { timeout: 3000 });
  expect(mocks.update).not.toHaveBeenCalled();
  expect(mocks.reauthorize).toHaveBeenCalledWith({ authConnectionId: "fixture-account" });
  expect(await screen.findByText("待授权")).toBeInTheDocument();
  expect(screen.queryByText("认证有效")).not.toBeInTheDocument();
  initial.unmount();
  page();
  expect(await screen.findByText("待授权")).toBeInTheDocument();
  expect(screen.queryByText("认证有效")).not.toBeInTheDocument();
});

it("keeps a pending-display account connected for existing data-source selection and browsing", () => {
  const account = mapCloudConnectionToFeishuAccount({
    connection_id: "fixture-account", status: "ACTIVE", provider_account_meta: { authorization_pending: true },
    tenant_id: "", provider: "feishu", auth_mode: "oauth_user", can_use_chat: true, created_at: "2026-01-01T00:00:00Z",
  });
  expect(account.status).toBe("connected");
  expect(account.connection?.status).toBe("connected");
  expect(isFeishuAccountAuthValid(account)).toBe(true);
});
