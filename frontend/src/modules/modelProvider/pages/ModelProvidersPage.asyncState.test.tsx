import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, message } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ModelProviderPage, {
  type AddedProvider,
  type ProviderConnectionGroup,
  type ProviderModel,
} from "./ModelProvidersPage";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getProvidersWithGroups: vi.fn(),
  getGroups: vi.fn(),
  getModels: vi.fn(),
  addModel: vi.fn(),
  editModel: vi.fn(),
  deleteModel: vi.fn(),
  translate: (key: string, options?: { name?: string }) =>
    options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }),
}));
vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
  getLocalizedErrorMessage: (error: unknown) =>
    `localized:${error instanceof Error ? error.message : String(error)}`,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersWithGroupsGet: mocks.getProvidersWithGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.getGroups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.getModels,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsPost: mocks.addModel,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdDelete: mocks.deleteModel,
  },
  modelProvidersDefaultApi: {},
  updateGroupModel: mocks.editModel,
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
  getCloudSession: vi.fn().mockResolvedValue({ configured: false, state: "signed_out" }),
  isCloudBusinessAvailable: () => false,
  beginCloudLogin: vi.fn(),
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: vi.fn(),
  closeCloudLoginPopup: vi.fn(),
  openCloudLogin: vi.fn(),
  openCloudTokenPlan: vi.fn(),
}));
vi.mock("../components/CredentialBackupPanel", () => ({ CredentialBackupPanel: () => null }));
vi.mock("../components/CredentialRestorePanel", () => ({ CredentialRestorePanel: () => null }));
vi.mock("../components/CloudSystemProviderCard", () => ({ default: () => null }));

interface WorkspaceProps {
  configuredProviders: AddedProvider[];
  modelsError?: Record<string, string>;
  modelsLoading: Record<string, boolean>;
  onLoadModels: (provider: AddedProvider, group: ProviderConnectionGroup) => void;
  onEditModel: (provider: AddedProvider, group: ProviderConnectionGroup, model: ProviderModel) => void;
  onDeleteModel: (provider: AddedProvider, group: ProviderConnectionGroup, model: ProviderModel) => void;
  onAddModel: (provider: AddedProvider, group: ProviderConnectionGroup) => void;
}

vi.mock("../components/ProviderWorkspace", () => ({
  ProviderWorkspace: (props: WorkspaceProps) => {
    const provider = props.configuredProviders[0];
    const group = provider?.groups[0];
    const key = provider && group ? `${provider.id}:${group.id}` : "";
    const error = props.modelsError?.[key];
    return <>
      <button disabled={!group || props.modelsLoading[key]} onClick={() => props.onLoadModels(provider, group)}>Load models</button>
      {error ? <div role="alert">{error}<button onClick={() => props.onLoadModels(provider, group)}>Retry models</button></div> : null}
      <button disabled={!group?.models[0]} onClick={() => props.onEditModel(provider, group, group.models[0])}>Edit first model</button>
      <button disabled={!group?.models[0]} onClick={() => props.onDeleteModel(provider, group, group.models[0])}>Delete first model</button>
      <button disabled={!group} onClick={() => props.onAddModel(provider, group)}>Add custom model</button>
      <output data-testid="group">{JSON.stringify(group)}</output>
      <output data-testid="model-load-state">{props.modelsLoading[key] ? "loading" : "ready"}</output>
    </>;
  },
}));

const editedValues = {
  name: "edited-vision",
  capability: "VLM" as const,
  maxInputTokens: "200000",
};

vi.mock("../components/ModelEditorModal", () => ({
  ModelEditorModal: (props: {
    model: ProviderModel | null;
    onSave: (values: typeof editedValues) => Promise<void>;
    onClose: () => void;
  }) => props.model ? <button onClick={async () => {
    await props.onSave(editedValues);
    props.onClose();
  }}>Save model edits</button> : null,
}));

const supplier = { id: "supplier", name: "OpenAI", protocol_name: "OpenAI", base_url: "https://example.test/v1" };
const initialGroup = {
  id: "group", name: "Supplier", base_url: supplier.base_url,
  has_api_key: true, is_verified: true, keys: [],
};
const originalModels = [{
  id: "chat", name: "original-chat", model_type: "llm", is_default: true,
  vision: false, max_input_tokens: "8192",
}];
const createdModel = {
  id: "created", name: "manual-model", model_type: "llm", is_default: false,
  max_input_tokens: "128K",
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

const currentGroup = (): ProviderConnectionGroup =>
  JSON.parse(screen.getByTestId("group").textContent || "null");

function draw() {
  render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
}

async function loadInitialModels() {
  const load = await screen.findByRole("button", { name: "Load models" });
  await waitFor(() => expect(load).toBeEnabled());
  fireEvent.click(load);
  await waitFor(() => expect(currentGroup().models).toHaveLength(1));
  await waitFor(() => expect(screen.getByTestId("model-load-state")).toHaveTextContent("ready"));
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getProviders.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.getProvidersWithGroups.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.getGroups.mockResolvedValue({ data: { groups: [initialGroup] } });
  mocks.getModels.mockResolvedValue({ data: { models: originalModels } });
  mocks.addModel.mockResolvedValue({ data: createdModel });
  mocks.editModel.mockResolvedValue({
    id: "chat", name: editedValues.name, model_type: "vlm", is_default: false,
    vision: true, max_input_tokens: editedValues.maxInputTokens,
  });
  mocks.deleteModel.mockResolvedValue({ data: {} });
});

afterEach(() => {
  cleanup();
  act(() => { message.destroy(); });
});

