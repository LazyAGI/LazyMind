import {
  act,
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
  it("loads models and confirms embedding downgrade", async () => {
      await requestDeletion();
      expect(
        await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc"),
      ).toBeInTheDocument();
      expect(mocks.getModels).toHaveBeenCalledWith({
        modelProviderId: provider.id,
        groupId: group.id,
      });
      expect(mocks.deleteSupplier).not.toHaveBeenCalled();
      fireEvent.click(
        screen.getByRole("button", {
          name: "common.delete",
        }),
      );
      await waitFor(() =>
        expect(mocks.deleteSupplier).toHaveBeenCalledWith(
          provider.id, true,
        ),
      );
    },
  );

  it.each([
    { models: [] },
    { models: [{ id: "chat", name: "fixture-chat", model_type: "llm" }] },
  ])(
    "keeps normal confirmation for non-embedding models $models",
    async ({ models }) => {
      mocks.getModels.mockResolvedValue({ data: { models } });
      await requestDeletion();
      expect(
        await screen.findByText("modelProvider.workspace.deleteSupplierHint"),
      ).toBeInTheDocument();
      expect(mocks.getModels).toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
      await waitFor(() =>
        expect(mocks.deleteSupplier).toHaveBeenCalledWith(
          provider.id, false,
        ),
      );
    },
  );

  it("does not delete when the user cancels the downgrade warning", async () => {
    await requestDeletion();
    expect(
      await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await waitFor(() =>
      expect(
        screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc"),
      ).not.toBeInTheDocument(),
    );
    expect(mocks.deleteSupplier).not.toHaveBeenCalled();
  });

  it("does not offer deletion while model lookup is pending or failed", async () => {
      let rejectLookup!: (error: Error) => void;
      mocks.getModels.mockReturnValue(
        new Promise((_, reject) => {
          rejectLookup = reject;
        }),
      );
      await requestDeletion();
      await waitFor(() => expect(mocks.getModels).toHaveBeenCalled());
      expect(
        screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByText("modelProvider.workspace.deleteSupplierHint"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByText("modelProvider.confirmRemoveProviderDesc"),
      ).not.toBeInTheDocument();
      await act(async () => {
        rejectLookup(new Error("fixture lookup failure"));
      });
      expect(mocks.deleteSupplier).not.toHaveBeenCalled();
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    },
  );

  it("does not offer deleting a built-in supplier", async () => {
    mocks.getProviders.mockResolvedValue({ data: { providers: [{ ...provider, is_custom: false }] } });
    mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [{ ...provider, is_custom: false }] } });
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ModelProviderPage />
      </ConfigProvider>,
    );
    const navigation = await screen.findByRole("navigation", { name: "modelProvider.workspace.suppliers" });
    await within(navigation).findByRole("button", {
      name: "OpenAI",
    });
    expect(
      screen.queryByRole("button", {
        name: "modelProvider.workspace.deleteSupplier",
      }),
    ).not.toBeInTheDocument();
  });

  it.each(["embedding", "embed_main"])(
    "recognizes the backend embedding alias %s",
    async (model_type) => {
      mocks.getModels.mockResolvedValue({
        data: { models: [{ ...embedding, model_type }] },
      });
      await requestDeletion();
      expect(
        await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc"),
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
      await waitFor(() =>
        expect(mocks.deleteSupplier).toHaveBeenCalledWith(
          provider.id, true,
        ),
      );
    },
  );

  it("refreshes previously expanded model data before confirming deletion", async () => {
    mocks.getModels.mockResolvedValueOnce({
      data: { models: [{ id: "chat", name: "Old chat", model_type: "llm" }] },
    });
    render(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ModelProviderPage />
      </ConfigProvider>,
    );
    expect(await screen.findByText("Old chat")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.deleteSupplier",
      }),
    );
    expect(
      await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc"),
    ).toBeInTheDocument();
    expect(mocks.getModels).toHaveBeenCalledTimes(2);
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "common.cancel",
      }),
    );
    expect(mocks.deleteSupplier).not.toHaveBeenCalled();
  });
});
