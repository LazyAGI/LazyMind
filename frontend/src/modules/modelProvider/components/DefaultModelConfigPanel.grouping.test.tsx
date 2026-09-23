import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DefaultModelConfigPanel from "./DefaultModelConfigPanel";

const mocks = vi.hoisted(() => ({
  role: "system-admin",
  imageEmbedEnabled: true,
  cloudAvailable: false,
  providers: vi.fn(),
  selections: vi.fn(),
  services: vi.fn(),
  models: vi.fn(),
  saveModel: vi.fn(),
  saveService: vi.fn(),
  ready: vi.fn(),
  verified: vi.fn(),
  groups: vi.fn(),
  configureProviders: vi.fn(),
  configureService: vi.fn(),
  changed: vi.fn(),
  translate: (key: string) => key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: mocks.translate, i18n: { language: "zh-CN" } }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: mocks.role }) },
}));
vi.mock("@/hooks/useModelFeatures", () => ({
  useModelFeatures: () => ({ status: "ready", features: { image_embed_enabled: mocks.imageEmbedEnabled } }),
}));
vi.mock("@/runtime/features", () => ({ runtimeFeatures: { hideUserGroupSurfaces: false } }));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "cloud-session-changed",
  getCloudSession: async () => ({}),
  isCloudBusinessAvailable: () => mocks.cloudAvailable,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.providers,
    apiCoreModelProvidersSelectedModelsGet: mocks.selections,
    apiCoreModelProvidersSelectedProvidersGet: mocks.services,
    apiCoreModelProvidersModelsGet: mocks.models,
    apiCoreModelProvidersSelectedModelsPut: mocks.saveModel,
    apiCoreModelProvidersSelectedProvidersPut: mocks.saveService,
    apiCoreModelProvidersVerifiedGet: mocks.verified,
    apiCoreModelProvidersProviderGroupsGet: mocks.groups,
  },
  modelProvidersDefaultApi: { apiCoreModelProvidersModelsReadyGet: mocks.ready },
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));

const selectedLlm = {
  model_key: "llm", model_id: "model", name: "Demo model", provider_name: "Demo",
  user_model_provider_id: "provider", user_model_provider_group_id: "group",
  group_name: "Demo group", availability: "available", max_input_tokens: "128K", share: true,
};
const availableModel = {
  id: "model", name: "Demo model", model_type: "llm", provider_name: "Demo",
  user_model_provider_id: "provider", user_model_provider_group_id: "group", group_name: "Demo group",
};
const selectedParsing = {
  category: "ocr", group_id: "parsing", group_name: "Parser", provider_name: "MinerU", share: false,
};

function renderPanel(modelState: "ready" | "empty" | "error" = "ready") {
  return render(<DefaultModelConfigPanel
    cloudServiceSetupStates={{ cloudParsing: "ready", searchEngine: "empty" }}
    modelProviderSetupState={modelState}
    onConfigureCloudService={mocks.configureService}
    onConfigureProviders={mocks.configureProviders}
    onModelSelectionChanged={mocks.changed}
    onRetrySetup={vi.fn()}
  />);
}

const configured = () => screen.getByRole("region", { name: "modelProvider.configuredCapabilities" });
const pending = () => screen.getByRole("region", { name: "modelProvider.pendingCapabilities" });
const group = (name: string) => screen.getByRole("group", { name });
const readyToSelect = async (name: string) => {
  const select = await within(group(name)).findByRole("combobox");
  await waitFor(() => expect(select).toBeEnabled());
  return select;
};
const configure = (name: string) => fireEvent.click(within(group(name)).getByRole("button", { name: "modelProvider.configureCapability" }));