describe("model provider asynchronous state", () => {
  it("retains cached models on a localized load error and clears the error after retry", async () => {
    draw();
    await loadInitialModels();
    const cached = currentGroup().models;
    mocks.getModels.mockRejectedValueOnce(new Error("Model list unavailable"));
    fireEvent.click(screen.getByRole("button", { name: "Load models" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("localized:Model list unavailable");
    expect(currentGroup().models).toEqual(cached);
    mocks.getModels.mockResolvedValueOnce({ data: { models: [{ ...originalModels[0], name: "reloaded-chat" }] } });
    fireEvent.click(screen.getByRole("button", { name: "Retry models" }));
    await waitFor(() => expect(currentGroup().models[0].name).toBe("reloaded-chat"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(mocks.getModels).toHaveBeenCalledTimes(3);
  });

  it("does not let a slow GET roll back a successful model edit", async () => {
    draw();
    await loadInitialModels();
    const slow = deferred<{ data: { models: typeof originalModels } }>();
    mocks.getModels.mockReturnValueOnce(slow.promise);
    fireEvent.click(screen.getByRole("button", { name: "Edit first model" }));
    fireEvent.click(screen.getByRole("button", { name: "Load models" }));
    await waitFor(() => expect(mocks.getModels).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole("button", { name: "Save model edits" }));
    await waitFor(() => expect(currentGroup().models[0]).toMatchObject({
      id: "chat", name: "edited-vision", capability: "VLM", builtIn: false,
      vision: true, maxInputTokens: "200000",
    }));
    expect(mocks.editModel).toHaveBeenCalledWith("supplier", "group", "chat", {
      name: "edited-vision", model_type: "vlm", vision: false,
      max_input_tokens: "200000", customize: true,
    });
    const edited = currentGroup().models;
    await act(async () => slow.resolve({ data: { models: originalModels } }));
    await waitFor(() => expect(screen.getByTestId("model-load-state")).toHaveTextContent("ready"));
    expect(currentGroup().models).toEqual(edited);
  });

  it("retains a failed custom-model draft, blocks duplicate POSTs, and preserves the added model against an older GET", async () => {
    draw();
    await loadInitialModels();
    const slow = deferred<{ data: { models: typeof originalModels } }>();
    mocks.getModels.mockReturnValueOnce(slow.promise);
    fireEvent.click(screen.getByRole("button", { name: "Load models" }));
    await waitFor(() => expect(mocks.getModels).toHaveBeenCalledTimes(2));
    const first = deferred<{ data: typeof createdModel }>();
    const second = deferred<{ data: typeof createdModel }>();
    mocks.addModel.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    fireEvent.click(screen.getByRole("button", { name: "Add custom model" }));
    const dialogElement = await screen.findByRole("dialog");
    const dialog = within(dialogElement);
    const name = dialog.getByPlaceholderText("modelProvider.modelNamePlaceholder");
    fireEvent.change(name, { target: { value: "manual-model" } });
    const add = dialog.getByRole("button", { name: "modelProvider.add" });
    act(() => { fireEvent.click(add); fireEvent.click(add); });
    await waitFor(() => expect(mocks.addModel).toHaveBeenCalledOnce());
    expect(name).toBeDisabled();
    await act(async () => first.reject(new Error("Create unavailable")));
    expect(await screen.findByText("localized:Create unavailable")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBe(dialogElement);
    expect(name).toHaveValue("manual-model");
    await waitFor(() => expect(name).toBeEnabled());
    act(() => { fireEvent.click(add); fireEvent.click(add); });
    await waitFor(() => expect(mocks.addModel).toHaveBeenCalledTimes(2));
    await act(async () => second.resolve({ data: createdModel }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(currentGroup().models.map((model) => model.id)).toEqual(["chat", "created"]);
    await act(async () => slow.resolve({ data: { models: originalModels } }));
    await waitFor(() => expect(screen.getByTestId("model-load-state")).toHaveTextContent("ready"));
    expect(currentGroup().models.find((model) => model.id === "created")).toMatchObject({
      name: "manual-model", capability: "LLM_CHAT", builtIn: false, maxInputTokens: "128K",
    });
  });

  it("does not resurrect a deleted model when a pre-deletion GET completes", async () => {
    draw();
    await loadInitialModels();
    const slow = deferred<{ data: { models: typeof originalModels } }>();
    mocks.getModels.mockReturnValueOnce(slow.promise);
    fireEvent.click(screen.getByRole("button", { name: "Load models" }));
    await waitFor(() => expect(mocks.getModels).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole("button", { name: "Delete first model" }));
    await waitFor(() => expect(currentGroup().models).toEqual([]));
    expect(mocks.deleteModel).toHaveBeenCalledWith({ modelProviderId: "supplier", groupId: "group", modelId: "chat" }, undefined);
    await act(async () => slow.resolve({ data: { models: originalModels } }));
    await waitFor(() => expect(screen.getByTestId("model-load-state")).toHaveTextContent("ready"));
    expect(currentGroup().models).toEqual([]);
  });

  it("keeps a model on failed direct deletion and sends one request during repeated clicks", async () => {
    draw();
    await loadInitialModels();
    const pending = deferred<{ data: object }>();
    mocks.deleteModel.mockReturnValueOnce(pending.promise).mockResolvedValueOnce({ data: {} });
    const remove = screen.getByRole("button", { name: "Delete first model" });
    act(() => { fireEvent.click(remove); fireEvent.click(remove); });
    await waitFor(() => expect(mocks.deleteModel).toHaveBeenCalledTimes(1));
    expect(currentGroup().models).toHaveLength(1);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await act(async () => pending.reject(new Error("Delete unavailable")));
    expect(await screen.findByText("localized:Delete unavailable")).toBeInTheDocument();
    expect(currentGroup().models).toHaveLength(1);
    fireEvent.click(remove);
    await waitFor(() => expect(mocks.deleteModel).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(currentGroup().models).toEqual([]));
  });
});
