import { act, fireEvent, render as renderComponent, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { useEffect, useState, type ComponentProps, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import DefaultServicesPage from "./DefaultServicesPage";

const render = (children: ReactNode) => renderComponent(<ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider>);

const mocks = vi.hoisted(() => ({
  listModels: vi.fn(),
  listProviderGroups: vi.fn(),
  getProviders: vi.fn(),
  getSelections: vi.fn(),
  getSelectedProviders: vi.fn(),
  panelMounts: vi.fn(),
  actualPanel: false,
  translate: (key: string) => key,
}));

vi.mock("@/modules/knowledge/components/TextIndexManager", () => ({ default: () => null }));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: mocks.translate, i18n: { language: "zh-CN" } }),
}));

vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) } }));
vi.mock("@/hooks/useModelFeatures", () => ({
  useModelFeatures: () => ({ status: "ready", features: { image_embed_enabled: true } }),
}));
vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "cloud-session-changed",
  getCloudSession: async () => ({}),
  isCloudBusinessAvailable: () => false,
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersModelsGet: mocks.listModels,
    apiCoreModelProvidersProviderGroupsGet: mocks.listProviderGroups,
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersSelectedModelsGet: mocks.getSelections,
    apiCoreModelProvidersSelectedProvidersGet: mocks.getSelectedProviders,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
}));

vi.mock("../components/DefaultModelConfigPanel", async (importOriginal) => {
  const { default: ActualPanel } = await importOriginal<typeof import("../components/DefaultModelConfigPanel")>();
  return {
    default: function MountedPanel(props: ComponentProps<typeof ActualPanel>) {
      const [search, setSearch] = useState("");
      const [expanded, setExpanded] = useState(true);
      useEffect(() => { mocks.panelMounts(); }, []);
      if (mocks.actualPanel) return <ActualPanel {...props} />;
      return (
        <div>
          <div data-testid="model-provider-setup-state">{props.modelProviderSetupState}</div>
          <input aria-label="search models" value={search} onChange={(event) => setSearch(event.target.value)} />
          <button aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>voice models</button>
          <button onClick={() => props.onConfigureCloudService("cloudParsing")}>configure parsing</button>
          <button onClick={() => props.onConfigureCloudService("searchEngine")}>configure search</button>
          <button onClick={() => props.onConfigureToolService?.("academic")}>configure academic</button>
          <button onClick={() => props.onConfigureToolService?.("translation")}>configure translation</button>
        </div>
      );
    },
  };
});

vi.mock("./ExternalServicesPage", () => ({
  default: ({ variant, visibleCategories, focusCategory, parsingControl, onConfigurationChanged }: {
    variant: string;
    visibleCategories: string[];
    focusCategory?: string;
    parsingControl?: ReactNode;
    onConfigurationChanged?: () => void;
  }) => (
    <div data-testid="external-services" data-variant={variant} data-focus={focusCategory}
      data-categories={visibleCategories.join(",")}>
      <section data-service-category="parsing">{parsingControl}</section>
      <section data-service-category="search" />
      <section data-service-category="academic" />
      <section data-service-category="translation" />
      <button onClick={onConfigurationChanged}>save service</button>
    </div>
  ),
}));

