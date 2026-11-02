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
  getModels: vi.fn(),
  deleteSupplier: vi.fn(),
  translate: (key: string, options?: { name?: string }) =>
    options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
  getLocalizedErrorMessage: (error: unknown) => error instanceof Error ? error.message : String(error),
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
  },
  modelProvidersDefaultApi: {},
  createCustomProvider: vi.fn(),
  updateCustomProviderName: vi.fn(),
  deleteCustomProvider: mocks.deleteSupplier,
  updateProviderEndpoint: vi.fn(),
  setProviderActiveKey: vi.fn(),
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

const provider = {
  id: "fixture-provider",
  name: "OpenAI",
  is_custom: true,
  protocol_name: "OpenAI",
  base_url: "https://api.example.test/v1",
};
const group = {
  id: "fixture-group",
  name: "Fixture",
  base_url: provider.base_url,
  is_verified: true,
};
const embedding = {
  id: "fixture-embed",
  name: "fixture-embedding",
  model_type: "embed",
};

async function requestDeletion() {
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <ModelProviderPage />
    </ConfigProvider>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "modelProvider.workspace.deleteSupplier" }));
}

beforeEach(() => {
  vi.resetAllMocks();
  mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getProvidersWithGroups.mockResolvedValue({
    data: { providers: [provider] },
  });
  mocks.getGroups.mockResolvedValue({ data: { groups: [group] } });
  mocks.getModels.mockResolvedValue({ data: { models: [embedding] } });
  mocks.deleteSupplier.mockResolvedValue({ id: provider.id, downgraded_knowledge_bases: 0 });
});

describe("custom supplier deletion", () => {
  it.each(["llm", "embed", "embedding", "embed_main"])("confirms once and leaves %s dependency validation to the server", async (model_type) => {
    mocks.getModels.mockResolvedValue({ data: { models: [{ ...embedding, model_type }] } });
    await requestDeletion();
    const dialog = within(await screen.findByRole("dialog"));
    expect(dialog.getByText("modelProvider.workspace.deleteSupplierHint")).toBeInTheDocument();
    expect(mocks.deleteSupplier).not.toHaveBeenCalled();
    fireEvent.click(dialog.getByRole("button", { name: "modelProvider.workspace.deleteSupplier" }));
    await waitFor(() => expect(mocks.deleteSupplier).toHaveBeenCalledWith(provider.id));
  });

  it("does not delete when confirmation is cancelled", async () => {
    await requestDeletion();
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "common.cancel" }));
    expect(mocks.deleteSupplier).not.toHaveBeenCalled();
  });

  it("keeps the supplier and shows guidance when an embedding is occupied", async () => {
    mocks.deleteSupplier.mockRejectedValue({ response: { status: 409, data: { message: "embedding model is in use; remove indexed knowledge bases before removing the model" } } });
    await requestDeletion();
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "modelProvider.workspace.deleteSupplier" }));
    expect(await screen.findByText("modelProvider.embeddingInUse")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "modelProvider.workspace.deleteSupplier" })).toBeEnabled();
    expect(mocks.deleteSupplier).toHaveBeenCalledTimes(1);
  });

  it("offers reset instead of deletion for a built-in supplier", async () => {
    mocks.getProviders.mockResolvedValue({ data: { providers: [{ ...provider, is_custom: false }] } });
    mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [{ ...provider, is_custom: false }] } });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    expect(await screen.findByRole("button", { name: "modelProvider.workspace.resetSupplier" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "modelProvider.workspace.deleteSupplier" })).not.toBeInTheDocument();
  });
});
