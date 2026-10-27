import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DefaultModelConfigPanel from "./DefaultModelConfigPanel";

const mocks = vi.hoisted(() => ({
  providers: vi.fn(), selections: vi.fn(), services: vi.fn(), models: vi.fn(), save: vi.fn(),
  translate: (key: string) => key,
}));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: mocks.translate, i18n: { language: "en-US" } }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) } }));
vi.mock("@/hooks/useModelFeatures", () => ({ useModelFeatures: () => ({ status: "ready", features: { image_embed_enabled: true } }) }));
vi.mock("@/runtime/features", () => ({ runtimeFeatures: { hideUserGroupSurfaces: false } }));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "cloud-session-changed", getCloudSession: async () => ({}), isCloudBusinessAvailable: () => false,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.providers,
    apiCoreModelProvidersSelectedModelsGet: mocks.selections,
    apiCoreModelProvidersSelectedProvidersGet: mocks.services,
    apiCoreModelProvidersModelsGet: mocks.models,
    apiCoreModelProvidersSelectedModelsPut: mocks.save,
  }, modelProvidersDefaultApi: {},
  unwrapModelProviderData: (value: unknown) => value,
  withModelProviderJsonOptions: (value: unknown) => value,
}));
vi.mock("../upstreamCatalog", async () => {
  const { createUpstreamCatalogIndex } = await import("../upstreamCatalogIndex");
  return { lookupUpstreamModel: createUpstreamCatalogIndex({ generated_at: "fixture", providers: {
    OpenAI: [{ name: "catalog-vision", model_type: "llm", input: ["text", "image"], output: ["text"], context: 128000, max_output: 16000 }],
  } }).lookupModel };
});

const provider = { id: "supplier", name: "Private gateway", protocol_name: "OpenAI", is_custom: true };
const identity = { user_model_provider_id: "supplier", user_model_provider_group_id: "group", provider_name: "OpenAI", group_name: "Private gateway" };
const selection = { ...identity, model_key: "llm", model_id: "saved-uuid", name: "catalog-vision", model_type: "llm", vision: false, source: "own" };
const renderPanel = () => render(<ConfigProvider theme={{ token: { motion: false } }}><DefaultModelConfigPanel
  cloudServiceSetupStates={{ cloudParsing: "empty", searchEngine: "empty" }} modelProviderSetupState="ready"
  onConfigureCloudService={vi.fn()} onConfigureProviders={vi.fn()} onModelSelectionChanged={vi.fn()} onRetrySetup={vi.fn()}
/></ConfigProvider>);
async function openLlm() {
  const select = within(screen.getByRole("group", { name: "modelProvider.module.llmChatTitle" })).getByRole("combobox");
  await waitFor(() => expect(select).toBeEnabled());
  fireEvent.mouseDown(select);
}
function optionElement(name: string) {
  const title = screen.getByText(name, { selector: ".model-provider-select-copy strong" });
  return title.closest(".model-provider-select-option") as HTMLElement;
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.providers.mockResolvedValue({ data: { providers: [provider] } });
  mocks.selections.mockResolvedValue({ data: { selections: [selection] } });
  mocks.services.mockResolvedValue({ data: { selections: [] } });
  mocks.models.mockResolvedValue({ data: { models: [] } });
  mocks.save.mockResolvedValue({ data: { selections: [] } });
});
afterEach(cleanup);

