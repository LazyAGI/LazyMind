import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ModelProviderPage, { type AddedProvider, type ProviderConnectionGroup } from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  providers: vi.fn(), withGroups: vi.fn(), groups: vi.fn(), models: vi.fn(), remote: vi.fn(), add: vi.fn(), remove: vi.fn(),
  translate: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }) }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code, getLocalizedErrorMessage: (error: unknown) => String(error) }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.providers,
    apiCoreModelProvidersWithGroupsGet: mocks.withGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.groups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.models,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsPost: mocks.add,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdDelete: mocks.remove,
  },
  modelProvidersDefaultApi: {},
  listRemoteGroupModels: mocks.remote,
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(), getCredentialRestoreDiscovery: vi.fn(), getCredentialRestoreOperation: vi.fn(),
  setCredentialBackupEnabled: vi.fn(), startCredentialRestore: vi.fn(), cancelCredentialRestore: vi.fn(),
}));
vi.mock("../components/ProviderWorkspace", () => ({ ProviderWorkspace: (props: { configuredProviders: AddedProvider[]; onSyncModels: (provider: AddedProvider, group: ProviderConnectionGroup) => void }) => {
  const provider = props.configuredProviders[0];
  const group = provider?.groups[0];
  return <button disabled={!group} onClick={() => props.onSyncModels(provider, group)}>Sync models</button>;
} }));
vi.mock("@/runtime/cloud/session", () => ({ LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "cloud-changed", getCloudSession: vi.fn().mockResolvedValue({ configured: false, state: "signed_out" }), isCloudBusinessAvailable: () => false, beginCloudLogin: vi.fn() }));
vi.mock("@/runtime/desktopBridge", () => ({ reserveCloudLoginPopup: vi.fn(), closeCloudLoginPopup: vi.fn(), openCloudLogin: vi.fn(), openCloudTokenPlan: vi.fn() }));
vi.mock("../components/CredentialBackupPanel", () => ({ CredentialBackupPanel: () => null }));
vi.mock("../components/CredentialRestorePanel", () => ({ CredentialRestorePanel: () => null }));
vi.mock("../components/CloudSystemProviderCard", () => ({ default: () => null }));

beforeEach(() => {
  vi.clearAllMocks();
  const supplier = { id: "supplier", name: "Gateway", protocol_name: "OpenAI", is_custom: true, base_url: "https://example.test/v1" };
  mocks.providers.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.withGroups.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.groups.mockResolvedValue({ data: { groups: [{ id: "store", name: "Gateway", is_verified: true }] } });
  mocks.models.mockResolvedValue({ data: { models: [] } });
  mocks.remote.mockResolvedValue({ models: [{ id: "qwen-plus", name: "qwen-plus", model_type: "llm", added: false }] });
  mocks.add.mockResolvedValue({ data: { id: "saved-model-id", name: "qwen-plus", model_type: "llm", is_default: false } });
  mocks.remove.mockResolvedValue({ data: {} });
});

describe("model catalog integration", () => {
  it("opens discovery in a drawer and changes configuration only after explicit plus or minus", async () => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    expect(mocks.remote).not.toHaveBeenCalled();
    fireEvent.click(sync);
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveClass("provider-model-catalog-drawer");
    expect(mocks.add).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:qwen-plus" }));
    await waitFor(() => expect(mocks.add).toHaveBeenCalledTimes(1));
    expect(mocks.add).toHaveBeenCalledWith({ modelProviderId: "supplier", groupId: "store", addModelProviderGroupModelOpenAPIRequest: { name: "qwen-plus", model_type: "llm", vision: false } });
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.removeModel:qwen-plus" }));
    expect(mocks.remove).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith({ modelProviderId: "supplier", groupId: "store", modelId: "saved-model-id" }, undefined));
    expect(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:qwen-plus" })).toBeInTheDocument();
  });
});
