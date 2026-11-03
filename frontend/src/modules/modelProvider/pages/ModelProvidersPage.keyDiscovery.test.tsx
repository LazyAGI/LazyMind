import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setupAntdStaticCleanup } from "@/test/antdStatic";
import ModelProviderPage, { type AddedProvider, type ProviderOption } from "./ModelProvidersPage";

setupAntdStaticCleanup();

const mocks = vi.hoisted(() => ({
  providers: vi.fn(), withGroups: vi.fn(), groups: vi.fn(), models: vi.fn(),
  remote: vi.fn(), check: vi.fn(), add: vi.fn(), remove: vi.fn(), finished: vi.fn(), failed: vi.fn(), lookup: vi.fn(),
  current: true,
  translate: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key,
}));

vi.mock("react-i18next", () => ({ useTranslation: () => ({ i18n: { language: "zh-CN" }, t: mocks.translate }) }));
vi.mock("../upstreamCatalog", () => ({ lookupUpstreamModel: mocks.lookup }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code, getLocalizedErrorMessage: (error: unknown) => String(error) }));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.providers,
    apiCoreModelProvidersWithGroupsGet: mocks.withGroups,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.groups,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: mocks.models,
    apiCoreModelProvidersModelProviderIdGroupsGroupIdCheckPost: mocks.check,
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
vi.mock("../components/ProviderWorkspace", () => ({ ProviderWorkspace: (props: {
  providers: ProviderOption[];
  configuredProviders: AddedProvider[];
  loading: boolean;
  modelsLoading: Record<string, boolean>;
  onKeySaved?: (provider: AddedProvider, groupId: string, isCurrent: () => boolean) => Promise<void>;
  onSyncModels: (provider: AddedProvider, group: AddedProvider["groups"][number]) => void;
}) => {
  const option = props.providers[0];
  const provider = props.configuredProviders[0] || (option ? { ...option, groups: [] } : undefined);
  const group = provider?.groups[0];
  return <>
    <button disabled={!provider || props.loading} onClick={() => {
      if (provider) void Promise.resolve(props.onKeySaved?.(provider, group?.id || "store", () => mocks.current)).then(mocks.finished, mocks.failed);
    }}>Key saved</button>
    <button disabled={!provider || !group || props.loading} onClick={() => {
      if (provider && group) props.onSyncModels(provider, group);
    }}>Sync models</button>
    <output data-testid="group-models">{JSON.stringify(group?.models || [])}</output>
    <output data-testid="discovery-state">{provider && group && props.modelsLoading[provider.id + ":" + group.id] ? "loading" : "ready"}</output>
  </>;
} }));
vi.mock("@/runtime/cloud/session", () => ({ LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "cloud-changed", getCloudSession: vi.fn().mockResolvedValue({ configured: false, state: "signed_out" }), isCloudBusinessAvailable: () => false, beginCloudLogin: vi.fn() }));
vi.mock("@/runtime/desktopBridge", () => ({ reserveCloudLoginPopup: vi.fn(), closeCloudLoginPopup: vi.fn(), openCloudLogin: vi.fn(), openCloudTokenPlan: vi.fn() }));
vi.mock("../components/CredentialBackupPanel", () => ({ CredentialBackupPanel: () => null }));
vi.mock("../components/CredentialRestorePanel", () => ({ CredentialRestorePanel: () => null }));
vi.mock("../components/CloudSystemProviderCard", () => ({ default: () => null }));

