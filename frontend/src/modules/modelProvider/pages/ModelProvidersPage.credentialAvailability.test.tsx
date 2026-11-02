import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(), getProvidersWithGroups: vi.fn(), getGroups: vi.fn(),
  createGroup: vi.fn(), updateGroup: vi.fn(), getModels: vi.fn(),
  translate: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code, getLocalizedErrorMessage: (error: Error) => error.message }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
    apiCoreModelProvidersModelProviderIdGroupsPost: mocks.createGroup,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch: mocks.updateGroup,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(), getCredentialRestoreDiscovery: vi.fn(),
  getCredentialRestoreOperation: vi.fn(), setCredentialBackupEnabled: vi.fn(),
  startCredentialRestore: vi.fn(), cancelCredentialRestore: vi.fn(),
}));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: vi.fn().mockResolvedValue({ configured: false, state: "signed_out" }),
  isCloudBusinessAvailable: () => false, beginCloudLogin: vi.fn(),
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(), closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(), openCloudTokenPlan: vi.fn(),
}));
vi.mock("../components/CredentialBackupPanel", () => ({ CredentialBackupPanel: () => null }));
vi.mock("../components/CredentialRestorePanel", () => ({ CredentialRestorePanel: () => null }));
vi.mock("../components/CloudSystemProviderCard", () => ({ default: () => null }));

const provider = { id: "fixture-provider", name: "OpenAI", base_url: "https://api.example.test/v1/" };
type SavedGroup = {
  id: string; name: string; base_url: string; has_api_key: boolean;
  is_verified: boolean; credential_status?: string;
  keys?: Array<{ id: string; masked: string; enabled: boolean }>;
};
let savedGroups: SavedGroup[];
const renderPage = () => render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  mocks.getModels.mockResolvedValue({ data: { models: [] } });
  savedGroups = [{
    id: "unreadable", name: "Old connection", base_url: provider.base_url,
    has_api_key: true, is_verified: false, credential_status: "unavailable",
  }];
  mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getGroups.mockImplementation(async () => ({ data: { groups: savedGroups } }));
});

describe("provider credential availability", () => {
  it("recovers an unreadable saved connection through the supplier key field and keeps it after reopening", async () => {
    mocks.updateGroup.mockImplementation(async () => {
      savedGroups = [{ ...savedGroups[0], credential_status: undefined,
        keys: [{ id: "replacement", masked: "********4321", enabled: false }] }];
      return { data: savedGroups[0] };
    });
    const page = renderPage();
    expect(await screen.findByText("modelProvider.credentialUnavailable")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.workspace.checkConnection" })).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("modelProvider.credentialUnavailableHelp");
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.change(input, { target: { value: "fixture-replacement-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(mocks.updateGroup).toHaveBeenCalledWith({
      modelProviderId: provider.id, groupId: "unreadable",
      updateModelProviderGroupOpenAPIRequest: {
        name: "Old connection", base_url: provider.base_url,
        api_key: "fixture-replacement-key", verify: false,
      },
    }, { timeout: 180000 }));
    await waitFor(() => expect(screen.queryByText("modelProvider.credentialUnavailable")).not.toBeInTheDocument());
    page.unmount();

    renderPage();
    expect(await screen.findByText("****4321")).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.credentialUnavailable")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.workspace.checkConnection" })).toBeEnabled();
  });

  it("requires an explicit replacement and preserves the warning and draft if saving fails", async () => {
    mocks.updateGroup.mockRejectedValue(new Error("fixture save failed"));
    renderPage();
    expect(await screen.findByText("modelProvider.credentialUnavailable")).toBeInTheDocument();
    const input = screen.getByLabelText("modelProvider.apiKeyLabel", { selector: "input" });
    fireEvent.blur(input);
    expect(mocks.updateGroup).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "fixture-replacement-key" } });
    fireEvent.blur(input);
    await waitFor(() => expect(mocks.updateGroup).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("fixture save failed")).toBeInTheDocument();
    expect(input).toHaveValue("fixture-replacement-key");
    expect(screen.getByText("modelProvider.credentialUnavailable")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.workspace.checkConnection" })).toBeDisabled();
  });
});
