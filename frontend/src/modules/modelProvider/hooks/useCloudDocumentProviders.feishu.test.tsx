import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Form, Input } from "antd";
import { beforeEach, expect, it, vi } from "vitest";
import { loadFeishuAuthAccounts, persistFeishuAppSetup } from "@/modules/dataSource/common/feishuAccounts";
import { useCloudDocumentProviders } from "./useCloudDocumentProviders";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), credentials: vi.fn(), navigate: vi.fn(), t: (key: string) => key,
}));
vi.mock("react-i18next", async (importOriginal) => ({
  ...await importOriginal<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: mocks.t }),
}));
vi.mock("react-router-dom", () => ({ useNavigate: () => mocks.navigate }));
vi.mock("@/runtime/cloud/session", () => ({
  getCloudSession: async () => null, isCloudBusinessAvailable: () => false,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "fixture-cloud-change",
}));
vi.mock("./useLocalDataSourceSettings", () => ({
  useLocalDataSourceSettings: () => ({ loading: false, canCreateLocalSource: true }),
}));
vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: mocks.list,
    getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet: mocks.credentials,
  },
  dataSourceProviderConnectionsApi: {},
}));

function SetupForm() {
  const vm = useCloudDocumentProviders();
  return <>
    <button disabled={vm.loading} onClick={() => void vm.handleManageFeishuAuth()}>新增飞书账号</button>
    <Form form={vm.feishuSetupForm}>
      <Form.Item name="name"><Input aria-label="账号描述" /></Form.Item>
      <Form.Item name="appId"><Input aria-label="App ID" /></Form.Item>
      <Form.Item name="appSecret"><Input aria-label="App Secret" /></Form.Item>
    </Form>
  </>;
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  sessionStorage.clear();
  mocks.list.mockResolvedValue({ data: { items: [] } });
  mocks.credentials.mockImplementation(async ({ provider }: { provider: string }) => ({ data:
    provider === "feishu"
      ? { app_id: "cli_fixtureold", secret_configured: true }
      : { app_id: "", secret_configured: false },
  }));
});

it.each([false, true])("opens a blank new-account form with saved server credentials (local cache: %s)", async (cached) => {
  if (cached) persistFeishuAppSetup({ appId: "cli_fixtureold", appSecret: "fixture-old-secret" });
  const page = render(<SetupForm />);
  await waitFor(() => expect(screen.getByRole("button", { name: "新增飞书账号" })).toBeEnabled());
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "新增飞书账号" })); });
  await waitFor(() => expect(screen.getByLabelText("App ID")).toHaveValue(""));
  expect(screen.getByLabelText("App Secret")).toHaveValue("");
  expect(screen.getByLabelText("账号描述")).toHaveValue("");

  fireEvent.change(screen.getByLabelText("App ID"), { target: { value: "cli_draft" } });
  fireEvent.change(screen.getByLabelText("App Secret"), { target: { value: "fixture-draft-secret" } });
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "新增飞书账号" })); });
  await waitFor(() => expect(screen.getByLabelText("App ID")).toHaveValue(""));
  expect(screen.getByLabelText("App Secret")).toHaveValue("");

  page.unmount();
  render(<SetupForm />);
  await waitFor(() => expect(screen.getByRole("button", { name: "新增飞书账号" })).toBeEnabled());
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "新增飞书账号" })); });
  await waitFor(() => expect(screen.getByLabelText("App ID")).toHaveValue(""));
  expect(screen.getByLabelText("App Secret")).toHaveValue("");
});

it("keeps the account cache empty when the server has no accounts but an old app setup remains", async () => {
  persistFeishuAppSetup({ appId: "cli_fixtureold", appSecret: "fixture-old-secret" });
  render(<SetupForm />);
  await waitFor(() => expect(screen.getByRole("button", { name: "新增飞书账号" })).toBeEnabled());
  expect(loadFeishuAuthAccounts()).toEqual([]);
});
