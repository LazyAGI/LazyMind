import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ModelProviderPage from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(), getProvidersWithGroups: vi.fn(), getGroups: vi.fn(),
  getModels: vi.fn(), deleteGroup: vi.fn(),
  translate: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdDelete: mocks.deleteGroup,
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

const provider = { id: "fixture-provider", name: "OpenAI", base_url: "https://api.example.test/v1" };
const group = { id: "fixture-group", name: "Fixture", base_url: provider.base_url, is_verified: true };
const embedding = { id: "fixture-embed", name: "fixture-embedding", model_type: "embed" };

async function requestDeletion(scope: "group" | "provider") {
  render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
  const name = scope === "group" ? "modelProvider.deleteGroupAria:Fixture" : "modelProvider.removeProviderAria:OpenAI";
  fireEvent.click(await screen.findByRole("button", { name }));
}

beforeEach(() => {
  vi.resetAllMocks();
  mocks.getProviders.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [provider] } });
  mocks.getGroups.mockResolvedValue({ data: { groups: [group] } });
  mocks.getModels.mockResolvedValue({ data: { models: [embedding] } });
  mocks.deleteGroup.mockResolvedValue({ data: {} });
});

describe("provider deletion before expanding models", () => {
  it.each(["group", "provider"] as const)("loads models and confirms embedding downgrade for %s", async (scope) => {
    await requestDeletion(scope);
    expect(await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc")).toBeInTheDocument();
    expect(mocks.getModels).toHaveBeenCalledWith({ modelProviderId: provider.id, groupId: group.id });
    expect(mocks.deleteGroup).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: scope === "group" ? "common.delete" : "modelProvider.remove" }));
    await waitFor(() => expect(mocks.deleteGroup).toHaveBeenCalledWith(
      { modelProviderId: provider.id, groupId: group.id }, { params: { confirm_indexed_downgrade: true } },
    ));
  });

  it.each([{ models: [] }, { models: [{ id: "chat", name: "fixture-chat", model_type: "llm" }] }])("keeps normal confirmation for non-embedding models $models", async ({ models }) => {
    mocks.getModels.mockResolvedValue({ data: { models } });
    await requestDeletion("group");
    expect(await screen.findByText("modelProvider.confirmDeleteGroupDesc")).toBeInTheDocument();
    expect(mocks.getModels).toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(mocks.deleteGroup).toHaveBeenCalledWith(
      { modelProviderId: provider.id, groupId: group.id }, undefined,
    ));
  });

  it("does not delete when the user cancels the downgrade warning", async () => {
    await requestDeletion("group");
    expect(await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await waitFor(() => expect(screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc")).not.toBeInTheDocument());
    expect(mocks.deleteGroup).not.toHaveBeenCalled();
  });

  it.each(["group", "provider"] as const)("does not offer deletion while model lookup is pending or failed for %s", async (scope) => {
    let rejectLookup!: (error: Error) => void;
    mocks.getModels.mockReturnValue(new Promise((_, reject) => { rejectLookup = reject; }));
    await requestDeletion(scope);
    await waitFor(() => expect(mocks.getModels).toHaveBeenCalled());
    expect(screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc")).not.toBeInTheDocument();
    expect(screen.queryByText("modelProvider.confirmDeleteGroupDesc")).not.toBeInTheDocument();
    expect(screen.queryByText("modelProvider.confirmRemoveProviderDesc")).not.toBeInTheDocument();
    await act(async () => { rejectLookup(new Error("fixture lookup failure")); });
    expect(mocks.deleteGroup).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("checks all provider groups and only confirms downgrade for embedding groups", async () => {
    const secondGroup = { ...group, id: "fixture-group-2", name: "Second" };
    mocks.getGroups.mockResolvedValue({ data: { groups: [group, secondGroup] } });
    mocks.getModels.mockImplementation(async ({ groupId }) => ({ data: { models: groupId === group.id ? [] : [embedding] } }));
    await requestDeletion("provider");
    expect(await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc")).toBeInTheDocument();
    expect(mocks.getModels).toHaveBeenCalledTimes(2);
    expect(mocks.deleteGroup).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.remove" }));
    await waitFor(() => expect(mocks.deleteGroup).toHaveBeenCalledTimes(2));
    expect(mocks.deleteGroup).toHaveBeenCalledWith({ modelProviderId: provider.id, groupId: group.id }, undefined);
    expect(mocks.deleteGroup).toHaveBeenCalledWith({ modelProviderId: provider.id, groupId: secondGroup.id }, { params: { confirm_indexed_downgrade: true } });
  });

  it("does not partially delete a provider when one group lookup fails", async () => {
    mocks.getGroups.mockResolvedValue({ data: { groups: [group, { ...group, id: "second", name: "Second" }] } });
    mocks.getModels.mockImplementation(async ({ groupId }) => {
      if (groupId === "second") throw new Error("fixture lookup failure");
      return { data: { models: [embedding] } };
    });
    await requestDeletion("provider");
    await waitFor(() => expect(mocks.getModels).toHaveBeenCalledTimes(2));
    expect(mocks.deleteGroup).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it.each(["embedding", "embed_main"])("recognizes the backend embedding alias %s", async (model_type) => {
    mocks.getModels.mockResolvedValue({ data: { models: [{ ...embedding, model_type }] } });
    await requestDeletion("group");
    expect(await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(mocks.deleteGroup).toHaveBeenCalledWith(
      { modelProviderId: provider.id, groupId: group.id }, { params: { confirm_indexed_downgrade: true } },
    ));
  });

  it("refreshes previously expanded model data before confirming deletion", async () => {
    mocks.getModels.mockResolvedValueOnce({ data: { models: [{ id: "chat", name: "Old chat", model_type: "llm" }] } });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    fireEvent.click(await screen.findByRole("button", { name: /modelProvider.expandModels/ }));
    expect(await screen.findByText("Old chat")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.deleteGroupAria:Fixture" }));
    expect(await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc")).toBeInTheDocument();
    expect(mocks.getModels).toHaveBeenCalledTimes(2);
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "common.cancel" }));
    expect(mocks.deleteGroup).not.toHaveBeenCalled();
  });
});
