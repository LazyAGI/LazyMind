import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { LAZYMIND_CLOUD_SESSION_CHANGED_EVENT } from "@/runtime/cloud/session";
import DefaultModelConfigPanel from "./DefaultModelConfigPanel";

const mocks = vi.hoisted(() => ({
  getProviders: vi.fn(),
  getSelectedModels: vi.fn(),
  getSelectedProviders: vi.fn(),
  listModels: vi.fn(),
  saveSelectedModels: vi.fn(),
  getCloudSession: vi.fn(),
  translate: (key: string) => key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: mocks.translate,
  }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) },
}));

vi.mock("@/hooks/useModelFeatures", () => ({
  useModelFeatures: () => ({
    status: "ready",
    features: { image_embed_enabled: true },
  }),
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideUserGroupSurfaces: false },
}));

vi.mock("@/runtime/cloud/session", () => ({
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
  getCloudSession: mocks.getCloudSession,
	isCloudBusinessAvailable: (session: any) => session?.state === "signed_in" && session?.configured !== false && session?.reachability !== "unreachable",
}));

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.getProviders,
    apiCoreModelProvidersSelectedModelsGet: mocks.getSelectedModels,
    apiCoreModelProvidersSelectedProvidersGet: mocks.getSelectedProviders,
    apiCoreModelProvidersModelsGet: mocks.listModels,
    apiCoreModelProvidersSelectedModelsPut: mocks.saveSelectedModels,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));

function renderPanel() {
  return render(
    <DefaultModelConfigPanel
      cloudServiceSetupStates={{ cloudParsing: "empty", searchEngine: "empty" }}
      modelProviderSetupState="ready"
      onConfigureCloudService={vi.fn()}
      onConfigureProviders={vi.fn()}
      onModelSelectionChanged={vi.fn()}
      onRetrySetup={vi.fn()}
    />,
  );
}

describe("Default model services with the LazyMind Cloud system provider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getCloudSession.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in" });
    mocks.getProviders.mockResolvedValue({ data: { providers: [] } });
    mocks.getSelectedModels.mockResolvedValue({ data: { selections: [] } });
    mocks.getSelectedProviders.mockResolvedValue({ data: { selections: [] } });
    mocks.listModels.mockImplementation(({ modelType }: { modelType?: string } = {}) =>
      Promise.resolve({
        data: {
          models: modelType === "llm"
            ? [{
                id: "lazymind-text-default",
                model_type: "llm",
                name: "LazyMind Text",
                source: "cloud",
                provider_id: "lazymind-cloud",
                provider_name: "LazyMind Cloud",
                availability: "available",
                read_only: true,
                capabilities: ["chat", "stream", "tool_calls"],
              }]
            : [],
        },
      }),
    );
    mocks.saveSelectedModels.mockResolvedValue({
      data: {
        selections: [{
          model_key: "llm",
          model_id: "lazymind-text-default",
          source: "cloud",
          name: "LazyMind Text",
          provider_id: "lazymind-cloud",
          provider_name: "LazyMind Cloud",
          availability: "available",
        }],
      },
    });
  });

  it("lists and saves a Cloud model without exposing a sharing control", async () => {
    const { container } = renderPanel();
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());

    const llmSelect = await screen.findByRole("combobox", {
      name: /modelProvider\.module\.llmChatTitle/,
    });
    fireEvent.mouseDown(llmSelect);
    const option = await screen.findByText("LazyMind Text");
    fireEvent.click(option);

    await waitFor(() =>
      expect(mocks.saveSelectedModels).toHaveBeenCalledWith({
        setSelectedModelsOpenAPIRequest: {
          selections: [{
            model_key: "llm",
            source: "cloud",
            model_id: "lazymind-text-default",
          }],
        },
      }),
    );
    const configured = screen.getByRole("region", { name: "modelProvider.configuredCapabilities" });
    const llmRow = await within(configured).findByRole("group", { name: "modelProvider.module.llmChatTitle" });
    expect(within(llmRow).queryByRole("switch")).not.toBeInTheDocument();
    expect(container.textContent).not.toContain("cloud-access-canary");
  });

  it("renders the existing TTS capability as a selectable default service", async () => {
    mocks.listModels.mockResolvedValue({ data: { models: [{
      id: "tts-model", name: "Speech model", model_type: "tts", source: "own",
      user_model_provider_id: "provider", user_model_provider_group_id: "group", provider_name: "Demo",
    }] } });
    renderPanel();
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());
    expect(
      await screen.findByRole("combobox", { name: "modelProvider.module.ttsTitle" }),
    ).toBeInTheDocument();
  });

  it("reloads model selections after the Cloud session changes", async () => {
    renderPanel();
    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalledTimes(1));

    act(() => {
      window.dispatchEvent(
        new CustomEvent(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, {
          detail: { state: "signed_in" },
        }),
      );
    });

    await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalledTimes(2));
  });

  it("hides every Cloud model surface while the Cloud account is signed out", async () => {
	mocks.getCloudSession.mockResolvedValue({
	  configured: true,
	  reachability: "reachable",
	  state: "signed_out",
	});
	const { container } = renderPanel();
	await waitFor(() => expect(mocks.getSelectedModels).toHaveBeenCalled());

	expect(container.querySelector('[data-provider-key="lazymind-cloud"]')).toBeNull();
	expect(screen.queryByText("LazyMind Text")).not.toBeInTheDocument();
  });
});