const supplier = { id: "supplier", name: "Gateway", protocol_name: "OpenAI", is_custom: true, base_url: "https://example.test/v1" };
const savedGroup = { id: "store", name: "Gateway", base_url: supplier.base_url, has_api_key: true, is_verified: true, keys: [{ id: "active-key", masked: "****1234", enabled: true }] };
const remoteModels = (count: number) => Array.from({ length: count }, (_, index) => ({ id: `model-${index}`, name: `model-${index}`, model_type: "llm", added: false }));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.current = true;
  mocks.lookup.mockReturnValue(undefined);
  mocks.providers.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.withGroups.mockResolvedValue({ data: { providers: [supplier] } });
  mocks.groups.mockResolvedValue({ data: { groups: [savedGroup] } });
  mocks.models.mockResolvedValue({ data: { models: [] } });
  mocks.remote.mockResolvedValue({ models: [] });
  mocks.check.mockResolvedValue({ data: { success: false, reason: "model_not_found" } });
  mocks.add.mockImplementation(async (request: { addModelProviderGroupModelOpenAPIRequest: { name: string; model_type: string } }) => ({ data: { id: `saved-${request.addModelProviderGroupModelOpenAPIRequest.name}`, ...request.addModelProviderGroupModelOpenAPIRequest, is_default: false } }));
  mocks.remove.mockResolvedValue({ data: {} });
});

async function startDiscoveryAfterKeySave() {
  render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
  const saved = await screen.findByRole("button", { name: "Key saved" });
  await waitFor(() => expect(saved).toBeEnabled());
  await act(async () => { fireEvent.click(saved); });
}

async function discoverAfterKeySave() {
  await startDiscoveryAfterKeySave();
  await waitFor(() => expect(mocks.finished).toHaveBeenCalledTimes(1));
}

