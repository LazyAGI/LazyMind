import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getProvidersWithGroups: vi.fn(),
  getGroups: vi.fn(),
  createGroup: vi.fn(),
  updateEndpoint: vi.fn(),
  getModels: vi.fn(),
  checkGroup: vi.fn(),
  translate: (key: string, options?: { name?: string }) =>
    options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsPost: mocks.createGroup,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdCheckPost:
      mocks.checkGroup,
  },
  modelProvidersDefaultApi: {},
  updateProviderEndpoint: mocks.updateEndpoint,
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(),
  getCredentialRestoreDiscovery: vi.fn(),
  getCredentialRestoreOperation: vi.fn(),
  setCredentialBackupEnabled: vi.fn(),
  startCredentialRestore: vi.fn(),
  cancelCredentialRestore: vi.fn(),
}));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: vi
    .fn()
    .mockResolvedValue({ configured: false, state: "signed_out" }),
  isCloudBusinessAvailable: () => false,
  beginCloudLogin: vi.fn(),
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(),
  closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(),
  openCloudTokenPlan: vi.fn(),
}));
vi.mock("../components/CredentialBackupPanel", () => ({
  CredentialBackupPanel: () => null,
}));
vi.mock("../components/CredentialRestorePanel", () => ({
  CredentialRestorePanel: () => null,
}));
vi.mock("../components/CloudSystemProviderCard", () => ({
  default: () => null,
}));

const openAI = {
  id: "fixture-provider",
  name: "OpenAI",
  base_url: "https://api.example.test/v1/",
};
const customUrl = "https://custom.example.test/v1/";
const group = {
  id: "fixture-group",
  name: "Production",
  base_url: openAI.base_url,
  has_api_key: true,
  is_verified: true,
  keys: [{ id: "opaque-key", masked: "********1234" }],
};

async function renderPage(groups: unknown[] = [group], provider = openAI) {
  mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getProvidersWithGroups.mockResolvedValue({
    data: { providers: groups.length ? [provider] : [] },
  });
  mocks.getGroups.mockResolvedValue({ data: { groups } });
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <ModelProviderPage />
    </ConfigProvider>,
  );
  await screen.findByRole("textbox", { name: "API Host" });
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.createGroup.mockResolvedValue({
    data: {
      id: "saved-fixture",
      name: "OpenAI",
      base_url: openAI.base_url,
      is_verified: false,
    },
  });
  mocks.updateEndpoint.mockResolvedValue({
    id: openAI.id,
    base_url: customUrl,
  });
  mocks.getModels.mockResolvedValue({ data: { models: [] } });
  mocks.checkGroup.mockResolvedValue({ data: { success: true } });
});

describe("supplier-level endpoints and connection instances", () => {
  it("keeps one supplier and shared endpoint for multiple instances, without adding suppliers", async () => {
    await renderPage([
      group,
      {
        ...group,
        id: "backup",
        name: "Backup",
        base_url: "https://obsolete.example.test",
      },
    ]);
    expect(screen.getAllByRole("textbox", { name: "API Host" })).toHaveLength(
      1,
    );
    expect(
      screen.queryByRole("button", {
        name: /configureAndAdd|removeProviderAria/,
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("https://obsolete.example.test"),
    ).not.toBeInTheDocument();
    expect(screen.getByText("********1234")).toBeInTheDocument();
    fireEvent.mouseDown(
      screen.getByRole("combobox", {
        name: "modelProvider.workspace.selectConnection",
      }),
    );
    expect(
      await screen.findByText("Backup", {
        selector: ".ant-select-item-option-content",
      }),
    ).toBeInTheDocument();
  });

  it("saves the endpoint through the supplier API and blocks connection actions until saved", async () => {
    await renderPage();
    const endpoint = screen.getByRole("textbox", { name: "API Host" });
    fireEvent.change(endpoint, { target: { value: customUrl } });
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.checkConnection",
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.addConnection",
      }),
    ).toBeDisabled();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.saveEndpoint",
      }),
    );
    await waitFor(() =>
      expect(mocks.updateEndpoint).toHaveBeenCalledWith(openAI.id, customUrl),
    );
    expect(mocks.createGroup).not.toHaveBeenCalled();
  });

  it("rejects invalid API hosts before sending an update", async () => {
    await renderPage();
    fireEvent.change(screen.getByRole("textbox", { name: "API Host" }), {
      target: { value: "bad-url" },
    });
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.saveEndpoint",
      }),
    );
    expect(
      (await screen.findAllByText("modelProvider.validation.baseUrlInvalid"))
        .length,
    ).toBeGreaterThan(0);
    expect(mocks.updateEndpoint).not.toHaveBeenCalled();
  });

  it("has no instance endpoint field and keeps the provider endpoint in the create contract", async () => {
    await renderPage([], { ...openAI, base_url: customUrl });
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.configureConnection",
      }),
    );
    const dialog = within(await screen.findByRole("dialog"));
    expect(
      dialog.queryByRole("textbox", { name: /Base URL|API Host/ }),
    ).not.toBeInTheDocument();
    expect(dialog.getByText(customUrl)).toBeInTheDocument();
    fireEvent.click(
      dialog.getByRole("button", { name: "modelProvider.saveConfig" }),
    );
    await waitFor(() =>
      expect(mocks.createGroup).toHaveBeenCalledWith(
        {
          modelProviderId: openAI.id,
          createModelProviderGroupOpenAPIRequest: {
            name: "OpenAI",
            base_url: customUrl,
            verify: false,
          },
        },
        undefined,
      ),
    );
  });

  it("checks the stored key without sending an empty or masked secret", async () => {
    await renderPage();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.checkConnection",
      }),
    );
    await waitFor(() => expect(mocks.checkGroup).toHaveBeenCalled());
    const payload =
      mocks.checkGroup.mock.calls[0][0].checkModelProviderOpenAPIRequest;
    expect(payload.base_url).toBe(openAI.base_url);
    expect(payload).not.toHaveProperty("api_key");
  });

  it("checks a custom endpoint without requiring a key", async () => {
    await renderPage(
      [{ ...group, has_api_key: false, keys: [], base_url: customUrl }],
      { ...openAI, base_url: customUrl },
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.checkConnection",
      }),
    );
    await waitFor(() => expect(mocks.checkGroup).toHaveBeenCalled());
    expect(
      mocks.checkGroup.mock.calls[0][0].checkModelProviderOpenAPIRequest,
    ).not.toHaveProperty("api_key");
  });
});
