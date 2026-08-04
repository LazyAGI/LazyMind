import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const {
  getUserInfoMock,
  messageErrorMock,
  isDesktopRuntimeMock,
  mockedGet,
  fetchCurrentUserMock,
  fetchModelFeaturesMock,
} = vi.hoisted(() => ({
  getUserInfoMock: vi.fn(),
  messageErrorMock: vi.fn(),
  isDesktopRuntimeMock: vi.fn(() => false),
  mockedGet: vi.fn(),
  fetchCurrentUserMock: vi.fn(),
  fetchModelFeaturesMock: vi.fn(),
}));

vi.mock("antd", () => ({
  message: { error: messageErrorMock },
}));

vi.mock("@/components/auth", () => ({
  AUTH_USER_CHANGE_EVENT: "lazymind:user-change",
  AgentAppsAuth: { getUserInfo: getUserInfoMock },
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: (...args: unknown[]) => mockedGet(...args) },
  BASE_URL: "",
  localizeErrorCode: (code: string) => `localized:${code}`,
}));

vi.mock("@/modules/signin/utils/request", () => ({
  fetchCurrentUser: fetchCurrentUserMock,
}));

vi.mock("@/hooks/useModelFeatures", () => ({
  MODEL_FEATURES_CHANGED_EVENT: "lazymind:model-features-changed",
  fetchModelFeatures: fetchModelFeaturesMock,
  isImageEmbedRequired: () => false,
}));

vi.mock("@/runtime/mode", () => ({
  isDesktopRuntime: () => isDesktopRuntimeMock(),
}));

vi.mock("@/runtime/readiness", () => ({
  waitForRuntimeCapability: vi.fn().mockResolvedValue(undefined),
}));

import { useChatModelProviderGuard as useChatModelProviderGuardImport } from "./useChatModelProviderGuard";

function readyResponse(ready: boolean) {
  return { data: { data: { ready } } };
}

describe("useChatModelProviderGuard", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    getUserInfoMock.mockReturnValue({ userId: "user-1", dynamic: false });
    isDesktopRuntimeMock.mockReturnValue(false);
    // Invalidate the module-level cache by importing fresh state per test file run;
    // each test uses a distinct userId to avoid cross-test cache hits.
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("resolves to ready without model checks when the user is not dynamic", async () => {
    getUserInfoMock.mockReturnValue({ userId: "static-user", dynamic: false });
    fetchCurrentUserMock.mockResolvedValue({ dynamic: false });

    const { result } = renderHook(() => useChatModelProviderGuardImport());

    await waitFor(() => {
      expect(result.current.status).toBe("ready");
    });
    expect(result.current.canChat).toBe(true);
    expect(result.current.requiresModelProviderConfig).toBe(false);
    expect(mockedGet).not.toHaveBeenCalled();
  });

  it("marks status as missing when the llm model is not ready for a dynamic user", async () => {
    getUserInfoMock.mockReturnValue({ userId: "dynamic-user-missing", dynamic: true });
    fetchCurrentUserMock.mockResolvedValue({ dynamic: true });
    fetchModelFeaturesMock.mockResolvedValue({ image_embed_enabled: false });
    mockedGet.mockImplementation((url: string) => {
      if (url.includes("model_type=llm")) return Promise.resolve(readyResponse(false));
      return Promise.resolve(readyResponse(true));
    });

    const { result } = renderHook(() => useChatModelProviderGuardImport());

    await waitFor(() => {
      expect(result.current.status).toBe("missing");
    });
    expect(result.current.canChat).toBe(false);
    expect(result.current.needsModelProviderConfig).toBe(true);
    expect(result.current.requiresModelProviderConfig).toBe(true);
  });

  it("resolves to ready and exposes embedding/rerank/vlm readiness for a dynamic user", async () => {
    getUserInfoMock.mockReturnValue({ userId: "dynamic-user-ready", dynamic: true });
    fetchCurrentUserMock.mockResolvedValue({ dynamic: true });
    fetchModelFeaturesMock.mockResolvedValue({ image_embed_enabled: false });
    mockedGet.mockResolvedValue(readyResponse(true));

    const { result } = renderHook(() => useChatModelProviderGuardImport());

    await waitFor(() => {
      expect(result.current.status).toBe("ready");
    });
    expect(result.current.canChat).toBe(true);
    expect(result.current.embeddingReady).toBe(true);
    expect(result.current.rerankReady).toBe(true);
    expect(result.current.vlmReady).toBe(true);
  });

  it("sets status to error and shows a message when the llm readiness request fails", async () => {
    getUserInfoMock.mockReturnValue({ userId: "dynamic-user-error", dynamic: true });
    fetchCurrentUserMock.mockResolvedValue({ dynamic: true });
    fetchModelFeaturesMock.mockResolvedValue({ image_embed_enabled: false });
    mockedGet.mockImplementation((url: string) => {
      if (url.includes("model_type=llm")) return Promise.reject(new Error("boom"));
      return Promise.resolve(readyResponse(true));
    });

    const { result } = renderHook(() => useChatModelProviderGuardImport());

    await waitFor(() => {
      expect(result.current.status).toBe("error");
    });
    expect(result.current.canChat).toBe(false);
    expect(messageErrorMock).toHaveBeenCalledWith({
      key: "api-request-error",
      content: "localized:2000509",
    });
  });

  it("sets status to error when fetchCurrentUser rejects", async () => {
    getUserInfoMock.mockReturnValue({ userId: "user-fetch-fail", dynamic: false });
    fetchCurrentUserMock.mockRejectedValue(new Error("network down"));

    const { result } = renderHook(() => useChatModelProviderGuardImport());

    await waitFor(() => {
      expect(result.current.status).toBe("error");
    });
    expect(result.current.canChat).toBe(false);
  });

  it("refresh() re-triggers the readiness check", async () => {
    getUserInfoMock.mockReturnValue({ userId: "user-refresh", dynamic: false });
    fetchCurrentUserMock.mockResolvedValue({ dynamic: false });

    const { result } = renderHook(() => useChatModelProviderGuardImport());
    await waitFor(() => expect(result.current.status).toBe("ready"));

    fetchCurrentUserMock.mockClear();
    act(() => {
      result.current.refresh();
    });

    await waitFor(() => {
      expect(fetchCurrentUserMock).toHaveBeenCalled();
    });
  });
});
