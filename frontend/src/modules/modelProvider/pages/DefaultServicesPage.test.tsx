import { fireEvent, render as renderComponent, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { useEffect, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import DefaultServicesPage from "./DefaultServicesPage";

const render = (children: ReactNode) => renderComponent(<ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider>);

const mocks = vi.hoisted(() => ({
  listModels: vi.fn(),
  listProviderGroups: vi.fn(),
  panelMounts: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersModelsGet: mocks.listModels,
    apiCoreModelProvidersProviderGroupsGet: mocks.listProviderGroups,
  },
  unwrapModelProviderData: (data: unknown) => data,
}));

vi.mock("../components/DefaultModelConfigPanel", () => ({
  default: function MockDefaultModelConfigPanel({ modelProviderSetupState, onConfigureCloudService, onConfigureToolService }: {
    modelProviderSetupState: string;
    onConfigureCloudService: (service: "cloudParsing" | "searchEngine") => void;
    onConfigureToolService: (category: "academic" | "translation") => void;
  }) {
    useEffect(() => { mocks.panelMounts(); }, []);
    return (
      <div>
        <div data-testid="model-provider-setup-state">{modelProviderSetupState}</div>
        <button onClick={() => onConfigureCloudService("cloudParsing")}>configure parsing</button>
        <button onClick={() => onConfigureCloudService("searchEngine")}>configure search</button>
        <button onClick={() => onConfigureToolService("academic")}>configure academic</button>
        <button onClick={() => onConfigureToolService("translation")}>configure translation</button>
      </div>
    );
  },
}));

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
    mocks.listModels.mockReset();
    mocks.listProviderGroups.mockReset();
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

  it("refreshes setup availability after a service is saved", async () => {
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

    fireEvent.click(screen.getByRole("button", { name: "configure search" }));
    fireEvent.click(screen.getByRole("button", { name: "save service" }));
    await waitFor(() => expect(mocks.listProviderGroups).toHaveBeenCalledTimes(8));
    expect(mocks.listModels).toHaveBeenCalledTimes(2);
    expect(onModelSelectionChanged).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(mocks.panelMounts).toHaveBeenCalledTimes(2));
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
