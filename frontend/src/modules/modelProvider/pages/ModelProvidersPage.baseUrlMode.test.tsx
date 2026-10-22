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

async function renderPage(
  groups: unknown[] = [group],
  provider: typeof openAI & { is_custom?: boolean; protocol_name?: string } = openAI,
) {
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
  await screen.findByRole("textbox", { name: "modelProvider.apiAddressLabel" });
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

describe("supplier endpoints and direct setup", () => {
  it.each([
    ["AIhubmix", "OpenAI"],
    ["Doubao", "Doubao"],
  ])("uses a neutral logo for migrated %s with %s protocol", async (name, protocol) => {
    await renderPage([group], {
      ...openAI,
      name,
      is_custom: true,
      protocol_name: protocol,
    });
    const logos = document.querySelectorAll(".provider-workspace-logo img");
    expect(logos.length).toBeGreaterThan(0);
    logos.forEach((logo) => expect(logo).toHaveAttribute("src", "/provider-icons/custom.svg"));
  });

  it("shows supplier settings directly without an instance selector", async () => {
    await renderPage();
    expect(screen.getAllByRole("textbox", { name: "modelProvider.apiAddressLabel" })).toHaveLength(
      1,
    );
    expect(screen.getByText("********1234")).toBeInTheDocument();
    expect(
      screen.queryByRole("combobox", {
        name: "modelProvider.workspace.selectConnection",
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: "modelProvider.workspace.addConnection",
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.addSupplier",
      }),
    ).toBeInTheDocument();
  });
  it("saves the endpoint through the supplier API and blocks key actions until saved", async () => {
    await renderPage();
    const endpoint = screen.getByRole("textbox", { name: "modelProvider.apiAddressLabel" });
    fireEvent.change(endpoint, { target: { value: customUrl } });
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.checkConnection",
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.workspace.addKey",
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
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.apiAddressLabel" }), {
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

  it("starts a custom model directly for an unconfigured supplier", async () => {
    await renderPage([], { ...openAI, base_url: customUrl });
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.customModel" }),
    );
    const dialog = within(await screen.findByRole("dialog"));
    expect(
      dialog.queryByLabelText("modelProvider.groupName"),
    ).not.toBeInTheDocument();
    expect(
      dialog.queryByRole("combobox", { name: "modelProvider.group" }),
    ).not.toBeInTheDocument();
    expect(mocks.createGroup).toHaveBeenCalledWith({
      modelProviderId: openAI.id,
      createModelProviderGroupOpenAPIRequest: {
        name: "OpenAI",
        api_key: "",
        verify: false,
      },
    });
  });
  it("checks a newly initialized supplier without using stale page state", async () => {
    await renderPage([]);
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.checkConnection",
      }),
    );
    await waitFor(() => expect(mocks.checkGroup).toHaveBeenCalled());
    expect(mocks.checkGroup.mock.calls[0][0].groupId).toBe("saved-fixture");
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