describe("compact default model options", () => {
  it("shows the uploaded model logo in the selected value and refreshed options", async () => {
    const logo = "data:image/png;base64,uploaded-model";
    mocks.selections.mockResolvedValue({ data: { selections: [{ ...selection, logo_data_url: logo }] } });
    mocks.models.mockResolvedValue({ data: { models: [{ ...identity, id: "saved-uuid", name: "catalog-vision", model_type: "llm", logo_data_url: logo }] } });
    renderPanel();
    await screen.findByText("catalog-vision · Private gateway");
    expect(document.querySelector(".model-provider-select-value img")).toHaveAttribute("src", logo);
    await openLlm();
    await waitFor(() => expect(optionElement("catalog-vision").querySelector("img")).toHaveAttribute("src", logo));
  });
  it("retains explicit service identity when a selected shared supplier is absent from the local provider list", async () => {
    mocks.providers.mockResolvedValue({ data: { providers: [] } });
    mocks.selections.mockResolvedValue({ data: { selections: [{ ...selection, brand_key: "Qwen", service_key: "coding", is_custom: false, provider_name: "OpenAI", group_name: "QwenCodingPlan" }] } });
    renderPanel();
    expect(await screen.findByText("catalog-vision · Alibaba Cloud Model Studio · Coding Plan")).toBeInTheDocument();
  });

  it("keeps explicit custom supplier names independent of a shared catalog protocol and brand", async () => {
    mocks.providers.mockResolvedValue({ data: { providers: [] } });
    mocks.selections.mockResolvedValue({ data: { selections: [{ ...selection, brand_key: "Doubao", service_key: "coding", is_custom: true, provider_name: "Doubao", group_name: "AIhubmix" }] } });
    renderPanel();
    expect(await screen.findByText("catalog-vision · AIhubmix")).toBeInTheDocument();
    expect(document.querySelector('img[src="/provider-icons/custom.svg"]')).toBeInTheDocument();
  });
  it("keeps selected option identity without modality rows while refreshed options are pending", async () => {
    mocks.models.mockImplementation(() => new Promise(() => undefined));
    renderPanel();
    await screen.findByText("catalog-vision · Private gateway");
    await openLlm();
    const option = optionElement("catalog-vision");
    expect(within(option).queryByLabelText("modelProvider.modality.inputAria")).not.toBeInTheDocument();
    expect(within(option).queryByLabelText("modelProvider.modality.outputAria")).not.toBeInTheDocument();
    expect(option.querySelector(".model-provider-logo img")).toBeInTheDocument();
    expect(option.querySelector("small")).toHaveTextContent("Private gateway");
    expect(option).not.toHaveTextContent("modelProvider.customModelSuffix");
    expect(option).not.toHaveTextContent("modelProvider.builtInModelSuffix");
  });

  it("keeps refreshed options compact and saves UUID identity without display metadata", async () => {
    mocks.models.mockResolvedValue({ data: { models: [
      { ...identity, id: "saved-uuid", name: "catalog-vision", model_type: "llm", vision: false },
      { ...identity, id: "vision-uuid", name: "custom-vision", model_type: "llm", vision: true },
    ] } });
    renderPanel();
    await screen.findByText("catalog-vision · Private gateway");
    await openLlm();
    await screen.findByText("custom-vision", { selector: ".model-provider-select-copy strong" });
    for (const name of ["catalog-vision", "custom-vision"]) {
      expect(within(optionElement(name)).queryByLabelText("modelProvider.modality.inputAria")).not.toBeInTheDocument();
      expect(within(optionElement(name)).queryByLabelText("modelProvider.modality.outputAria")).not.toBeInTheDocument();
    }
    fireEvent.click(screen.getByText("custom-vision", { selector: ".model-provider-select-copy strong" }));
    await waitFor(() => expect(mocks.save).toHaveBeenCalledWith({ setSelectedModelsOpenAPIRequest: {
      selections: [{ model_key: "llm", model_id: "vision-uuid", source: "own" }],
    } }));
  });

  it("retains VLM selection when its modality row is hidden", async () => {
    mocks.models.mockResolvedValue({ data: { models: [{ ...identity, id: "vlm-uuid", name: "private-vlm", model_type: "vlm", vision: false }] } });
    renderPanel();
    await screen.findByText("catalog-vision · Private gateway");
    expect(screen.getByRole("button", { name: /modelProvider.defaults.visualTitle/ })).toHaveAttribute("aria-expanded", "true");
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "modelProvider.module.vlmTitle" }));
    await screen.findByText("private-vlm", { selector: ".model-provider-select-copy strong" });
    expect(within(optionElement("private-vlm")).queryByLabelText("modelProvider.modality.inputAria")).not.toBeInTheDocument();
    expect(within(optionElement("private-vlm")).queryByLabelText("modelProvider.modality.outputAria")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("private-vlm", { selector: ".model-provider-select-copy strong" }));
    await waitFor(() => expect(mocks.save).toHaveBeenCalledWith({ setSelectedModelsOpenAPIRequest: {
      selections: [{ model_key: "vlm", model_id: "vlm-uuid", source: "own" }],
    } }));
  });
});
