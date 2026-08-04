import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import DefaultModelConfigPanel from "./DefaultModelConfigPanel";
import { modelProvidersApi, modelProvidersDefaultApi } from "../api";
import { AgentAppsAuth } from "@/components/auth";
import { useModelFeatures } from "@/hooks/useModelFeatures";

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: vi.fn(),
    apiCoreModelProvidersSelectedModelsGet: vi.fn(),
    apiCoreModelProvidersSelectedProvidersGet: vi.fn(),
    apiCoreModelProvidersModelsGet: vi.fn(),
    apiCoreModelProvidersSelectedModelsPut: vi.fn(),
    apiCoreModelProvidersVerifiedGet: vi.fn(),
    apiCoreModelProvidersProviderGroupsGet: vi.fn(),
    apiCoreModelProvidersSelectedProvidersPut: vi.fn(),
    apiCoreModelProvidersSelectedProvidersSharePut: vi.fn(),
  },
  modelProvidersDefaultApi: {
    apiCoreModelProvidersModelsReadyGet: vi.fn(),
    apiCoreModelProvidersSelectedModelsSharePut: vi.fn(),
  },
  unwrapModelProviderData: (payload: unknown) => {
    if (payload && typeof payload === "object" && "data" in (payload as any)) {
      return (payload as any).data;
    }
    return payload;
  },
  withModelProviderJsonOptions: (options: any = {}) => ({
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: vi.fn() },
}));

vi.mock("@/hooks/useModelFeatures", () => ({
  useModelFeatures: vi.fn(),
}));

const providersGetMock = modelProvidersApi.apiCoreModelProvidersGet as unknown as ReturnType<
  typeof vi.fn
>;
const selectedModelsGetMock =
  modelProvidersApi.apiCoreModelProvidersSelectedModelsGet as unknown as ReturnType<
    typeof vi.fn
  >;
const selectedProvidersGetMock =
  modelProvidersApi.apiCoreModelProvidersSelectedProvidersGet as unknown as ReturnType<
    typeof vi.fn
  >;
const modelsGetMock = modelProvidersApi.apiCoreModelProvidersModelsGet as unknown as ReturnType<
  typeof vi.fn
>;
const selectedModelsPutMock =
  modelProvidersApi.apiCoreModelProvidersSelectedModelsPut as unknown as ReturnType<
    typeof vi.fn
  >;

describe("DefaultModelConfigPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (AgentAppsAuth.getUserInfo as any).mockReturnValue({ role: "member" });
    (useModelFeatures as any).mockReturnValue({
      status: "ready",
      features: { image_embed_enabled: true },
    });
    providersGetMock.mockResolvedValue({ data: { providers: [] } });
    selectedModelsGetMock.mockResolvedValue({ data: { selections: [] } });
    selectedProvidersGetMock.mockResolvedValue({ data: { selections: [] } });
    modelsGetMock.mockResolvedValue({ data: { models: [] } });
    selectedModelsPutMock.mockResolvedValue({ data: { selections: [] } });
  });

  it("renders module rows and cloud service rows", async () => {
    renderWithProviders(<DefaultModelConfigPanel />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.module.llmChatTitle"),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByText("modelProvider.module.cloudParsingServiceTitle"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("modelProvider.module.searchEngineServiceTitle"),
    ).toBeInTheDocument();
  });

  it("hides the multimodal embedding module when image embed is disabled", async () => {
    (useModelFeatures as any).mockReturnValue({
      status: "ready",
      features: { image_embed_enabled: false },
    });
    renderWithProviders(<DefaultModelConfigPanel />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.module.llmChatTitle"),
      ).toBeInTheDocument();
    });
    expect(
      screen.queryByText("modelProvider.module.multimodalEmbeddingTitle"),
    ).not.toBeInTheDocument();
  });

  it("marks restricted modules as disabled for non-admin users", async () => {
    renderWithProviders(<DefaultModelConfigPanel />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.module.embeddingTitle"),
      ).toBeInTheDocument();
    });
    expect(
      screen.getAllByText("modelProvider.limited").length,
    ).toBeGreaterThan(0);
  });

  it("loads a preselected model and shows a share toggle for admins", async () => {
    (AgentAppsAuth.getUserInfo as any).mockReturnValue({ role: "system-admin" });
    selectedModelsGetMock.mockResolvedValue({
      data: {
        selections: [
          {
            base_url: "https://api.openai.com",
            group_name: "default",
            max_input_tokens: "8192",
            model_id: "gpt-4",
            model_key: "llm",
            name: "GPT-4",
            provider_name: "OpenAI",
            share: true,
            user_model_provider_group_id: "group-1",
            user_model_provider_id: "provider-1",
          },
        ],
      },
    });
    renderWithProviders(<DefaultModelConfigPanel />);
    await waitFor(() => {
      expect(screen.getByText(/GPT-4/)).toBeInTheDocument();
    });
    expect(
      screen.getByText("modelProvider.maxInputTokens"),
    ).toBeInTheDocument();
    const switches = screen.getAllByRole("switch");
    expect(switches.length).toBeGreaterThan(0);
  });

  it("loads model options for a module when its dropdown opens", async () => {
    modelsGetMock.mockResolvedValue({
      data: {
        models: [
          {
            id: "model-1",
            name: "Model One",
            base_url: "https://api.example.com",
            group_name: "default",
            provider_name: "Example",
            user_model_provider_group_id: "group-1",
            user_model_provider_id: "provider-1",
          },
        ],
      },
    });
    renderWithProviders(<DefaultModelConfigPanel />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.module.llmChatTitle"),
      ).toBeInTheDocument();
    });
    const select = document.querySelector("#model-provider-llm");
    expect(select).not.toBeNull();
    fireEvent.mouseDown(select!.closest(".ant-select-selector") as Element);
    await waitFor(() => {
      expect(modelsGetMock).toHaveBeenCalledWith(
        expect.objectContaining({ modelType: "llm" }),
      );
    });
    await waitFor(() => {
      expect(screen.getByText("Model One")).toBeInTheDocument();
    });
  });
});
