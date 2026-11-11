import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Modal } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
  previewEmbeddingSwitch: vi.fn(),
  saveService: vi.fn(),
  ready: vi.fn(),
  verified: vi.fn(),
  groups: vi.fn(),
  configureProviders: vi.fn(),
  configureService: vi.fn(),
  changed: vi.fn(),
  translate: (key: string, options?: { configured?: number; total?: number }) =>
    key === "modelProvider.defaults.configuredCount"
      ? `${options?.configured}/${options?.total}`
      : key,
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
  previewEmbeddingSwitch: mocks.previewEmbeddingSwitch,
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
  model_key: "llm", model_id: "model", name: "Demo model", provider_name: "SiliconFlow",
  user_model_provider_id: "provider", user_model_provider_group_id: "group",
  group_name: "Demo group", availability: "available", max_input_tokens: "128K", share: true,
};
const availableModel = {
  id: "model", name: "Demo model", model_type: "llm", provider_name: "SiliconFlow",
  user_model_provider_id: "provider", user_model_provider_group_id: "group", group_name: "Demo group",
};
const selectedParsing = {
  category: "ocr", group_id: "parsing", group_name: "Parser", provider_name: "MinerU", share: false,
};
const selectedEmbedding = {
  ...selectedLlm, model_key: "embed_main", model_id: "old-embedding",
  name: "Old embedding", share: false,
};
const candidateEmbedding = {
  ...availableModel, id: "new-embedding", name: "New embedding", model_type: "embed_main",
};

function renderPanel(
  modelState: "ready" | "empty" | "error" = "ready",
  highlightTarget?: "image_generator" | "evo_llm",
  onHighlightResolved = vi.fn(),
) {
  return render(<DefaultModelConfigPanel
    cloudServiceSetupStates={{ cloudParsing: "ready", searchEngine: "empty" }}
    modelProviderSetupState={modelState}
    onConfigureCloudService={mocks.configureService}
    onConfigureProviders={mocks.configureProviders}
    onModelSelectionChanged={mocks.changed}
    onRetrySetup={vi.fn()}
    highlightTarget={highlightTarget}
    onHighlightResolved={onHighlightResolved}
  />);
}

const basic = () => screen.getByRole("region", { name: "modelProvider.defaults.basicTitle" });
const categoryNames = ["visual", "retrieval", "voice", "evolution"] as const;
type CategoryName = typeof categoryNames[number];
const categoryButton = (category: CategoryName) => screen.getByRole("button", {
  name: new RegExp(`modelProvider.defaults.${category}Title`),
});
const expandCategory = (category: CategoryName) => {
  const button = categoryButton(category);
  if (button.getAttribute("aria-expanded") !== "true") fireEvent.click(button);
  return screen.getByRole("region", { name: `modelProvider.defaults.${category}Title` });
};
const categoryByTitle: Record<string, CategoryName> = {
  "modelProvider.module.vlmTitle": "visual",
  "modelProvider.module.textToImageTitle": "visual",
  "modelProvider.module.textToVideoTitle": "visual",
  "modelProvider.module.asrTitle": "voice",
  "modelProvider.module.ttsTitle": "voice",
  "modelProvider.module.multimodalEmbeddingTitle": "retrieval",
  "modelProvider.module.rerankTitle": "retrieval",
  "modelProvider.module.cloudParsingServiceTitle": "retrieval",
  "modelProvider.module.searchEngineServiceTitle": "retrieval",
  "modelProvider.module.selfEvolutionTitle": "evolution",
};
const group = (name: string) => {
  const category = categoryByTitle[name];
  if (category) expandCategory(category);
  return screen.getByRole("group", { name });
};
const revealAllCategories = () => categoryNames.forEach(expandCategory);
const readyToSelect = async (name: string) => {
  const select = await within(group(name)).findByRole("combobox");
  await waitFor(() => expect(select).toBeEnabled());
  return select;
};
const configure = (name: string) => fireEvent.click(within(group(name)).getByRole("button", { name: "modelProvider.configureCapability" }));
const chooseCandidateEmbedding = async () => {
  await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
  const select = await readyToSelect("modelProvider.module.embeddingTitle");
  fireEvent.mouseDown(select);
  fireEvent.click(await screen.findByText("New embedding", { selector: "strong" }));
};

