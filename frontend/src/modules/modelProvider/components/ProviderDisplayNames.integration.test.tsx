import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { createInstance, type i18n } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";
import type { AddedProvider } from "../pages/ModelProvidersPage";
import DefaultModelConfigPanel from "./DefaultModelConfigPanel";
import { ProviderWorkspace } from "./ProviderWorkspace";

const mocks = vi.hoisted(() => ({
  providers: vi.fn(),
  selectedModels: vi.fn(),
  selectedProviders: vi.fn(),
  models: vi.fn(),
  saveModels: vi.fn(),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) },
}));
vi.mock("@/hooks/useModelFeatures", () => ({
  useModelFeatures: () => ({ status: "ready", features: { image_embed_enabled: true } }),
}));
vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideUserGroupSurfaces: false },
}));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "cloud-session-changed",
  getCloudSession: async () => ({ state: "signed_out" }),
  isCloudBusinessAvailable: () => false,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.providers,
    apiCoreModelProvidersSelectedModelsGet: mocks.selectedModels,
    apiCoreModelProvidersSelectedProvidersGet: mocks.selectedProviders,
    apiCoreModelProvidersModelsGet: mocks.models,
    apiCoreModelProvidersSelectedModelsPut: mocks.saveModels,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));

let locale: i18n;

function supplier(name: string, id = name.toLowerCase(), isCustom = false): AddedProvider {
  return {
    id,
    name,
    isCustom,
    protocolName: isCustom ? "OpenAI" : name,
    source: isCustom ? "OpenAI" : name,
    brand: name,
    headline: "",
    baseUrl: "https://api.example.test/v1",
    capabilities: ["LLM_CHAT"],
    models: [],
    groups: [],
  };
}

function renderTranslated(content: React.ReactNode) {
  return render(
    <I18nextProvider i18n={locale}>
      <ConfigProvider theme={{ token: { motion: false } }}>
        {content}
      </ConfigProvider>
    </I18nextProvider>,
  );
}

function renderWorkspace() {
  const suppliers = [
    supplier("DeepSeek"),
    supplier("Kimi"),
    supplier("OpenRouter"),
    supplier("DeepSeek", "custom-deepseek", true),
  ];
  return renderTranslated(
    <ProviderWorkspace
      providers={suppliers}
      configuredProviders={suppliers}
      highlightProviderId="deepseek"
      loading={false}
      loadError={false}
      onRetry={vi.fn()}
      capabilityLabel={(type) => type}
      onLoadModels={vi.fn()}
      modelsLoading={{}}
      onAddModel={vi.fn()}
      onSyncModels={vi.fn()}
      onEditModel={vi.fn()}
      onDeleteModel={vi.fn()}
      onCheck={vi.fn()}
      checking={{}}
      onReload={vi.fn().mockResolvedValue(undefined)}
      cloudContent={null}
    />,
  );
}

const selection = {
  model_key: "llm",
  model_id: "chat-1",
  name: "fixture-chat",
  provider_name: "DeepSeek",
  user_model_provider_id: "deepseek",
  user_model_provider_group_id: "keys",
  group_name: "DeepSeek",
  availability: "available",
};

beforeEach(async () => {
  vi.clearAllMocks();
  locale = createInstance();
  await locale.use(initReactI18next).init({
    lng: "zh-CN",
    fallbackLng: "en-US",
    resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } },
    interpolation: { escapeValue: false },
  });
  mocks.providers.mockResolvedValue({ data: { providers: [{
    id: "deepseek", name: "DeepSeek", protocol_name: "Claude", is_custom: false,
    base_url: "https://api.example.test/v1",
  }] } });
  mocks.selectedModels.mockResolvedValue({ data: { selections: [selection] } });
  mocks.selectedProviders.mockResolvedValue({ data: { selections: [] } });
  mocks.models.mockImplementation(({ modelType }: { modelType: string }) => Promise.resolve({ data: { models: modelType === "llm" ? [
    { id: "chat-1", name: "fixture-chat", model_type: "llm", provider_name: "DeepSeek", group_name: "DeepSeek", user_model_provider_id: "deepseek", user_model_provider_group_id: "keys" },
    { id: "chat-2", name: "other-chat", model_type: "llm", provider_name: "DeepSeek", group_name: "DeepSeek", user_model_provider_id: "deepseek", user_model_provider_group_id: "keys" },
  ] : [] } }));
  mocks.saveModels.mockResolvedValue({ data: { selections: [{ ...selection, model_id: "chat-2", name: "other-chat" }] } });
});