describe("model discovery after verified key save", () => {
  it("fetches an unverified keyless custom endpoint without requiring a key or verification", async () => {
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...savedGroup, is_verified: false, has_api_key: false, keys: [] }] } });
    mocks.remote.mockResolvedValue({ models: remoteModels(1) });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    await waitFor(() => expect(mocks.remote).toHaveBeenCalledWith("supplier", "store"));
    expect(screen.queryByText("modelProvider.catalog.verifyBeforeSync")).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:model-0" })).toBeEnabled();
  });

  it.each(["official", "disabled-key"] as const)("does not bypass verification for %s suppliers", async (kind) => {
    const current = { ...supplier, is_custom: kind !== "official" };
    mocks.providers.mockResolvedValue({ data: { providers: [current] } });
    mocks.withGroups.mockResolvedValue({ data: { providers: [current] } });
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...savedGroup, is_verified: false, has_api_key: kind === "disabled-key", keys: kind === "disabled-key" ? [{ ...savedGroup.keys[0], enabled: false }] : [] }] } });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const sync = await screen.findByRole("button", { name: "Sync models" });
    await waitFor(() => expect(sync).toBeEnabled());
    fireEvent.click(sync);
    expect(await screen.findByText("modelProvider.catalog.verifyBeforeSync")).toBeInTheDocument();
    expect(mocks.remote).not.toHaveBeenCalled();
  });

  it("checks a saved unverified active key separately before discovery", async () => {
    let verified = false;
    mocks.groups.mockImplementation(async () => ({ data: { groups: [{ ...savedGroup, is_verified: verified }] } }));
    mocks.check.mockImplementation(async () => {
      verified = true;
      return { data: { success: true } };
    });
    mocks.remote.mockResolvedValue({ models: remoteModels(1) });
    await discoverAfterKeySave();
    expect(mocks.check).toHaveBeenCalledOnce();
    expect(mocks.check).toHaveBeenCalledWith(expect.any(Object), expect.objectContaining({ silentError: true }));
    expect(mocks.remote).toHaveBeenCalledWith("supplier", "store");
    expect(mocks.add).toHaveBeenCalledOnce();
  });

  it("keeps saved model rows usable while the separate connection check is pending", async () => {
    let finish!: (value: { data: { success: boolean; reason: string } }) => void;
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...savedGroup, is_verified: false }] } });
    mocks.check.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await startDiscoveryAfterKeySave();
    await waitFor(() => expect(mocks.check).toHaveBeenCalledOnce());
    expect(screen.getByTestId("discovery-state")).toHaveTextContent("ready");
    await act(async () => finish({ data: { success: false, reason: "model_list_unavailable" } }));
    await waitFor(() => expect(mocks.finished).toHaveBeenCalledOnce());
  });

  it.each(["authentication_failed", "permission_denied", "model_not_found", "quota_exceeded", "rate_limited", "timeout"])("keeps the saved key and explains %s without discovering models", async (reason) => {
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...savedGroup, is_verified: false }] } });
    mocks.check.mockResolvedValue({ data: { success: false, reason } });
    await discoverAfterKeySave();
    expect(mocks.check).toHaveBeenCalledOnce();
    expect(await screen.findByText("modelProvider.probe.reason." + reason)).toBeVisible();
    expect(mocks.remote).not.toHaveBeenCalled();
    expect(mocks.add).not.toHaveBeenCalled();
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(mocks.failed).not.toHaveBeenCalled();
  });

  it("adds catalog-recognized ID-only models using their recognized types", async () => {
    mocks.remote.mockResolvedValue({ models: [
      { id: "gateway/vector", name: "gateway/vector" },
      { id: "gateway/reranker", name: "gateway/reranker" },
    ] });
    mocks.lookup.mockImplementation((_protocol: string, name: string) => ({
      name, model_type: name.endsWith("vector") ? "embed" : "rerank", input: ["text"], output: [], context: 0, max_output: 0,
    }));
    await discoverAfterKeySave();
    expect(mocks.add.mock.calls.map(([request]) => request.addModelProviderGroupModelOpenAPIRequest)).toEqual([
      { name: "gateway/vector", model_type: "embed", vision: false },
      { name: "gateway/reranker", model_type: "rerank", vision: false },
    ]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("preserves explicit and raw-output unknowns while keeping a saved manual type", async () => {
    mocks.models.mockResolvedValue({ data: { models: [{ id: "manual", name: "manual", model_type: "tts", is_default: false }] } });
    mocks.remote.mockResolvedValue({ models: [
      { id: "manual", name: "manual", model_type: "llm" },
      { id: "raw", name: "raw", model_type: "", output: ["decisions"] },
      { id: "unknown", name: "unknown", model_type: "unknown" },
      { id: "future", name: "future", model_type: "future-task", output: [] },
    ] });
    mocks.lookup.mockReturnValue({ name: "known", model_type: "embed", input: ["text"], output: ["embeddings"], context: 0, max_output: 0 });
    await discoverAfterKeySave();
    expect(mocks.add).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toEqual([
      expect.objectContaining({ id: "manual", name: "manual", capability: "TTS", builtIn: false }),
    ]);
  });

  it("opens manual discovery for Core's explicit unclassified result without filling it from the catalog", async () => {
    mocks.remote.mockResolvedValue({ models: [{ id: "qwen38-27b", name: "qwen38-27b", model_type: "", added: false }] });
    mocks.lookup.mockReturnValue({ name: "qwen/qwen3.8-27b", model_type: "llm", input: ["text", "image", "video"], output: ["text"], context: 128000, max_output: 0 });
    await discoverAfterKeySave();
    expect(mocks.add).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.catalog.addModel:qwen38-27b" })).toBeEnabled();
  });

  it("adds models after the first key creates a group that is not yet present in the rendered state", async () => {
    mocks.groups.mockResolvedValueOnce({ data: { groups: [] } });
    mocks.remote.mockResolvedValue({ models: remoteModels(4) });
    render(<ConfigProvider theme={{ token: { motion: false } }}><ModelProviderPage /></ConfigProvider>);
    const saved = await screen.findByRole("button", { name: "Key saved" });
    await waitFor(() => expect(saved).toBeEnabled());
    expect(mocks.groups).toHaveBeenCalledTimes(1);
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toEqual([]);

    mocks.groups.mockResolvedValue({ data: { groups: [savedGroup] } });
    await act(async () => { fireEvent.click(saved); });
    await waitFor(() => expect(mocks.finished).toHaveBeenCalledTimes(1));

    expect(mocks.remote).toHaveBeenCalledWith("supplier", "store");
    expect(mocks.add).toHaveBeenCalledTimes(4);
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toHaveLength(4);
    expect(mocks.failed).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("automatically adds all four available models without opening the catalog", async () => {
    mocks.remote.mockResolvedValue({ models: remoteModels(4) });
    await discoverAfterKeySave();
    expect(mocks.remote).toHaveBeenCalledWith("supplier", "store");
    expect(mocks.add).toHaveBeenCalledTimes(4);
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toHaveLength(4);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mocks.remove).not.toHaveBeenCalled();
  });

  it("opens exactly five available models for selection without adding any automatically", async () => {
    mocks.remote.mockResolvedValue({ models: remoteModels(5) });
    await discoverAfterKeySave();
    expect(await screen.findByRole("dialog")).toHaveClass("provider-model-catalog-drawer");
    expect(mocks.remote).toHaveBeenCalledTimes(1);
    expect(mocks.add).not.toHaveBeenCalled();
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toEqual([]);
  });

  it("deduplicates blank and repeated remote names and preserves already added models", async () => {
    const existing = { id: "existing", name: "model-0", model_type: "llm", is_default: false };
    mocks.models.mockResolvedValue({ data: { models: [existing] } });
    mocks.remote.mockResolvedValue({ models: [
      ...remoteModels(2),
      { id: "duplicate", name: " MODEL-0 ", model_type: "llm", added: true },
      { id: "empty", name: " ", model_type: "llm", added: false },
      { id: "unknown", name: "custom-unknown", model_type: "", added: false },
    ] });
    await discoverAfterKeySave();
    expect(mocks.add).toHaveBeenCalledTimes(1);
    expect(mocks.add.mock.calls.map(([request]) => request.addModelProviderGroupModelOpenAPIRequest.name)).toEqual(["model-1"]);
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toEqual(expect.arrayContaining([expect.objectContaining({ id: "existing", name: "model-0" })]));
    expect(screen.getByRole("dialog")).toHaveClass("provider-model-catalog-drawer");
    expect(mocks.remove).not.toHaveBeenCalled();
  });

  it("does not discover models for a key whose supplier is not verified", async () => {
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...savedGroup, is_verified: false }] } });
    await discoverAfterKeySave();
    expect(mocks.remote).not.toHaveBeenCalled();
    expect(mocks.add).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("does not enable or discover using explicitly disabled keys", async () => {
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...savedGroup, keys: [{ ...savedGroup.keys[0], enabled: false }] }] } });
    await discoverAfterKeySave();
    expect(mocks.remote).not.toHaveBeenCalled();
    expect(mocks.add).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("does not add models or open an old supplier drawer after switching away during discovery", async () => {
    let resolveRemote!: (data: { models: ReturnType<typeof remoteModels> }) => void;
    mocks.remote.mockReturnValue(new Promise((resolve) => { resolveRemote = resolve; }));
    await startDiscoveryAfterKeySave();
    await waitFor(() => expect(mocks.remote).toHaveBeenCalledTimes(1));
    mocks.current = false;
    await act(async () => { resolveRemote({ models: remoteModels(4) }); });
    await waitFor(() => expect(mocks.finished).toHaveBeenCalledTimes(1));
    expect(mocks.add).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("does not reject the successful key-save workflow when remote discovery fails", async () => {
    mocks.remote.mockRejectedValue(new Error("remote discovery unavailable"));
    await discoverAfterKeySave();
    expect(mocks.failed).not.toHaveBeenCalled();
    expect(mocks.add).not.toHaveBeenCalled();
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("modelProvider.catalog.discoveryFailed")).toBeInTheDocument();
  });

  it("leaves an empty remote catalog empty without creating placeholder models", async () => {
    await discoverAfterKeySave();
    expect(mocks.remote).toHaveBeenCalledWith("supplier", "store");
    expect(mocks.add).not.toHaveBeenCalled();
    expect(mocks.remove).not.toHaveBeenCalled();
    expect(JSON.parse(screen.getByTestId("group-models").textContent || "[]")).toEqual([]);
  });
});