describe("default capability configuration groups", () => {
  afterEach(() => vi.restoreAllMocks());
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.role = "system-admin";
    mocks.imageEmbedEnabled = true;
    mocks.cloudAvailable = false;
    mocks.providers.mockResolvedValue({ data: { providers: [{ id: "provider", name: "Demo", protocol_name: "SiliconFlow", is_custom: true }] } });
    mocks.selections.mockResolvedValue({ data: { selections: [selectedLlm] } });
    mocks.services.mockResolvedValue({ data: { selections: [selectedParsing] } });
    mocks.models.mockResolvedValue({ data: { models: [availableModel] } });
    mocks.saveModel.mockResolvedValue({ data: { selections: [selectedLlm] } });
    mocks.previewEmbeddingSwitch.mockResolvedValue({
      outcome: "empty", can_switch: true, indexed_knowledge_bases: 0,
      active_model_name: "", candidate_model_name: "New embedding",
      signature_status: "none", generation: 1,
    });
    mocks.saveService.mockResolvedValue({ data: { selections: [] } });
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    mocks.verified.mockResolvedValue({ data: { ready: false } });
    mocks.groups.mockResolvedValue({ data: { groups: [] } });
    Element.prototype.scrollIntoView = vi.fn();
  });

  it.each([true, false])("keeps the unavailable saved embedding when the catalog contains the same model (saved provider IDs: %s)", async (hasProviderIds) => {
    mocks.selections.mockResolvedValue({ data: { selections: [{
      ...selectedEmbedding, availability: "unavailable", unavailable_reason: "index_identity_unverified",
      group_name: "Demo",
      user_model_provider_id: hasProviderIds ? "provider" : undefined,
      user_model_provider_group_id: hasProviderIds ? "group" : undefined,
    }] } });
    mocks.models.mockResolvedValue({ data: { models: [{ ...candidateEmbedding, id: "old-embedding", name: "Catalog embedding" }] } });
    renderPanel();

    const row = await screen.findByRole("group", { name: "modelProvider.module.embeddingTitle" });
    const select = await within(row).findByRole("combobox");
    expect(within(row).getByText("Old embedding · Demo")).toBeInTheDocument();
    expect(within(row).getByRole("alert")).toHaveTextContent("index_identity_unverified");
    expect(row).not.toHaveClass("is-pending");
    fireEvent.mouseDown(select);
    await waitFor(() => expect(screen.getByText("Old embedding", { selector: "strong" })
      .closest(".ant-select-item-option")).toHaveClass("ant-select-item-option-disabled"));
    expect(screen.queryByText("Catalog embedding")).not.toBeInTheDocument();
    expect(within(row).getByText("Old embedding · Demo")).toBeInTheDocument();
  });

  it("keeps an embedding confirmed by the save response when the index makes it unavailable", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    mocks.saveModel.mockResolvedValue({ data: { selections: [{
      ...selectedEmbedding, model_id: "new-embedding", name: "Saved embedding", availability: "unavailable",
      unavailable_reason: "index_cleanup_incomplete",
    }] } });
    renderPanel();

    await chooseCandidateEmbedding();

    const row = group("modelProvider.module.embeddingTitle");
    expect(await within(row).findByText("Saved embedding · Demo")).toBeInTheDocument();
    expect(within(row).getByRole("alert")).toHaveTextContent("index_cleanup_incomplete");
    expect(row).not.toHaveClass("is-pending");
    expect(within(row).queryByText("Old embedding · Demo")).not.toBeInTheDocument();
  });

  it.each(["empty", "error"] as const)("retains a saved embedding when provider setup is %s", async (setupState) => {
    mocks.selections.mockResolvedValue({ data: { selections: [{
      ...selectedEmbedding, availability: "unavailable", unavailable_reason: "credentials_unavailable",
    }] } });
    mocks.models.mockResolvedValue({ data: { models: [] } });
    renderPanel(setupState);

    const row = await screen.findByRole("group", { name: "modelProvider.module.embeddingTitle" });
    expect(within(row).getByRole("combobox")).toBeDisabled();
    expect(within(row).getByText("Old embedding · Demo")).toBeInTheDocument();
    expect(within(row).getByText("credentials_unavailable")).toBeInTheDocument();
    expect(row).not.toHaveClass("is-pending");
  });

  it.each(["providers", "services"] as const)("shows the saved embedding while %s never resolves", async (part) => {
    mocks[part].mockImplementation(() => new Promise(() => {}));
    mocks.selections.mockResolvedValue({ data: { selections: [{
      ...selectedEmbedding, group_name: "Saved supplier", availability: "unavailable", unavailable_reason: "index_cleared",
    }] } });
    renderPanel();

    const supplier = part === "providers" ? "Saved supplier" : "Demo";
    expect(await screen.findByText(`Old embedding · ${supplier}`)).toBeInTheDocument();
    const row = group("modelProvider.module.embeddingTitle");
    expect(within(row).getByText("index_cleared")).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.defaultConfigLoadFailed")).not.toBeInTheDocument();
  });

  it("keeps initial selection loading when the provider catalog arrives first", async () => {
    let resolveSelections!: (value: unknown) => void;
    mocks.selections.mockImplementation(() => new Promise((resolve) => { resolveSelections = resolve; }));
    renderPanel();

    await waitFor(() => expect(resolveSelections).toBeTypeOf("function"));
    expect(screen.getByText("common.loading")).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.defaultConfigLoadFailed")).not.toBeInTheDocument();
    await act(async () => { resolveSelections({ data: { selections: [selectedEmbedding] } }); });
    expect(await screen.findByText("Old embedding · Demo")).toBeInTheDocument();
  });

  it("preserves the server's available selection while its catalog metadata is pending", async () => {
    mocks.providers.mockImplementation(() => new Promise(() => {}));
    mocks.selections.mockResolvedValue({ data: { selections: [{
      ...selectedEmbedding, provider_name: "Unlisted gateway", group_name: "Saved supplier",
    }] } });
    renderPanel();

    expect(await screen.findByText("Old embedding · Saved supplier")).toBeInTheDocument();
    expect(within(group("modelProvider.module.embeddingTitle")).queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each(["providers", "services"] as const)("retains a successful selection when %s fails", async (part) => {
    mocks[part].mockRejectedValue(new Error("initial request failed"));
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    renderPanel();

    const supplier = part === "providers" ? "Demo group" : "Demo";
    expect(await screen.findByText(`Old embedding · ${supplier}`)).toBeInTheDocument();
    expect(screen.getByText("modelProvider.defaultConfigLoadFailed")).toBeInTheDocument();
  });

  it("shows the selection and options without a failure warning during readiness checks", async () => {
    mocks.role = "user";
    mocks.ready.mockImplementation(() => new Promise(() => {}));
    mocks.verified.mockImplementation(() => new Promise(() => {}));
    renderPanel();

    expect(await screen.findByText("Demo model · Demo")).toBeInTheDocument();
    await readyToSelect("modelProvider.module.ttsTitle");
    expect(screen.queryByText("modelProvider.capabilityStatusLoadFailed")).not.toBeInTheDocument();
  });

  it("checks the embedding signature and asks for confirmation before a compatible switch", async () => {
    const confirm = vi.spyOn(Modal, "confirm").mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() }));
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    mocks.previewEmbeddingSwitch.mockResolvedValue({
      outcome: "same", can_switch: true, indexed_knowledge_bases: 2,
      active_model_name: "Old embedding", candidate_model_name: "New embedding",
      signature_status: "verified", generation: 2,
    });
    renderPanel();

    await chooseCandidateEmbedding();

    await waitFor(() => expect(mocks.previewEmbeddingSwitch).toHaveBeenCalledWith({
      model_id: "new-embedding", source: "own",
    }));
    expect(mocks.saveModel).not.toHaveBeenCalled();
    await waitFor(() => expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
      content: "modelProvider.embeddingSignatureMatched",
      okText: "modelProvider.confirmSwitch",
    })));
    await act(async () => { (confirm.mock.calls[0][0].onOk as () => void)(); });
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalledOnce());
  });

  it("does not switch to an incompatible embedding while index cleanup is unavailable", async () => {
    const warning = vi.spyOn(Modal, "warning").mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() }));
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    mocks.previewEmbeddingSwitch.mockResolvedValue({
      outcome: "clear_required", can_switch: false, indexed_knowledge_bases: 2,
      active_model_name: "Old embedding", candidate_model_name: "New embedding",
      signature_status: "verified", generation: 2,
    });
    renderPanel();

    await chooseCandidateEmbedding();

    await waitFor(() => expect(warning).toHaveBeenCalledWith(expect.objectContaining({
      content: "modelProvider.embeddingIndexResetRequired",
    })));
    expect(mocks.saveModel).not.toHaveBeenCalled();
    expect(group("modelProvider.module.embeddingTitle")).toHaveTextContent("Old embedding");
  });

  it("keeps the old embedding when the candidate signature cannot be checked", async () => {
    const warning = vi.spyOn(Modal, "warning").mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() }));
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    mocks.previewEmbeddingSwitch.mockResolvedValue({
      outcome: "probe_failed", can_switch: false, indexed_knowledge_bases: 2,
      active_model_name: "Old embedding", candidate_model_name: "New embedding",
      signature_status: "unknown", generation: 2,
    });
    renderPanel();

    await chooseCandidateEmbedding();

    await waitFor(() => expect(warning).toHaveBeenCalledWith(expect.objectContaining({
      content: "modelProvider.embeddingSignatureCheckFailed",
    })));
    expect(mocks.saveModel).not.toHaveBeenCalled();
  });

  it("honors a server refusal even when the preview reports a matching signature", async () => {
    const warning = vi.spyOn(Modal, "warning").mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() }));
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    mocks.previewEmbeddingSwitch.mockResolvedValue({
      outcome: "same", can_switch: false, indexed_knowledge_bases: 2,
      active_model_name: "Old embedding", candidate_model_name: "New embedding",
      signature_status: "verified", generation: 2,
    });
    renderPanel();

    await chooseCandidateEmbedding();

    await waitFor(() => expect(warning).toHaveBeenCalledWith(expect.objectContaining({
      content: "modelProvider.embeddingSignatureCheckFailed",
    })));
    expect(mocks.saveModel).not.toHaveBeenCalled();
  });

  it("checks the candidate before selecting an embedding when no index exists", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    renderPanel();

    await chooseCandidateEmbedding();

    await waitFor(() => expect(mocks.previewEmbeddingSwitch).toHaveBeenCalledWith({
      model_id: "new-embedding", source: "own",
    }));
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalledOnce());
  });

  it("fails closed when the embedding switch preview request fails", async () => {
    const warning = vi.spyOn(Modal, "warning").mockImplementation(() => ({ destroy: vi.fn(), update: vi.fn() }));
    mocks.selections.mockResolvedValue({ data: { selections: [selectedEmbedding] } });
    mocks.models.mockResolvedValue({ data: { models: [candidateEmbedding] } });
    mocks.previewEmbeddingSwitch.mockRejectedValue(new Error("probe unavailable"));
    renderPanel();

    await chooseCandidateEmbedding();

    await waitFor(() => expect(warning).toHaveBeenCalledWith(expect.objectContaining({
      content: "modelProvider.embeddingSignatureCheckFailed",
    })));
    expect(mocks.saveModel).not.toHaveBeenCalled();
    await waitFor(() => expect(within(group("modelProvider.module.embeddingTitle")).getByRole("combobox")).toBeEnabled());
  });

  it("shows the four tool services in the requested order and opens their configuration", async () => {
    const configureTool = vi.fn();
    render(<DefaultModelConfigPanel
      cloudServiceSetupStates={{ cloudParsing: "ready", searchEngine: "ready" }}
      modelProviderSetupState="ready"
      onConfigureCloudService={mocks.configureService}
      onConfigureToolService={configureTool}
      toolServiceSummaries={{ translation: "Google Translate Web" }}
      onConfigureProviders={mocks.configureProviders}
      onModelSelectionChanged={mocks.changed}
      onRetrySetup={vi.fn()}
    />);
    const retrieval = await screen.findByRole("region", { name: "modelProvider.defaults.retrievalTitle" });
    const rows = within(retrieval).getAllByRole("group");
    expect(rows.slice(0, 4).map((row) => row.getAttribute("aria-label")))
      .toEqual(["搜索引擎", "学术搜索引擎", "文档解析（OCR）", "机器翻译"]);
    for (const [index, category] of ["search", "academic", "parsing", "translation"].entries()) {
      fireEvent.click(within(rows[index]).getByRole("button", { name: "modelProvider.configureCapability" }));
      expect(configureTool).toHaveBeenLastCalledWith(category);
    }
    expect(within(rows[3]).getByText("Google Translate Web")).toBeInTheDocument();
  });

  it("keeps context length inside the selected model control, without a metadata line", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [{ ...selectedLlm, max_input_tokens: "1000000" }] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const row = group("modelProvider.module.llmChatTitle");
    const badge = await within(row).findByText("1M");
    expect(badge.closest(".ant-select")).not.toBeNull();
    expect(row.querySelector(".model-provider-default-meta")).not.toHaveTextContent("1M");
    expect(mocks.saveModel).not.toHaveBeenCalled();
  });

  it("opens configuration for an already configured external service", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const row = group("modelProvider.module.cloudParsingServiceTitle");
    await within(row).findByRole("combobox");
    fireEvent.click(within(row).getByRole("button", { name: "modelProvider.configureCapability" }));
    expect(mocks.configureService).toHaveBeenCalledWith("cloudParsing");
  });

  it("groups models by supplier, supports collapsing, and keeps the selected label", async () => {
    mocks.providers.mockResolvedValue({ data: { providers: [
      { id: "provider", name: "Demo", protocol_name: "SiliconFlow", is_custom: true },
      { id: "other", name: "Other", protocol_name: "SiliconFlow", is_custom: true },
    ] } });
    mocks.models.mockResolvedValue({ data: { models: [
      { ...availableModel, is_default: true },
      { ...availableModel, id: "other-model", name: "Other model", user_model_provider_id: "other", user_model_provider_group_id: "other-group" },
    ] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const select = await readyToSelect("modelProvider.module.llmChatTitle");
    fireEvent.mouseDown(select);
    const supplier = await screen.findByRole("button", { name: /^Demo\s*1$/ });
    expect(supplier).toHaveAttribute("aria-expanded", "true");
    expect(screen.queryByText("modelProvider.builtInModelSuffix")).not.toBeInTheDocument();
    expect(screen.queryByText("modelProvider.customModelSuffix")).not.toBeInTheDocument();
    fireEvent.click(supplier);
    expect(supplier).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Demo model", { selector: "strong" })).not.toBeInTheDocument();
    expect(within(group("modelProvider.module.llmChatTitle")).getByText("Demo model · Demo")).toBeInTheDocument();
    expect(screen.getByText("Other model", { selector: "strong" })).toBeInTheDocument();
    fireEvent.click(screen.getByText("Other model", { selector: "strong" }));
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalled());
    expect(mocks.saveModel.mock.calls[0][0].setSelectedModelsOpenAPIRequest.selections).toEqual(expect.arrayContaining([
      expect.objectContaining({ model_key: "llm", model_id: "other-model" }),
    ]));
  });

  it("loads a configured model list on first open and reuses it when reopened", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const select = await readyToSelect("modelProvider.module.llmChatTitle");
    const llmRequests = () => mocks.models.mock.calls.filter(([params]) => params.modelType === "llm");
    expect(llmRequests()).toHaveLength(0);

    fireEvent.mouseDown(select);
    await waitFor(() => expect(llmRequests()).toHaveLength(1));
    await screen.findByText("Demo model", { selector: "strong" });
    fireEvent.mouseDown(document.body);
    await waitFor(() => expect(select).toHaveAttribute("aria-expanded", "false"));
    fireEvent.mouseDown(select);
    await waitFor(() => expect(select).toHaveAttribute("aria-expanded", "true"));
    expect(llmRequests()).toHaveLength(1);
  });

  it("does not refetch a model list that was loaded before opening the selector", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const select = await readyToSelect("modelProvider.module.ttsTitle");
    const ttsRequests = () => mocks.models.mock.calls.filter(([params]) => params.modelType === "tts");
    await waitFor(() => expect(ttsRequests()).toHaveLength(1));

    fireEvent.mouseDown(select);
    expect(ttsRequests()).toHaveLength(1);
  });

  it("reveals collapsed supplier models when searching", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const select = await readyToSelect("modelProvider.module.llmChatTitle");
    fireEvent.mouseDown(select);
    fireEvent.click(await screen.findByRole("button", { name: /^Demo\s*1$/ }));
    expect(screen.queryByText("Demo model", { selector: "strong" })).not.toBeInTheDocument();
    fireEvent.change(select, { target: { value: "Demo model" } });
    expect(await screen.findByText("Demo model", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Demo\s*1$/ })).toHaveAttribute("aria-expanded", "true");
  });

  it("shows two basic selectors and expands every available category on entry", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    await waitFor(() => expect(screen.getAllByRole("combobox")).toHaveLength(11));
    const basic = screen.getByRole("region", { name: "modelProvider.defaults.basicTitle" });
    expect(within(basic).getAllByRole("group")).toHaveLength(2);
    expect(within(basic).getByRole("group", { name: "modelProvider.module.llmChatTitle" })).toBeInTheDocument();
    expect(within(basic).getByRole("group", { name: "modelProvider.module.embeddingTitle" })).toBeInTheDocument();
    const headings = [...document.querySelectorAll(".model-defaults-group-heading")].map((heading) => heading.textContent);
    expect(headings).toEqual([
      expect.stringContaining("modelProvider.defaults.visualTitle"),
      expect.stringContaining("modelProvider.defaults.retrievalTitle"),
      expect.stringContaining("modelProvider.defaults.voiceTitle"),
      expect.stringContaining("modelProvider.defaults.evolutionTitle"),
    ]);
    for (const category of ["visual", "retrieval", "voice", "evolution"]) {
      expect(screen.getByRole("button", { name: new RegExp(`modelProvider.defaults.${category}Title`) })).toHaveAttribute("aria-expanded", "true");
      expect(screen.getByRole("region", { name: `modelProvider.defaults.${category}Title` })).toBeInTheDocument();
    }
    const retrievalRows = screen
      .getByRole("region", { name: "modelProvider.defaults.retrievalTitle" })
      .querySelectorAll(".model-provider-default-row");
    expect([...retrievalRows].map((row) => row.getAttribute("aria-label"))).toEqual([
      "modelProvider.module.searchEngineServiceTitle",
      "modelProvider.module.cloudParsingServiceTitle",
      "modelProvider.module.rerankTitle",
      "modelProvider.module.multimodalEmbeddingTitle",
    ]);
    expect(screen.getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
  });

  it("shows custom supplier identity without its private storage group", async () => {
    mocks.providers.mockResolvedValue({data:{providers:[{id:"provider",name:"My gateway",protocol_name:"OpenAI",is_custom:true}]}});
    mocks.selections.mockResolvedValue({data:{selections:[{...selectedLlm,provider_name:"OpenAI",group_name:"Legacy connection"}]}});
    mocks.models.mockResolvedValue({data:{models:[{...availableModel,provider_name:"OpenAI",group_name:"Legacy connection"}]}});
    renderPanel();
    await screen.findByRole("region", {name:"modelProvider.defaults.basicTitle"});
    const select = await readyToSelect("modelProvider.module.llmChatTitle");
    expect(await screen.findByText("Demo model · My gateway")).toBeInTheDocument();
    expect(within(group("modelProvider.module.llmChatTitle")).getByText("Demo model · My gateway").closest(".ant-select-selection-item")?.querySelector("img")).toHaveAttribute("src", "/provider-icons/custom.svg");
    fireEvent.mouseDown(select);
    await waitFor(()=>expect(document.querySelector(".model-provider-select-option small")).toHaveTextContent("My gateway"));
    expect(document.querySelector(".model-provider-select-option")).toHaveClass("is-child");
    expect(screen.queryByText(/Legacy connection/)).not.toBeInTheDocument();
    expect(screen.queryByText("My gateway / My gateway")).not.toBeInTheDocument();
  });

  it("inherits an uploaded custom supplier logo without per-model artwork", async () => {
    const logo = "data:image/png;base64,supplier-logo";
    mocks.providers.mockResolvedValue({data:{providers:[{id:"provider",name:"My gateway",protocol_name:"OpenAI",is_custom:true,logo_data_url:logo}]}});
    renderPanel();
    await screen.findByRole("region", {name:"modelProvider.defaults.basicTitle"});
    const select = await readyToSelect("modelProvider.module.llmChatTitle");
    const selected = await screen.findByText("Demo model · My gateway");
    expect(selected.closest(".ant-select-selection-item")?.querySelector("img")).toHaveAttribute("src", logo);
    fireEvent.mouseDown(select);
    await waitFor(() => expect(document.querySelector(".model-provider-select-option img")).toHaveAttribute("src", logo));
  });

  it("uses the canonical supplier identity for a shared model outside the personal catalog", async () => {
    mocks.providers.mockResolvedValue({data:{providers:[]}});
    mocks.selections.mockResolvedValue({data:{selections:[{...selectedLlm,provider_name:"OpenAI",group_name:"Team gateway"}]}});
    mocks.models.mockResolvedValue({data:{models:[{...availableModel,provider_name:"OpenAI",group_name:"Team gateway"}]}});
    renderPanel();
    await screen.findByRole("region", {name:"modelProvider.defaults.basicTitle"});
    await readyToSelect("modelProvider.module.llmChatTitle");
    expect(await screen.findByText("Demo model · Team gateway")).toBeInTheDocument();
    expect(within(group("modelProvider.module.llmChatTitle")).getByText("Demo model · Team gateway").closest(".ant-select-selection-item")?.querySelector("img")).toHaveAttribute("src", "/provider-icons/custom.svg");
    expect(screen.queryByText("Demo model · OpenAI")).not.toBeInTheDocument();
  });

  it("keeps every category visible by default and supports collapsing one category", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const voice = expandCategory("voice");
    expect(categoryButton("voice")).toHaveAttribute("aria-expanded", "true");
    expect(within(voice).getAllByRole("group")).toHaveLength(2);
    expect(within(voice).getByRole("group", { name: "modelProvider.module.ttsTitle" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "modelProvider.module.textToImageTitle" })).toBeInTheDocument();
    fireEvent.click(categoryButton("voice"));
    expect(categoryButton("voice")).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("region", { name: "modelProvider.defaults.voiceTitle" })).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "modelProvider.module.textToImageTitle" })).toBeInTheDocument();
  });

  it("reopens a manually collapsed group when a new highlight targets it", async () => {
    const { rerender } = renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    fireEvent.click(categoryButton("voice"));
    expect(categoryButton("voice")).toHaveAttribute("aria-expanded", "false");

    rerender(<DefaultModelConfigPanel
      cloudServiceSetupStates={{ cloudParsing: "ready", searchEngine: "empty" }}
      modelProviderSetupState="ready"
      onConfigureCloudService={mocks.configureService}
      onConfigureProviders={mocks.configureProviders}
      onModelSelectionChanged={mocks.changed}
      onRetrySetup={vi.fn()}
      highlightTarget="tts"
    />);

    const voice = await screen.findByRole("region", { name: "modelProvider.defaults.voiceTitle" });
    expect(categoryButton("voice")).toHaveAttribute("aria-expanded", "true");
    const target = within(voice).getByRole("group", { name: "modelProvider.module.ttsTitle" });
    expect(target).toHaveClass("is-config-highlighted");
    await waitFor(() => expect(target).toHaveFocus());
  });

  it("automatically reveals and focuses a deep-linked capability in a collapsed category", async () => {
    const resolved = vi.fn();
    renderPanel("ready", "image_generator", resolved);
    const visual = await screen.findByRole("region", { name: "modelProvider.defaults.visualTitle" });
    const target = within(visual).getByRole("group", { name: "modelProvider.module.textToImageTitle" });
    expect(categoryButton("visual")).toHaveAttribute("aria-expanded", "true");
    expect(categoryButton("voice")).toHaveAttribute("aria-expanded", "true");
    expect(target).toHaveClass("is-config-highlighted");
    await waitFor(() => expect(target).toHaveFocus());
    expect(target.scrollIntoView).not.toHaveBeenCalled();
    expect(resolved).not.toHaveBeenCalled();
  });

  it("keeps configured and pending models in stable categories and preserves sharing", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    expect(within(basic()).getByRole("group", { name: "modelProvider.module.llmChatTitle" })).not.toHaveClass("is-pending");
    expect(within(basic()).getByRole("group", { name: "modelProvider.module.embeddingTitle" })).toHaveClass("is-pending");
    const retrieval = expandCategory("retrieval");
    expect(within(retrieval).getByRole("group", { name: "modelProvider.module.cloudParsingServiceTitle" })).not.toHaveClass("is-pending");
    expect(within(retrieval).getByRole("group", { name: "modelProvider.module.rerankTitle" })).toHaveClass("is-pending");
    expect(within(group("modelProvider.module.llmChatTitle")).getByRole("switch")).toBeChecked();
    revealAllCategories();
    await waitFor(() => expect(screen.getAllByRole("combobox")).toHaveLength(11));
    expect(screen.getAllByRole("group")).toHaveLength(12);
  });

  it("shows selectors directly and only links capabilities without usable models to setup", async () => {
    mocks.models.mockImplementation(({ modelType }: { modelType: string }) => Promise.resolve({
      data: { models: modelType === "reranker" ? [] : [{ ...availableModel,
        availability: modelType === "stt" ? "unavailable" : "available",
        lifecycle: modelType === "text2video" ? "retired" : "active",
      }] },
    }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
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
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    fireEvent.mouseDown(await readyToSelect("modelProvider.module.ttsTitle"));
    const optionName = await screen.findByText(modelName);
    fireEvent.mouseEnter(optionName);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent(`${modelName} · Demo`);
    expect(mocks.saveModel).not.toHaveBeenCalled();
    fireEvent.mouseLeave(optionName);
    await waitFor(() => expect(tooltip.closest(".ant-tooltip")).toHaveClass("ant-tooltip-hidden"), { timeout: 2500 });
  });

  it("keeps the selector visible when a search has no matches", async () => {
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const select = await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(select);
    fireEvent.change(select, { target: { value: "no matching model" } });
    await within(group("modelProvider.module.ttsTitle")).findByRole("combobox");
    expect(within(group("modelProvider.module.ttsTitle")).queryByRole("button", { name: "modelProvider.configureCapability" })).not.toBeInTheDocument();
  });

  it("shows retry on a model list failure and does not treat it as an empty capability", async () => {
    mocks.models.mockRejectedValue(new Error("catalog unavailable"));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const card = group("modelProvider.module.ttsTitle");
    expect(await within(card).findByRole("alert")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "modelProvider.configureCapability" })).not.toBeInTheDocument();
    mocks.models.mockResolvedValue({ data: { models: [availableModel] } });
    fireEvent.click(within(card).getByRole("button", { name: "common.retry" }));
    expect(await readyToSelect("modelProvider.module.ttsTitle")).toBeInTheDocument();
  });

  it("updates configuration after a successful save and keeps the capability in its category", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    let resolveSave!: (value: unknown) => void;
    mocks.saveModel.mockImplementation(() => new Promise(resolve => { resolveSave = resolve; }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalled());
    expect(group("modelProvider.module.ttsTitle")).toHaveClass("is-pending");
    expect(categoryButton("voice")).toHaveTextContent("0/2");
    const voice = screen.getByRole("region", { name: "modelProvider.defaults.voiceTitle" });
    await act(async () => { resolveSave({ data: { selections: [{ ...selectedLlm, model_key: "tts" }] } }); });
    expect(group("modelProvider.module.ttsTitle")).not.toHaveClass("is-pending");
    expect(categoryButton("voice")).toHaveTextContent("1/2");
    expect(group("modelProvider.module.ttsTitle").closest("section")).toBe(voice);
    mocks.saveModel.mockResolvedValue({ data: { selections: [] } });
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByLabelText("close-circle"));
    await waitFor(() => expect(group("modelProvider.module.ttsTitle")).toHaveClass("is-pending"));
    expect(categoryButton("voice")).toHaveTextContent("0/2");
    expect(group("modelProvider.module.ttsTitle").closest("section")).toBe(voice);
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
      await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
      expect(group(title)).not.toHaveClass("is-pending");
      expect(within(group(title)).getByLabelText("check-circle")).toBeInTheDocument();
      const calls = readiness().mock.calls.length;
      let resolveReadiness!: (value: unknown) => void;
      readiness().mockImplementationOnce(() => new Promise(resolve => { resolveReadiness = resolve; }));
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(readiness()).toHaveBeenCalledTimes(calls + 1));
      await readyToSelect(title);
      expect(within(group(title)).queryByLabelText("modelProvider.readyStatusAria")).not.toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
      await act(async () => {
        resolveReadiness({ data: { ready: hasFallback, source: hasFallback ? "shared" : undefined,
          model_name: hasFallback ? "Shared fallback" : undefined, group_name: hasFallback ? "Shared fallback" : undefined } });
      });
      await readyToSelect(title);
      expect(group(title).classList.contains("is-pending")).toBe(!hasFallback);
      expect(group(title).closest("section")).toHaveAttribute("aria-label", `modelProvider.defaults.${kind === "model" ? "voice" : "retrieval"}Title`);
      expect(categoryButton(kind === "model" ? "voice" : "retrieval")).toHaveTextContent(`${hasFallback ? 1 : 0}/${kind === "model" ? 2 : 4}`);
      expect(within(group(title)).getByLabelText(hasFallback ? "check-circle" : "minus-circle")).toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
      if (hasFallback) expect(within(group(title)).getByText("Shared fallback")).toBeInTheDocument();
      expect(mocks.selections).toHaveBeenCalledOnce();
      expect(mocks.services).toHaveBeenCalledOnce();
    });

    it("shows a recoverable error when the post-save readiness check fails", async () => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
      readiness().mockRejectedValueOnce(new Error("readiness unavailable"));
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(within(group(title)).getByRole("alert")).toHaveTextContent("modelProvider.capabilityStatusLoadFailed"));
      expect(within(group(title)).queryByLabelText("check-circle")).not.toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
      await readyToSelect(title);
      readiness().mockResolvedValueOnce({ data: { ready: true, source: "shared", model_name: "Recovered fallback", group_name: "Recovered fallback" } });
      fireEvent.click(within(group(title)).getByRole("button", { name: "common.retry" }));
      await waitFor(() => expect(group(title)).toHaveTextContent("Recovered fallback"));
      expect(save()).toHaveBeenCalledOnce();
    });

    it("preserves the current readiness when clearing fails to save", async () => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
      const calls = readiness().mock.calls.length;
      save().mockRejectedValueOnce(new Error("save unavailable"));
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(save()).toHaveBeenCalledOnce());
      await readyToSelect(title);
      expect(readiness()).toHaveBeenCalledTimes(calls);
      expect(group(title)).not.toHaveClass("is-pending");
      expect(within(group(title)).getByLabelText("check-circle")).toBeInTheDocument();
    });

    it("ignores a pre-save readiness retry that finishes after the saved configuration is checked", async () => {
      renderPanel();
      await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
      readiness().mockRejectedValue(new Error("refresh unavailable"));
      act(() => window.dispatchEvent(new Event("focus")));
      await waitFor(() => expect(within(group(title)).getByRole("alert")).toBeInTheDocument());
      let resolveOldRetry!: (value: unknown) => void;
      readiness().mockImplementationOnce(() => new Promise(resolve => { resolveOldRetry = resolve; }));
      fireEvent.click(within(group(title)).getByRole("button", { name: "common.retry" }));
      readiness().mockResolvedValueOnce({ data: { ready: false } });
      fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
      await waitFor(() => expect(group(title)).toHaveClass("is-pending"));
      await readyToSelect(title);
      await act(async () => {
        resolveOldRetry({ data: { ready: true, source: "own", model_name: oldName, group_name: oldName } });
      });
      expect(group(title)).toHaveClass("is-pending");
      expect(within(group(title)).getByLabelText("minus-circle")).toBeInTheDocument();
      expect(within(group(title)).queryByText(oldName)).not.toBeInTheDocument();
    });
  });

  it("keeps failed saves pending", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.saveModel.mockRejectedValue(new Error("save failed"));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    await readyToSelect("modelProvider.module.llmChatTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.llmChatTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(mocks.saveModel).toHaveBeenCalled());
    expect(group("modelProvider.module.llmChatTitle")).toHaveClass("is-pending");
    expect(mocks.changed).not.toHaveBeenCalled();
  });

  it("does not classify a selection filtered out by server validation as configured", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.saveModel.mockResolvedValue({ data: { selections: [] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(mocks.changed).toHaveBeenCalled());
    expect(group("modelProvider.module.ttsTitle")).toHaveClass("is-pending");
  });

  it("retains service configuration on failed removal and updates it within the same category", async () => {
    mocks.saveService.mockRejectedValueOnce(new Error("save failed"));
    mocks.groups.mockResolvedValue({ data: { groups: [{ ...selectedParsing, base_url: "", user_model_provider_id: "parser-provider" }] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const title = "modelProvider.module.cloudParsingServiceTitle";
    fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
    await waitFor(() => expect(mocks.saveService).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(group(title)).getByRole("combobox")).toBeEnabled());
    expect(group(title)).not.toHaveClass("is-pending");
    fireEvent.mouseDown(within(group(title)).getByLabelText("close-circle"));
    await waitFor(() => expect(group(title)).toHaveClass("is-pending"));
    mocks.saveService.mockResolvedValue({ data: { selections: [selectedParsing] } });
    await readyToSelect(title);
    fireEvent.mouseDown(within(group(title)).getByRole("combobox"));
    fireEvent.click(await screen.findByText("MinerU"));
    await waitFor(() => expect(group(title)).not.toHaveClass("is-pending"));
    expect(within(group(title)).getByRole("combobox")).toBeInTheDocument();
  });

  it("does not let an older refresh revert a newly saved selection", async () => {
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    let resolveRefresh!: (value: unknown) => void;
    mocks.selections.mockImplementationOnce(() => new Promise(resolve => { resolveRefresh = resolve; }));
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(mocks.selections).toHaveBeenCalledTimes(2));
    mocks.saveModel.mockResolvedValue({ data: { selections: [{ ...selectedLlm, model_key: "tts" }] } });
    await readyToSelect("modelProvider.module.ttsTitle");
    fireEvent.mouseDown(within(group("modelProvider.module.ttsTitle")).getByRole("combobox"));
    fireEvent.click(await screen.findByText("Demo model"));
    await waitFor(() => expect(group("modelProvider.module.ttsTitle")).not.toHaveClass("is-pending"));
    await act(async () => { resolveRefresh({ data: { selections: [] } }); });
    expect(group("modelProvider.module.ttsTitle")).not.toHaveClass("is-pending");
  });

  it("ignores a stale model catalog after refreshed configuration has no usable models", async () => {
    let resolveCatalog!: (value: unknown) => void;
    mocks.models.mockImplementation(({ modelType }: { modelType: string }) => modelType === "tts"
      ? new Promise(resolve => { resolveCatalog = resolve; })
      : Promise.resolve({ data: { models: [availableModel] } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
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
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    mocks.selections.mockResolvedValue({ data: { selections: [{ ...selectedLlm, availability: "unavailable" }] } });
    mocks.models.mockResolvedValue({ data: { models: [{ ...availableModel, availability: "unavailable" }] } });
    mocks.services.mockResolvedValue({ data: { selections: [] } });
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(within(group("modelProvider.module.llmChatTitle")).getByRole("alert"))
      .toHaveTextContent("modelProvider.modelNotReadyTip"));
    expect(group("modelProvider.module.llmChatTitle")).not.toHaveClass("is-pending");
    expect(within(group("modelProvider.module.llmChatTitle")).getByText("Demo model · Demo")).toBeInTheDocument();
    revealAllCategories();
    expect(screen.getAllByRole("group")).toHaveLength(12);
    expect(screen.getAllByRole("group").filter((row) => !row.classList.contains("is-pending")))
      .toEqual([group("modelProvider.module.llmChatTitle")]);
  });

  it("preserves shared capability readiness and administrator-only configuration", async () => {
    mocks.role = "user";
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) => Promise.resolve({
      data: { ready: params.model_type === "embed_main", source: "shared", provider_name: "Shared provider", model_name: "Shared embedding", shared_by_name: "Admin" },
    }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    expect(group("modelProvider.module.embeddingTitle")).not.toHaveClass("is-pending");
    expect(await within(group("modelProvider.module.multimodalEmbeddingTitle")).findByRole("combobox")).toBeDisabled();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("excludes disabled features and sends empty provider/service entries to their setup flow", async () => {
    mocks.imageEmbedEnabled = false;
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.services.mockResolvedValue({ data: { selections: [] } });
    renderPanel("empty");
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    revealAllCategories();
    expect(screen.getAllByRole("group")).toHaveLength(11);
    expect(categoryButton("retrieval")).toHaveTextContent("0/3");
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
    expect(screen.queryByRole("region", { name: "modelProvider.defaults.basicTitle" })).not.toBeInTheDocument();
    expect(within(group("modelProvider.module.cloudParsingServiceTitle")).getByText(/MinerU/)).toBeInTheDocument();
    mocks.selections.mockResolvedValue({ data: { selections: [selectedLlm] } });
    fireEvent.click(screen.getByRole("button", { name: "common.retry" }));
    expect(await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" })).toBeInTheDocument();
    expect(within(basic()).getAllByRole("group")).toHaveLength(2);
  });

  it("isolates failed model readiness and retries only that capability without losing selections", async () => {
    mocks.role = "user";
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) =>
      params.model_type === "tts" ? Promise.reject(new Error("upstream unavailable")) : Promise.resolve({ data: { ready: false } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    expect(group("modelProvider.module.llmChatTitle")).toHaveTextContent("Demo model");
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
    expect(within(card).queryByRole("alert")).not.toBeInTheDocument();
    expect(mocks.ready).toHaveBeenCalledTimes(calls + 1);
    expect(mocks.ready).toHaveBeenLastCalledWith(expect.objectContaining({ params: { model_type: "tts" } }));
    await act(async () => { resolveRetry({ data: { ready: true, source: "shared", model_name: "Shared speech" } }); });
    await waitFor(() => expect(within(group("modelProvider.module.ttsTitle")).queryByRole("alert")).not.toBeInTheDocument());
    expect(group("modelProvider.module.ttsTitle")).not.toHaveClass("is-pending");
    expect(mocks.selections).toHaveBeenCalledOnce();
    expect(mocks.verified).toHaveBeenCalledTimes(verifiedCalls);
    expect(mocks.saveModel).not.toHaveBeenCalled();
  });

  it("isolates service readiness failures and keeps a failed retry recoverable", async () => {
    mocks.role = "user";
    mocks.verified.mockImplementation(({ category }: { category: string }) =>
      category === "ocr" ? Promise.reject(new Error("unavailable")) : Promise.resolve({ data: { ready: false } }));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
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
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    const card = group("modelProvider.module.ttsTitle");
    let resolveRetry!: (value: unknown) => void;
    mocks.ready.mockImplementationOnce(() => new Promise(resolve => { resolveRetry = resolve; }));
    fireEvent.click(within(card).getByRole("button", { name: "common.retry" }));
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(within(card).queryByRole("alert")).not.toBeInTheDocument());
    await act(async () => { resolveRetry({ data: { ready: true, source: "shared" } }); });
    expect(group("modelProvider.module.ttsTitle")).toHaveClass("is-pending");
  });

  it("preserves the last shared configuration while reporting a failed refresh as unknown", async () => {
    mocks.role = "user";
    mocks.ready.mockResolvedValue({ data: { ready: true, source: "shared", model_name: "Shared speech" } });
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    mocks.ready.mockRejectedValue(new Error("unavailable"));
    act(() => window.dispatchEvent(new Event("focus")));
    const card = group("modelProvider.module.ttsTitle");
    expect(await within(card).findByRole("alert")).toHaveTextContent("modelProvider.capabilityStatusLoadFailed");
    expect(within(card).queryByLabelText("modelProvider.readyStatusAria")).not.toBeInTheDocument();
    expect(within(card).getByText("Shared speech")).toBeInTheDocument();
  });

  it("does not let an older background refresh overwrite a successful manual retry", async () => {
    mocks.role = "user";
    mocks.ready.mockRejectedValue(new Error("unavailable"));
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    let resolveRefresh!: (value: unknown) => void;
    mocks.selections.mockImplementationOnce(() => new Promise(resolve => { resolveRefresh = resolve; }));
    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(mocks.selections).toHaveBeenCalledTimes(2));
    mocks.ready.mockResolvedValueOnce({ data: { ready: true, source: "shared" } });
    fireEvent.click(within(group("modelProvider.module.ttsTitle")).getByRole("button", { name: "common.retry" }));
    await waitFor(() => expect(group("modelProvider.module.ttsTitle")).not.toHaveClass("is-pending"));
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    await act(async () => { resolveRefresh({ data: { selections: [selectedLlm] } }); });
    expect(group("modelProvider.module.ttsTitle")).not.toHaveClass("is-pending");
  });

  it("does not request readiness for a disabled feature", async () => {
    mocks.role = "user";
    mocks.imageEmbedEnabled = false;
    renderPanel();
    await screen.findByRole("region", { name: "modelProvider.defaults.basicTitle" });
    expect(mocks.ready.mock.calls.some(([options]) => options.params.model_type === "embed_image")).toBe(false);
    expect(screen.queryByRole("group", { name: "modelProvider.module.multimodalEmbeddingTitle" })).not.toBeInTheDocument();
  });
});