describe("default capability configuration groups", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.role = "system-admin";
    mocks.imageEmbedEnabled = true;
    mocks.cloudAvailable = false;
    mocks.providers.mockResolvedValue({ data: { providers: [{ id: "provider", name: "Demo" }] } });
    mocks.selections.mockResolvedValue({ data: { selections: [selectedLlm] } });
    mocks.services.mockResolvedValue({ data: { selections: [selectedParsing] } });
    mocks.models.mockResolvedValue({ data: { models: [availableModel] } });
    mocks.saveModel.mockResolvedValue({ data: { selections: [selectedLlm] } });
    mocks.saveService.mockResolvedValue({ data: { selections: [] } });
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    mocks.verified.mockResolvedValue({ data: { ready: false } });
    mocks.groups.mockResolvedValue({ data: { groups: [] } });
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("groups configured models and services above all remaining capabilities and preserves sharing", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    expect(within(configured()).getAllByRole("group")).toHaveLength(2);
    expect(within(pending()).getAllByRole("group")).toHaveLength(10);
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.llmChatTitle" })).toBeInTheDocument();
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.cloudParsingServiceTitle" })).toBeInTheDocument();
    expect(within(group("modelProvider.module.llmChatTitle")).getByRole("switch")).toBeChecked();
    await waitFor(() => expect(within(pending()).getAllByRole("combobox")).toHaveLength(9));
    expect(screen.getByRole("heading", { name: "modelProvider.pendingCapabilities 10" })).toBeInTheDocument();
  });

  it("shows selectors directly and only links capabilities without usable models to setup", async () => {
    mocks.models.mockImplementation(({ modelType }: { modelType: string }) => Promise.resolve({
      data: { models: modelType === "reranker" ? [] : [{ ...availableModel,
        availability: modelType === "stt" ? "unavailable" : "available",
        lifecycle: modelType === "text2video" ? "retired" : "active",
      }] },
    }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    expect(await readyToSelect("modelProvider.module.ttsTitle")).toBeInTheDocument();
    expect(within(group("modelProvider.module.ttsTitle")).queryByRole("button", { name: "modelProvider.configureCapability" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "modelProvider.configureAllCapabilities" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "modelProvider.collapseCapabilityConfiguration" })).not.toBeInTheDocument();
    for (const title of ["modelProvider.module.rerankTitle", "modelProvider.module.asrTitle", "modelProvider.module.textToVideoTitle"]) {
      const action = await within(group(title)).findByRole("button", { name: "modelProvider.configureCapability" });
      expect(within(group(title)).queryByRole("combobox")).not.toBeInTheDocument();
      fireEvent.click(action);
    }
    expect(mocks.configureProviders).toHaveBeenCalledTimes(3);
    configure("modelProvider.module.searchEngineServiceTitle");
    expect(mocks.configureService).toHaveBeenCalledWith("searchEngine");
  });

  it("shows the full model name when hovering a dropdown option without selecting it", async () => {
    const modelName = "tongyi-embedding-vision-plus-2026-03-06";
    mocks.models.mockResolvedValue({ data: { models: [{ ...availableModel, name: modelName }] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    fireEvent.mouseDown(await readyToSelect("modelProvider.module.ttsTitle"));
    const optionName = await screen.findByText(modelName);
    fireEvent.mouseEnter(optionName);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent(`${modelName} · Demo group`);
    expect(mocks.saveModel).not.toHaveBeenCalled();
    fireEvent.mouseLeave(optionName);
    await waitFor(() => expect(tooltip.closest(".ant-tooltip")).toHaveClass("ant-tooltip-hidden"), { timeout: 2500 });
  });

  it("keeps the selector visible when a search has no matches", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    const select = await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(select);
    fireEvent.change(select, { target: { value: "no matching model" } });
    await within(group("modelProvider.module.ttsTitle")).findByRole("combobox");
    expect(within(group("modelProvider.module.ttsTitle")).queryByRole("button", { name: "modelProvider.configureCapability" })).not.toBeInTheDocument();
  });

  it("shows retry on a model list failure and does not treat it as an empty capability", async () => {
    mocks.models.mockRejectedValue(new Error("catalog unavailable"));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    const card = group("modelProvider.module.ttsTitle");
    expect(await within(card).findByRole("alert")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "modelProvider.configureCapability" })).not.toBeInTheDocument();
    mocks.models.mockResolvedValue({ data: { models: [availableModel] } });
    fireEvent.click(within(card).getByRole("button", { name: "common.retry" }));
    expect(await readyToSelect("modelProvider.module.ttsTitle")).toBeInTheDocument();
  });

  it("moves a capability only after a successful save, and clearing it returns it to pending", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    let resolveSave!: (value: unknown) => void;
    mocks.saveModel.mockImplementation(() => new Promise(resolve => { resolveSave = resolve; }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalled());
    expect(within(pending()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
    await act(async () => { resolveSave({ data: { selections: [{ ...selectedLlm, model_key: "tts" }] } }); });
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
    mocks.saveModel.mockResolvedValue({ data: { selections: [] } });
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByLabelText("close-circle"));
    await waitFor(() => expect(within(pending()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument());
  });

  describe.each(["model", "service"] as const)("ordinary user %s selection readiness", (kind) => {
    const title = kind === "model" ? "modelProvider.module.ttsTitle" : "modelProvider.module.cloudParsingServiceTitle";
    const oldName = "Previous own configuration";
    const readiness = () => kind === "model" ? mocks.ready : mocks.verified;
    const save = () => kind === "model" ? mocks.saveModel : mocks.saveService;

    beforeEach(() => {
      mocks.role = "user";
      mocks.selections.mockResolvedValue({ data: { selections: kind === "model" ? [{ ...selectedLlm, model_key: "tts" }] : [] } });
      mocks.services.mockResolvedValue({ data: { selections: kind === "service" ? [selectedParsing] : [] } });
      mocks.saveModel.mockResolvedValue({ data: { selections: [] } });
      mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) => Promise.resolve({
        data: { ready: kind === "model" && params.model_type === "tts", source: "own", model_name: oldName },
      }));
      mocks.verified.mockImplementation(({ category }: { category: string }) => Promise.resolve({
        data: { ready: kind === "service" && category === "ocr", source: "own", group_name: oldName },
      }));
      mocks.groups.mockResolvedValue({ data: { groups: [selectedParsing] } });
    });

    it.each([false, true])("rechecks readiness after clearing, with shared fallback=%s", async (hasFallback) => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
      expect(within(configured()).getAllByRole("group")).toHaveLength(1);
      expect(within(group(title)).getByLabelText("check-circle")).toBeInTheDocument();
      const calls = readiness().mock.calls.length;
      let resolveReadiness!: (value: unknown) => void;
      readiness().mockImplementationOnce(() => new Promise(resolve => { resolveReadiness = resolve; }));
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(readiness()).toHaveBeenCalledTimes(calls + 1));
      expect(within(group(title)).getByRole("combobox")).toBeDisabled();
      expect(within(group(title)).queryByLabelText("modelProvider.readyStatusAria")).not.toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
      await act(async () => {
        resolveReadiness({ data: { ready: hasFallback, source: hasFallback ? "shared" : undefined,
          model_name: hasFallback ? "Shared fallback" : undefined, group_name: hasFallback ? "Shared fallback" : undefined } });
      });
      await readyToSelect(title);
      expect(within(hasFallback ? configured() : pending()).getByRole("group", { name: title })).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: `modelProvider.configuredCapabilities ${hasFallback ? 1 : 0}` })).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: `modelProvider.pendingCapabilities ${hasFallback ? 11 : 12}` })).toBeInTheDocument();
      expect(within(group(title)).getByLabelText(hasFallback ? "check-circle" : "minus-circle")).toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
      if (hasFallback) expect(within(group(title)).getByText("Shared fallback")).toBeInTheDocument();
      expect(mocks.selections).toHaveBeenCalledOnce();
      expect(mocks.services).toHaveBeenCalledOnce();
    });

    it("shows a recoverable error when the post-save readiness check fails", async () => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
      readiness().mockRejectedValueOnce(new Error("readiness unavailable"));
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(within(group(title)).getByRole("alert")).toHaveTextContent("modelProvider.capabilityStatusLoadFailed"));
      expect(within(group(title)).queryByLabelText("check-circle")).not.toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
      await readyToSelect(title);
      readiness().mockResolvedValueOnce({ data: { ready: true, source: "shared", model_name: "Recovered fallback", group_name: "Recovered fallback" } });
      fireEvent.click(within(group(title)).getByRole("button", { name: "common.retry" }));
      await waitFor(() => expect(within(configured()).getByRole("group", { name: title })).toHaveTextContent("Recovered fallback"));
      expect(save()).toHaveBeenCalledOnce();
    });

    it("preserves the current readiness when clearing fails to save", async () => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
      const calls = readiness().mock.calls.length;
      save().mockRejectedValueOnce(new Error("save unavailable"));
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(save()).toHaveBeenCalledOnce());
      await readyToSelect(title);
      expect(readiness()).toHaveBeenCalledTimes(calls);
      expect(within(configured()).getByRole("group", { name: title })).toBeInTheDocument();
      expect(within(group(title)).getByLabelText("check-circle")).toBeInTheDocument();
    });

    it("ignores a pre-save readiness retry that finishes after the saved configuration is checked", async () => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
      readiness().mockRejectedValue(new Error("refresh unavailable"));
      act(() => window.dispatchEvent(new Event("focus")));
      await waitFor(() => expect(within(group(title)).getByRole("alert")).toBeInTheDocument());
      let resolveOldRetry!: (value: unknown) => void;
      readiness().mockImplementationOnce(() => new Promise(resolve => { resolveOldRetry = resolve; }));
      fireEvent.click(within(group(title)).getByRole("button", { name: "common.retry" }));
      readiness().mockResolvedValueOnce({ data: { ready: false } });
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(within(pending()).getByRole("group", { name: title })).toBeInTheDocument());
      await readyToSelect(title);
      await act(async () => {
        resolveOldRetry({ data: { ready: true, source: "own", model_name: oldName, group_name: oldName } });
      });
      expect(within(pending()).getByRole("group", { name: title })).toBeInTheDocument();
      expect(within(group(title)).getByLabelText("minus-circle")).toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
    });
  });

  it("keeps failed saves pending", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.saveModel.mockRejectedValue(new Error("save failed"));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    await readyToSelect("modelProvider.module.llmChatTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.llmChatTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalled());
    expect(within(pending()).getByRole("group", { name: "modelProvider.module.llmChatTitle" })).toBeInTheDocument();
    expect(mocks.changed).not.toHaveBeenCalled();
  });

  it("does not classify a selection filtered out by server validation as configured", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.saveModel.mockResolvedValue({ data: { selections: [] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(mocks.changed).toHaveBeenCalled());
    expect(within(pending()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
  });

  it("retains a service on failed removal and moves it after successful removal and configuration", async () => {
    mocks.saveService.mockRejectedValueOnce(new Error("save failed"));
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...selectedParsing, base_url: "", user_model_provider_id: "parser-provider" }] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    const title = "modelProvider.module.cloudParsingServiceTitle";
    fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
    await waitFor(() => expect(mocks.saveService).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(group(title)).getByRole("combobox")).toBeEnabled());
    expect(within(configured()).getByRole("group", { name: title })).toBeInTheDocument();
    fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
    await waitFor(() => expect(within(pending()).getByRole("group", { name: title })).toBeInTheDocument());
    mocks.saveService.mockResolvedValue({ data: { selections: [selectedParsing] } });
    await readyToSelect(title);
    fireEvent.mouseDown(within(group(title)).getByRole("combobox"));
    fireEvent.click(await screen.findByText("MinerU"));
    await waitFor(() => expect(within(configured()).getByRole("group", { name: title })).toBeInTheDocument());
    expect(within(group(title)).getByRole("combobox")).toBeInTheDocument();
  });

  it("does not let an older refresh revert a newly saved selection", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    let resolveRefresh!: (value: unknown) => void;
    mocks.selections.mockImplementationOnce(() => new Promise(resolve => { resolveRefresh = resolve; }));
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(mocks.selections).toHaveBeenCalledTimes(2));
    mocks.saveModel.mockResolvedValue({ data: { selections: [{ ...selectedLlm, model_key: "tts" }] } });
    await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument());
    await act(async () => { resolveRefresh({ data: { selections: [] } }); });
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
  });

  it("ignores a stale model catalog after refreshed configuration has no usable models", async () => {
    let resolveCatalog!: (value: unknown) => void;
    mocks.models.mockImplementation(({ modelType }: { modelType: string }) => modelType === "tts"
      ? new Promise(resolve => { resolveCatalog = resolve; })
      : Promise.resolve({ data: { models: [availableModel] } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    await waitFor(() => expect(mocks.models).toHaveBeenCalledWith({ modelType: "tts" }));
    const card = group("modelProvider.module.ttsTitle");
    expect(within(card).getByRole("status")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "modelProvider.configureCapability" })).not.toBeInTheDocument();
    mocks.models.mockResolvedValue({ data: { models: [] } });
    act(() => window.dispatchEvent(new Event("focus")));
    expect(await within(card).findByRole("button", { name: "modelProvider.configureCapability" })).toBeInTheDocument();
    await act(async () => { resolveCatalog({ data: { models: [availableModel] } }); });
    expect(within(card).queryByRole("combobox")).not.toBeInTheDocument();
    expect(within(card).getByRole("button", { name: "modelProvider.configureCapability" })).toBeInTheDocument();
  });

  it("refreshes deleted and unavailable selections when returning to the page", async () => {
    mocks.cloudAvailable = true;
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    mocks.selections.mockResolvedValue({ data: { selections: [{ ...selectedLlm, availability: "unavailable" }] } });
    mocks.models.mockResolvedValue({ data: { models: [{ ...availableModel, availability: "unavailable" }] } });
    mocks.services.mockResolvedValue({ data: { selections: [] } });
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(within(configured()).queryByRole("group")).not.toBeInTheDocument());
    expect(within(pending()).getAllByRole("group")).toHaveLength(12);
  });

  it("preserves shared capability readiness and administrator-only configuration", async () => {
    mocks.role = "user";
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) => Promise.resolve({
      data: { ready: params.model_type === "embed_main", source: "shared", provider_name: "Shared provider", model_name: "Shared embedding", shared_by_name: "Admin" },
    }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.embeddingTitle" })).toBeInTheDocument();
    expect(await within(group("modelProvider.module.multimodalEmbeddingTitle")).findByRole("combobox")).toBeDisabled();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("excludes disabled features and sends empty provider/service entries to their setup flow", async () => {
    mocks.imageEmbedEnabled = false;
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.services.mockResolvedValue({ data: { selections: [] } });
    renderPanel("empty");
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    expect(within(pending()).getAllByRole("group")).toHaveLength(11);
    configure("modelProvider.module.llmChatTitle");
    expect(mocks.configureProviders).toHaveBeenCalledOnce();
    configure("modelProvider.module.searchEngineServiceTitle");
    expect(mocks.configureService).toHaveBeenCalledWith("searchEngine");
    fireEvent.click(await within(group("modelProvider.module.cloudParsingServiceTitle")).findByRole("button", { name: "modelProvider.configureCapability" }));
    expect(mocks.configureService).toHaveBeenCalledWith("cloudParsing");
  });

  it("shows a retry state instead of labelling failed selection loads as unconfigured", async () => {
    mocks.selections.mockRejectedValue(new Error("load failed"));
    renderPanel();
    expect(await screen.findByRole("alert")).toHaveTextContent("modelProvider.defaultConfigLoadFailed");
    expect(screen.queryByRole("region", { name: "modelProvider.pendingCapabilities" })).not.toBeInTheDocument();
    mocks.selections.mockResolvedValue({ data: { selections: [selectedLlm] } });
    fireEvent.click(screen.getByRole("button", { name: "common.retry" }));
    expect(await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" })).toBeInTheDocument();
    expect(within(configured()).getAllByRole("group")).toHaveLength(2);
  });

  it("isolates failed model readiness and retries only that capability without losing selections", async () => {
    mocks.role = "user";
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) =>
      params.model_type === "tts" ? Promise.reject(new Error("upstream unavailable")) : Promise.resolve({ data: { ready: false } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.llmChatTitle" })).toHaveTextContent("Demo model");
    const card = group("modelProvider.module.ttsTitle");
    expect(await within(card).findByRole("alert")).toHaveTextContent("modelProvider.capabilityStatusLoadFailed");
    expect(screen.queryByText("modelProvider.defaultConfigLoadFailed")).not.toBeInTheDocument();
    const calls = mocks.ready.mock.calls.length;
    const verifiedCalls = mocks.verified.mock.calls.length;
    let resolveRetry!: (value: unknown) => void;
    mocks.ready.mockImplementationOnce(() => new Promise(resolve => { resolveRetry = resolve; }));
    const retry = within(card).getByRole("button", { name: "common.retry" });
    fireEvent.click(retry);
    fireEvent.click(retry);
    expect(retry).toBeDisabled();
    expect(mocks.ready).toHaveBeenCalledTimes(calls + 1);
    expect(mocks.ready).toHaveBeenLastCalledWith(expect.objectContaining({ params: { model_type: "tts" } }));
    await act(async () => { resolveRetry({ data: { ready: true, source: "shared", model_name: "Shared speech" } }); });
    await waitFor(() => expect(within(group("modelProvider.module.ttsTitle")).queryByRole("alert")).not.toBeInTheDocument());
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
    expect(mocks.selections).toHaveBeenCalledOnce();
    expect(mocks.verified).toHaveBeenCalledTimes(verifiedCalls);
    expect(mocks.saveModel).not.toHaveBeenCalled();
  });

  it("isolates service readiness failures and keeps a failed retry recoverable", async () => {
    mocks.role = "user";
    mocks.verified.mockImplementation(({ category }: { category: string }) =>
      category === "ocr" ? Promise.reject(new Error("unavailable")) : Promise.resolve({ data: { ready: false } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    const card = group("modelProvider.module.cloudParsingServiceTitle");
    expect(await within(card).findByRole("alert")).toHaveTextContent("modelProvider.capabilityStatusLoadFailed");
    fireEvent.click(within(card).getByRole("button", { name: "common.retry" }));
    await waitFor(() => expect(within(card).getByRole("button", { name: "common.retry" })).toBeEnabled());
    expect(within(card).getByRole("alert")).toBeInTheDocument();
    mocks.verified.mockResolvedValueOnce({ data: { ready: true, source: "own" } });
    fireEvent.click(within(card).getByRole("button", { name: "common.retry" }));
    await waitFor(() => expect(within(card).queryByRole("alert")).not.toBeInTheDocument());
    expect(mocks.verified).toHaveBeenLastCalledWith({ category: "ocr" }, expect.objectContaining({ silentError: true }));
    expect(mocks.saveService).not.toHaveBeenCalled();
  });

  it("ignores a readiness retry that finishes after a newer refresh", async () => {
    mocks.role = "user";
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) =>
      params.model_type === "tts" ? Promise.reject(new Error("unavailable")) : Promise.resolve({ data: { ready: false } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    const card = group("modelProvider.module.ttsTitle");
    let resolveRetry!: (value: unknown) => void;
    mocks.ready.mockImplementationOnce(() => new Promise(resolve => { resolveRetry = resolve; }));
    fireEvent.click(within(card).getByRole("button", { name: "common.retry" }));
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(within(card).queryByRole("alert")).not.toBeInTheDocument());
    await act(async () => { resolveRetry({ data: { ready: true, source: "shared" } }); });
    expect(within(pending()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
  });

  it("preserves the last shared configuration while reporting a failed refresh as unknown", async () => {
    mocks.role = "user";
    mocks.ready.mockResolvedValue({ data: { ready: true, source: "shared", model_name: "Shared speech" } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    mocks.ready.mockRejectedValue(new Error("unavailable"));
    act(() => window.dispatchEvent(new Event("focus")));
    const card = within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" });
    expect(await within(card).findByRole("alert")).toHaveTextContent("modelProvider.capabilityStatusLoadFailed");
    expect(within(card).queryByLabelText("modelProvider.readyStatusAria")).not.toBeInTheDocument();
    expect(within(card).getByText("Shared speech")).toBeInTheDocument();
  });

  it("does not let an older background refresh overwrite a successful manual retry", async () => {
    mocks.role = "user";
    mocks.ready.mockRejectedValue(new Error("unavailable"));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.pendingCapabilities" });
    let resolveRefresh!: (value: unknown) => void;
    mocks.selections.mockImplementationOnce(() => new Promise(resolve => { resolveRefresh = resolve; }));
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(mocks.selections).toHaveBeenCalledTimes(2));
    mocks.ready.mockResolvedValueOnce({ data: { ready: true, source: "shared" } });
    fireEvent.click(within(group("modelProvider.module.ttsTitle")).getByRole("button", { name: "common.retry" }));
    await waitFor(() => expect(within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument());
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    await act(async () => { resolveRefresh({ data: { selections: [selectedLlm] } }); });
    expect(within(configured()).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
  });

  it("does not request readiness for a disabled feature", async () => {
    mocks.role = "user";
    mocks.imageEmbedEnabled = false;
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.configuredCapabilities" });
    expect(mocks.ready.mock.calls.some(([options]) => options.params.model_type === "embed_image")).toBe(false);
    expect(screen.queryByRole("group", { name: "modelProvider.module.multimodalEmbeddingTitle" })).not.toBeInTheDocument();
  });
});