afterEach(() => {
  cleanup();
  locale.off("languageChanged");
});

describe("localized supplier names in real configuration components", () => {
  it("updates the workspace title and templates with the locale while preserving custom names", async () => {
    renderWorkspace();
    const sidebar = screen.getByRole("navigation", { name: "供应商" });
    expect(within(sidebar).getByRole("button", { name: "深度求索" })).toBeInTheDocument();
    expect(within(sidebar).getByRole("button", { name: "月之暗面" })).toBeInTheDocument();
    expect(within(sidebar).getByRole("button", { name: "OpenRouter" })).toBeInTheDocument();
    expect(within(sidebar).getByRole("button", { name: "DeepSeek" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "深度求索" })).toHaveAttribute("title", "深度求索");

    await act(async () => { await locale.changeLanguage("en-US"); });
    expect(screen.getByRole("heading", { name: "DeepSeek" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Kimi" })).toBeInTheDocument();

    await act(async () => { await locale.changeLanguage("zh-CN"); });
    fireEvent.click(screen.getByRole("button", { name: "添加供应商" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.mouseDown(within(dialog).getByRole("combobox"));
    expect(await screen.findByText("月之暗面", { selector: ".ant-select-item-option-content" })).toBeInTheDocument();
    expect(screen.getByText("深度求索", { selector: ".ant-select-item-option-content" })).toBeInTheDocument();
  });

  it("finds suppliers by their Chinese, English and legacy aliases in both locales", async () => {
    renderWorkspace();
    const sidebar = screen.getByRole("navigation", { name: "供应商" });
    const search = screen.getByRole("textbox", { name: "搜索供应商或模型" });
    fireEvent.change(search, { target: { value: "深度求索" } });
    expect(within(sidebar).getAllByRole("button")).toHaveLength(1);
    expect(within(sidebar).getByRole("button", { name: "深度求索" })).toBeInTheDocument();
    fireEvent.change(search, { target: { value: "moonshot" } });
    expect(within(sidebar).getByRole("button", { name: "月之暗面" })).toBeInTheDocument();
    await act(async () => { await locale.changeLanguage("en-US"); });
    expect(screen.getByRole("button", { name: "Kimi" })).toBeInTheDocument();
    fireEvent.change(search, { target: { value: "深度求索" } });
    expect(within(sidebar).getAllByRole("button")).toHaveLength(1);
    expect(within(sidebar).getByRole("button", { name: "DeepSeek" })).toBeInTheDocument();
  });

  it("localizes default-model labels and alias search while saving only the actual model identity", async () => {
    renderTranslated(<DefaultModelConfigPanel
      modelProviderSetupState="ready"
      cloudServiceSetupStates={{ cloudParsing: "empty", searchEngine: "empty" }}
      onConfigureCloudService={vi.fn()}
      onConfigureProviders={vi.fn()}
      onModelSelectionChanged={vi.fn()}
      onRetrySetup={vi.fn()}
    />);
    expect(await screen.findByText("fixture-chat · 深度求索")).toBeInTheDocument();
    expect(document.querySelector('img[src="/provider-icons/anthropic.svg"]')).not.toBeInTheDocument();
    expect(document.querySelector('img[src="/provider-icons/deepseek.svg"]')).toBeInTheDocument();
    await act(async () => { await locale.changeLanguage("en-US"); });
    expect(await screen.findByText("fixture-chat · DeepSeek")).toBeInTheDocument();
    const llmRow = screen.getByRole("group", { name: enUS.modelProvider.module.llmChatTitle });
    const select = await within(llmRow).findByRole("combobox");
    fireEvent.mouseDown(select);
    expect(await screen.findByText("other-chat")).toBeInTheDocument();
    fireEvent.change(select, { target: { value: "深度求索" } });
    expect(await screen.findByText("other-chat")).toBeInTheDocument();
    fireEvent.click(screen.getByText("other-chat"));
    await waitFor(() => expect(mocks.saveModels).toHaveBeenCalledWith({
      setSelectedModelsOpenAPIRequest: { selections: [{ model_key: "llm", model_id: "chat-2", source: "own" }] },
    }));
  });
});
