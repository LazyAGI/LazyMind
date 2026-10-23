import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ModelProviderPage, { type AddedProvider, type ProviderConnectionGroup } from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  providers: vi.fn(), withGroups: vi.fn(), groups: vi.fn(), models: vi.fn(), remote: vi.fn(), add: vi.fn(), addBatch: vi.fn(), remove: vi.fn(), update: vi.fn(),
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
  updateGroupModel: mocks.update,
  addGroupModels: mocks.addBatch,
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
  getCredentialBackupStatus: vi.fn(), getCredentialRestoreDiscovery: vi.fn(), getCredentialRestoreOperation: vi.fn(),
  setCredentialBackupEnabled: vi.fn(), startCredentialRestore: vi.fn(), cancelCredentialRestore: vi.fn(),
}));
vi.mock("../components/ProviderWorkspace", () => ({ ProviderWorkspace: (props: { configuredProviders: AddedProvider[]; onSyncModels: (provider: AddedProvider, group: ProviderConnectionGroup) => void; onEditModel: (provider: AddedProvider, group: ProviderConnectionGroup, model: ProviderConnectionGroup["models"][number]) => void }) => {
  const provider = props.configuredProviders[0];
  const group = provider?.groups[0];
  return <><button disabled={!group} onClick={() => props.onSyncModels(provider, group)}>Sync models</button><output aria-label="Configured model count">{group?.modelCount}</output>{group?.models[0] ? <button onClick={() => props.onEditModel(provider, group, group.models[0])}>Edit saved model</button> : null}</>;
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
  it("imports a large catalog in bounded batches and retains the returned saved metadata", async () => {
    const choices = Array.from({ length: 205 }, (_, index) => ({
      id: `model-${index}`, name: `model-${index}`, model_type: index === 204 ? "" : "llm", max_input_tokens: "262144", added: false,
    }));
    mocks.remote.mockResolvedValue({ models: choices });
    mocks.addBatch.mockImplementation(async (_provider: string, _group: string, models: typeof choices) => ({
      models: models.map((model) => ({ ...model, id: `saved-${model.name}`, is_default: false })),
      added_count: models.length, existing_count: 0, failures: [],
    }));
    const changed = vi.fn();
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage onConfigurationChanged={changed} /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    await screen.findByRole("button", { name: "modelProvider.catalog.addModel:model-204" });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    await waitFor(() => expect(mocks.addBatch).toHaveBeenCalledTimes(2));
    expect(mocks.addBatch.mock.calls.map(([, , models]) => models.length)).toEqual([200, 5]);
    expect(mocks.addBatch.mock.calls.every(([provider, group]) => provider === "supplier" && group === "store")).toBe(true);
    expect(mocks.addBatch.mock.calls[1][2]).toContainEqual({ name: "model-204", model_type: "", vision: false, max_input_tokens: "262144" });
    expect(mocks.add).not.toHaveBeenCalled();
    expect(await screen.findByRole("button", { name: "modelProvider.catalog.removeModel:model-204" })).toBeInTheDocument();
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
  }, 20000);

  it("keeps the server's manual type and context while reporting partial batch failures", async () => {
    mocks.groups.mockResolvedValue({ data: { groups: [{ id: "store", name: "Gateway", is_verified: true, model_count: 1000 }] } });
    mocks.models.mockRejectedValueOnce(new Error("saved inventory unavailable"));
    mocks.remote.mockResolvedValue({ models: [
      { id: "manual", name: "manual", model_type: "llm", max_input_tokens: "128K" },
      { id: "fresh", name: "fresh", model_type: "llm" },
      { id: "broken", name: "broken", model_type: "llm" },
    ] });
    mocks.addBatch.mockResolvedValue({
      models: [
        { id: "saved-manual", name: "manual", model_type: "", max_input_tokens: "131073", is_default: false },
        { id: "saved-fresh", name: "fresh", model_type: "llm", max_input_tokens: "128K", is_default: false },
      ],
      added_count: 1, existing_count: 1, total_count: 1001, failures: [{ name: "broken", message: "invalid model" }],
    });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    await screen.findByRole("button", { name: "modelProvider.catalog.addModel:broken" });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    expect(await screen.findByText("modelProvider.catalog.partialFailure")).toBeInTheDocument();
    expect(screen.getByLabelText("Configured model count")).toHaveTextContent("1001");
    const manual = screen.getByRole("button", { name: "modelProvider.catalog.removeModel:manual" }).closest(".provider-model-catalog-row");
    expect(manual?.querySelector('[data-model-type="unknown"]')).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.catalog.addModel:broken" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit saved model" }));
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("131073");
  });

  it.each(["llm", ""])("retains exact vLLM context in the catalog add request for type %j", async (modelType) => {
    mocks.remote.mockResolvedValue({ models: [{ id: "qwen38-27b", name: "qwen38-27b", model_type: modelType, input: ["text", "image", "video"], max_input_tokens: "262144", added: false }] });
    mocks.add.mockResolvedValue({ data: { id: "saved-model-id", name: "qwen38-27b", model_type: modelType, max_input_tokens: "262144", is_default: false } });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:qwen38-27b" }));
    await waitFor(() => expect(mocks.add).toHaveBeenCalledWith({ modelProviderId: "supplier", groupId: "store", addModelProviderGroupModelOpenAPIRequest: { name: "qwen38-27b", model_type: modelType, vision: modelType === "llm", max_input_tokens: "262144" } }));
  });

  it("preserves discovered context while saving an unclassified model and then choosing dialogue", async () => {
    mocks.remote.mockResolvedValue({ models: [{ id: "private-local", name: "private-local", model_type: "", max_input_tokens: "262144", added: false }] });
    mocks.add.mockResolvedValue({ data: { id: "saved-model-id", name: "private-local", model_type: "", max_input_tokens: "262144", is_default: false } });
    mocks.update.mockImplementation(async (_provider: string, _group: string, _model: string, input: object) => ({ id: "saved-model-id", is_default: false, ...input }));
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:private-local" }));
    fireEvent.click(await screen.findByRole("button", { name: "Close" }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit saved model" }));
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("262144");
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.editor.apiModelId" }), { target: { value: "private-renamed" } });
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.editor.save" }));
    await waitFor(() => expect(mocks.update).toHaveBeenCalledWith("supplier", "store", "saved-model-id", expect.objectContaining({ name: "private-renamed", model_type: "", max_input_tokens: "262144" })));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Edit saved model" }));
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "modelProvider.modelType" }));
    fireEvent.click(await screen.findByText("modelProvider.capability.llmChat", { selector: ".model-editor-type-option" }));
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.editor.save" }));
    await waitFor(() => expect(mocks.update).toHaveBeenLastCalledWith("supplier", "store", "saved-model-id", expect.objectContaining({ name: "private-renamed", model_type: "llm", max_input_tokens: "262144" })));
  });

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
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith({ modelProviderId: "supplier", groupId: "store", modelId: "saved-model-id" }, undefined));
    expect(screen.queryByRole("button", { name: "common.delete" })).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:qwen-plus" })).toBeInTheDocument();
  });

  it("directly removes an embedding while retaining the indexed-downgrade request contract", async () => {
    mocks.remote.mockResolvedValue({ models: [{ id: "bge", name: "BAAI/bge-large", model_type: "embed", added: false }] });
    mocks.add.mockResolvedValue({ data: { id: "saved-vector-id", name: "BAAI/bge-large", model_type: "embed", is_default: false } });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:BAAI/bge-large" }));
    const remove = await screen.findByRole("button", { name: "modelProvider.catalog.removeModel:BAAI/bge-large" });
    await waitFor(() => expect(remove).toBeEnabled());
    fireEvent.click(remove);
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith(
      { modelProviderId: "supplier", groupId: "store", modelId: "saved-vector-id" },
      { params: { confirm_indexed_downgrade: true } },
    ));
    expect(screen.queryByRole("button", { name: "common.delete" })).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:BAAI/bge-large" })).toBeInTheDocument();
  });
});
