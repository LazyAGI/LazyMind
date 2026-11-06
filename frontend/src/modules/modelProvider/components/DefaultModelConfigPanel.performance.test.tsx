import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDefaultModelConfig } from "../hooks/useDefaultModelConfig";

const mocks = vi.hoisted(() => ({
  role: "system-admin",
  providers: vi.fn(),
  selections: vi.fn(),
  services: vi.fn(),
  models: vi.fn(),
  ready: vi.fn(),
  verified: vi.fn(),
  groups: vi.fn(),
  translate: (key: string) => key,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: mocks.translate, i18n: { language: "zh-CN" } }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: mocks.role }) },
}));
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
    apiCoreModelProvidersGet: mocks.providers,
    apiCoreModelProvidersSelectedModelsGet: mocks.selections,
    apiCoreModelProvidersSelectedProvidersGet: mocks.services,
    apiCoreModelProvidersModelsGet: mocks.models,
    apiCoreModelProvidersVerifiedGet: mocks.verified,
    apiCoreModelProvidersProviderGroupsGet: mocks.groups,
  },
  modelProvidersDefaultApi: { apiCoreModelProvidersModelsReadyGet: mocks.ready },
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));

const cloudServiceSetupStates = { cloudParsing: "empty" as const, searchEngine: "empty" as const };
const renderConfig = () => renderHook(() => useDefaultModelConfig({
  cloudServiceSetupStates,
  modelProviderSetupState: "ready",
  onModelSelectionChanged: () => {},
}));

describe("default model configuration load timing", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.role = "system-admin";
    mocks.providers.mockResolvedValue({ data: { providers: [] } });
    mocks.selections.mockResolvedValue({ data: { selections: [] } });
    mocks.services.mockResolvedValue({ data: { selections: [] } });
    mocks.models.mockResolvedValue({ data: { models: [] } });
    mocks.ready.mockResolvedValue({ data: { ready: false } });
    mocks.verified.mockResolvedValue({ data: { ready: false } });
    mocks.groups.mockResolvedValue({ data: { groups: [] } });
  });

  it("starts all three initial requests without waiting for providers", async () => {
    let resolveProviders!: (value: unknown) => void;
    mocks.providers.mockImplementationOnce(() => new Promise((resolve) => { resolveProviders = resolve; }));
    const { result } = renderConfig();

    try {
      await waitFor(() => expect(mocks.providers).toHaveBeenCalledOnce());
      expect(mocks.selections).toHaveBeenCalledOnce();
      expect(mocks.services).toHaveBeenCalledOnce();
    } finally {
      await act(async () => { resolveProviders({ data: { providers: [] } }); });
    }
    await waitFor(() => expect(result.current.defaultLoadState).toBe("ready"));
  });

  it("shows completed capability readiness while another check is pending", async () => {
    mocks.role = "user";
    let resolveTts!: (value: unknown) => void;
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) =>
      params.model_type === "tts"
        ? new Promise((resolve) => { resolveTts = resolve; })
        : Promise.resolve({ data: { ready: params.model_type === "embed_main" } }),
    );
    const { result } = renderConfig();

    try {
      await waitFor(() => expect(mocks.ready.mock.calls.some(([options]) => options.params.model_type === "tts")).toBe(true));
      await waitFor(() => expect(result.current.defaultLoadState).toBe("ready"));
      await waitFor(() => expect(result.current.capabilityReadyStates.embed_main).toBe("ready"));
      expect(result.current.modelReadyStatus.embed_main?.ready).toBe(true);
      expect(result.current.capabilityReadyStates.tts).toBe("loading");
      await waitFor(() => expect(mocks.models.mock.calls.some(([options]) => options.modelType === "llm")).toBe(true));
    } finally {
      await act(async () => { resolveTts({ data: { ready: false } }); });
    }
    await waitFor(() => expect(result.current.capabilityReadyStates.tts).toBe("ready"));
  });

  it("ignores an older readiness result after a newer page refresh", async () => {
    mocks.role = "user";
    let resolveOldEmbedding!: (value: unknown) => void;
    let embeddingChecks = 0;
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) => {
      if (params.model_type === "embed_main" && embeddingChecks++ === 0) {
        return new Promise((resolve) => { resolveOldEmbedding = resolve; });
      }
      return Promise.resolve({ data: { ready: false } });
    });
    const { result } = renderConfig();
    await waitFor(() => expect(resolveOldEmbedding).toBeTypeOf("function"));

    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(result.current.capabilityReadyStates.embed_main).toBe("ready"));
    expect(result.current.modelReadyStatus.embed_main?.ready).toBe(false);

    await act(async () => { resolveOldEmbedding({ data: { ready: true } }); });
    expect(result.current.modelReadyStatus.embed_main?.ready).toBe(false);
  });

  it("keeps an in-flight readiness check when an unfinished refresh is superseded by a retry", async () => {
    mocks.role = "user";
    let resolveEmbedding!: (value: unknown) => void;
    let resolveRefresh!: (value: unknown) => void;
    mocks.ready.mockImplementation(({ params }: { params: { model_type: string } }) =>
      params.model_type === "embed_main"
        ? new Promise((resolve) => { resolveEmbedding = resolve; })
        : Promise.resolve({ data: { ready: false } }),
    );
    const { result } = renderConfig();
    await waitFor(() => expect(result.current.capabilityReadyStates.embed_main).toBe("loading"));
    mocks.selections.mockImplementationOnce(() => new Promise((resolve) => { resolveRefresh = resolve; }));

    act(() => window.dispatchEvent(new Event("focus")));
    await waitFor(() => expect(mocks.selections).toHaveBeenCalledTimes(2));
    try {
      await act(async () => { await result.current.retryCapabilityReadiness("tts"); });
      await act(async () => { resolveEmbedding({ data: { ready: true } }); });
      await waitFor(() => expect(result.current.capabilityReadyStates.embed_main).toBe("ready"));
      expect(result.current.modelReadyStatus.embed_main?.ready).toBe(true);
    } finally {
      await act(async () => { resolveRefresh({ data: { selections: [] } }); });
    }
  });
});