describe("DefaultServicesPage", () => {
  const defaultOcrProps = {
    documentParsingEnabled: false,
    documentParsingSaving: false,
    controlsDisabled: false,
    onDocumentParsingChange: vi.fn(),
  };

  beforeEach(() => {
    mocks.actualPanel = false;
    mocks.listModels.mockReset();
    mocks.listProviderGroups.mockReset();
    mocks.getProviders.mockReset().mockResolvedValue({ data: { providers: [] } });
    mocks.getSelections.mockReset().mockResolvedValue({ data: { selections: [] } });
    mocks.getSelectedProviders.mockReset().mockResolvedValue({ data: { selections: [] } });
    mocks.panelMounts.mockReset();
    mocks.listProviderGroups.mockResolvedValue({ data: { groups: [] } });
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("allows configuration when any verified provider model exists without an LLM", async () => {
    mocks.listModels.mockResolvedValue({
      data: {
        models: [{ id: "model-multimodal", model_type: "multimodal_embedding" }],
      },
    });

    render(
      <DefaultServicesPage
        {...defaultOcrProps}
        onConfigureCloudService={vi.fn()}
        onConfigureProviders={vi.fn()}
        onModelSelectionChanged={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("ready");
    });
    expect(mocks.listModels).toHaveBeenCalledWith(
      {},
      expect.objectContaining({ silentError: true }),
    );
  });

  it("marks model setup ready while unrelated service checks are still pending", async () => {
    mocks.listModels.mockResolvedValue({
      data: { models: [{ id: "model-llm", model_type: "llm" }] },
    });
    mocks.listProviderGroups.mockImplementation(() => new Promise(() => {}));

    render(
      <DefaultServicesPage
        {...defaultOcrProps}
        onConfigureCloudService={vi.fn()}
        onConfigureProviders={vi.fn()}
        onModelSelectionChanged={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("ready");
    });
  });

  it("supports a backend that still requires model_type during a rolling update", async () => {
    mocks.listModels.mockImplementation(({ modelType }: { modelType?: string } = {}) => {
      if (!modelType) return Promise.reject(new Error("model_type is required"));
      return Promise.resolve({
        data: {
          models: modelType === "cross_modal_embed"
            ? [{ id: "model-multimodal", model_type: "multimodal_embedding" }]
            : [],
        },
      });
    });

    render(
      <DefaultServicesPage
        {...defaultOcrProps}
        onConfigureCloudService={vi.fn()}
        onConfigureProviders={vi.fn()}
        onModelSelectionChanged={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("ready");
    });
    expect(mocks.listModels).toHaveBeenCalledWith(
      {},
      expect.objectContaining({ silentError: true }),
    );
    expect(mocks.listModels).toHaveBeenCalledWith(
      { modelType: "cross_modal_embed" },
      expect.objectContaining({ silentError: true }),
    );
  });

  it("treats a Cloud-only catalog as ready without requiring a personal Provider", async () => {
    mocks.listModels.mockResolvedValue({
      data: {
        models: [{
          id: "lazymind-text-default",
          model_type: "llm",
          name: "LazyMind Text",
          source: "cloud",
          provider_id: "lazymind-cloud",
          provider_name: "LazyMind Cloud",
          availability: "available",
          read_only: true,
        }],
      },
    });

    render(
      <DefaultServicesPage
        {...defaultOcrProps}
        onConfigureCloudService={vi.fn()}
        onConfigureProviders={vi.fn()}
        onModelSelectionChanged={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("ready");
    });
  });

  it("opens OCR configuration only in a dialog, with the OCR master switch", async () => {
    mocks.listModels.mockResolvedValue({ data: { models: [] } });
    const onDocumentParsingChange = vi.fn();
    render(<DefaultServicesPage
      onConfigureCloudService={vi.fn()}
      onConfigureProviders={vi.fn()}
      onModelSelectionChanged={vi.fn()}
      documentParsingEnabled
      documentParsingSaving={false}
      controlsDisabled={false}
      onDocumentParsingChange={onDocumentParsingChange}
    />);

    expect(screen.queryByTestId("external-services")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "configure parsing" }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "文档解析（OCR）" })).toBeVisible());
    const services = screen.getByTestId("external-services");
    expect(services).toHaveAttribute("data-variant", "default-models");
    expect(services).toHaveAttribute("data-categories", "parsing");
    const switchControl = screen.getByRole("switch", { name: "settingsPage.knowledge.documentParsingAria" });
    expect(switchControl).toBeChecked();
    fireEvent.click(switchControl);
    expect(onDocumentParsingChange).toHaveBeenCalledWith(false, expect.anything());
    await waitFor(() => expect(screen.getByTestId("model-provider-setup-state")).toHaveTextContent("empty"));
  });

  it("refreshes setup after a service is saved while preserving the mounted panel's search and collapse state", async () => {
    mocks.listModels.mockResolvedValue({ data: { models: [] } });
    const onModelSelectionChanged = vi.fn();
    render(<DefaultServicesPage
      onConfigureCloudService={vi.fn()}
      onConfigureProviders={vi.fn()}
      onModelSelectionChanged={onModelSelectionChanged}
      documentParsingEnabled={false}
      documentParsingSaving={false}
      controlsDisabled={false}
      onDocumentParsingChange={vi.fn()}
    />);
    await waitFor(() => expect(mocks.listProviderGroups).toHaveBeenCalledTimes(4));
    fireEvent.change(screen.getByRole("textbox", { name: "search models" }), { target: { value: "embedding" } });
    fireEvent.click(screen.getByRole("button", { name: "voice models" }));

    fireEvent.click(screen.getByRole("button", { name: "configure search" }));
    fireEvent.click(screen.getByRole("button", { name: "save service" }));
    await waitFor(() => expect(mocks.listProviderGroups).toHaveBeenCalledTimes(8));
    expect(mocks.listModels).toHaveBeenCalledTimes(2);
    expect(onModelSelectionChanged).toHaveBeenCalledTimes(1);
    expect.soft(mocks.panelMounts).toHaveBeenCalledOnce();
    expect.soft(screen.getByRole("textbox", { name: "search models" })).toHaveValue("embedding");
    expect.soft(screen.getByRole("button", { name: "voice models" })).toHaveAttribute("aria-expanded", "false");
  });

  const selection = (id: string, name: string) => ({
    model_id: id, model_key: "llm", model_type: "llm", name, provider_name: "OpenAI",
    provider_id: "provider", provider_group_id: "group", group_name: "OpenAI", availability: "available",
  });

  const candidate = (id: string, name: string) => ({ ...selection(id, name), id });
  const selected = (id: string, name: string) => ({ data: { selections: [selection(id, name)] } });
  const modelRow = () => screen.getByRole("group", { name: "modelProvider.module.llmChatTitle" });
  const openSearchService = () => fireEvent.click(within(screen.getByRole("group", { name: "搜索引擎" }))
    .getByRole("button", { name: "modelProvider.configureCapability" }));

  it("refreshes persisted models after a service save and ignores a slower earlier selection response", async () => {
    mocks.actualPanel = true;
    mocks.listModels.mockResolvedValue({ data: { models: [candidate("old", "Current model")] } });
    let resolveOlder!: (data: unknown) => void;
    let resolveLatest!: (data: unknown) => void;
    mocks.getSelections.mockResolvedValueOnce(selected("old", "Current model"))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveOlder = resolve; }))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveLatest = resolve; }));
    render(<DefaultServicesPage {...defaultOcrProps} onConfigureCloudService={vi.fn()} onConfigureProviders={vi.fn()} onModelSelectionChanged={vi.fn()} />);
    expect(await screen.findByText("Current model · OpenAI")).toBeInTheDocument();
    const modelSelect = within(modelRow()).getByRole("combobox");
    await waitFor(() => expect(modelSelect).toBeEnabled());
    const voice = screen.getByRole("button", { name: /modelProvider\.defaults\.voiceTitle/ });
    fireEvent.click(voice);
    openSearchService();
    const save = await screen.findByRole("button", { name: "save service" });
    mocks.listModels.mockImplementationOnce(() => new Promise(() => {}));

    fireEvent.click(save);
    await waitFor(() => expect(resolveOlder).toBeTypeOf("function"));
    expect(screen.getByText("Current model · OpenAI")).toBeInTheDocument();
    expect(modelSelect).toBeEnabled();
    expect(voice).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(save);
    await waitFor(() => expect(resolveLatest).toBeTypeOf("function"));
    await act(async () => { resolveLatest(selected("latest", "Latest model")); });
    expect(await screen.findByText("Latest model · OpenAI")).toBeInTheDocument();
    await act(async () => { resolveOlder(selected("stale", "Stale model")); });
    expect(screen.getByText("Latest model · OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Stale model · OpenAI")).not.toBeInTheDocument();
    expect(mocks.panelMounts).toHaveBeenCalledOnce();
    expect(voice).toHaveAttribute("aria-expanded", "false");
  });

  it("keeps existing model options visible until the replacement catalog arrives", async () => {
    mocks.actualPanel = true;
    mocks.getSelections.mockResolvedValueOnce(selected("old", "Current model"))
      .mockImplementationOnce(() => new Promise(() => {}));
    let resolveCatalog!: (data: unknown) => void;
    let llmCalls = 0;
    mocks.listModels.mockImplementation(({ modelType }: { modelType?: string }) => {
      if (!modelType) return Promise.resolve({ data: { models: [candidate("old", "Current model")] } });
      if (modelType !== "llm") return Promise.resolve({ data: { models: [] } });
      if (llmCalls++ === 0) return Promise.resolve({ data: { models: [candidate("candidate-old", "Existing option")] } });
      return new Promise((resolve) => { resolveCatalog = resolve; });
    });
    render(<DefaultServicesPage {...defaultOcrProps} onConfigureCloudService={vi.fn()} onConfigureProviders={vi.fn()} onModelSelectionChanged={vi.fn()} />);
    expect(await screen.findByText("Current model · OpenAI")).toBeInTheDocument();
    const select = within(modelRow()).getByRole("combobox");
    await waitFor(() => expect(select).toBeEnabled());
    fireEvent.mouseDown(select);
    expect(await screen.findByText("Existing option", { selector: "strong" })).toBeInTheDocument();

    openSearchService();
    fireEvent.click(await screen.findByRole("button", { name: "save service" }));
    await waitFor(() => expect(resolveCatalog).toBeTypeOf("function"));
    expect(screen.getByText("Current model · OpenAI")).toBeInTheDocument();
    expect(screen.getByText("Existing option", { selector: "strong" })).toBeInTheDocument();
    await act(async () => { resolveCatalog({ data: { models: [candidate("candidate-new", "Fresh option")] } }); });
    expect(await screen.findByText("Fresh option", { selector: "strong" })).toBeInTheDocument();
    expect(screen.queryByText("Existing option", { selector: "strong" })).not.toBeInTheDocument();
  });

  it("opens a deep-linked category and allows reopening it after closing", async () => {
    mocks.listModels.mockResolvedValue({ data: { models: [] } });
    const onConfigureCloudService = vi.fn();
    render(<DefaultServicesPage
      onConfigureCloudService={onConfigureCloudService}
      onConfigureProviders={vi.fn()}
      onModelSelectionChanged={vi.fn()}
      focusServiceCategory="parsing"
      documentParsingEnabled={false}
      documentParsingSaving={false}
      controlsDisabled={false}
      onDocumentParsingChange={vi.fn()}
    />);
    await waitFor(() => expect(screen.getByRole("dialog", { name: "文档解析（OCR）" })).toBeVisible());
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "configure parsing" }));
    expect(onConfigureCloudService).toHaveBeenCalledWith("cloudParsing");
    await waitFor(() => expect(screen.getByTestId("external-services")).toHaveAttribute("data-focus", "parsing"));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "文档解析（OCR）" })).toBeVisible());
  });

  it.each([
    ["search", "搜索引擎"], ["academic", "学术搜索引擎"], ["translation", "机器翻译"],
  ])("opens only the requested %s configuration", async (category, title) => {
    mocks.listModels.mockResolvedValue({ data: { models: [] } });
    render(<DefaultServicesPage {...defaultOcrProps} onConfigureCloudService={vi.fn()} onConfigureProviders={vi.fn()} onModelSelectionChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: `configure ${category}` }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: title })).toBeVisible());
    expect(screen.getByTestId("external-services")).toHaveAttribute("data-categories", category);
  });
});
