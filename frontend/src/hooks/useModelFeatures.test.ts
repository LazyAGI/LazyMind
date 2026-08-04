import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
  MODEL_FEATURES_CHANGED_EVENT,
  fetchModelFeatures,
  invalidateModelFeaturesCache,
  isImageEmbedRequired,
  notifyModelFeaturesChanged,
  useModelFeatures,
} from "./useModelFeatures";
import { axiosInstance } from "@/components/request";

vi.mock("@/components/request", () => ({
  axiosInstance: { get: vi.fn() },
  BASE_URL: "",
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;

describe("isImageEmbedRequired", () => {
  it("returns true only when the flag is strictly true", () => {
    expect(isImageEmbedRequired({ image_embed_enabled: true, image_embed_required: true })).toBe(
      true,
    );
    expect(isImageEmbedRequired({ image_embed_enabled: true, image_embed_required: false })).toBe(
      false,
    );
    expect(isImageEmbedRequired({ image_embed_enabled: true })).toBe(false);
  });
});

describe("fetchModelFeatures", () => {
  beforeEach(() => {
    invalidateModelFeaturesCache();
    mockedGet.mockReset();
  });

  afterEach(() => {
    invalidateModelFeaturesCache();
  });

  it("caches the resolved features after the first successful fetch", async () => {
    mockedGet.mockResolvedValue({ data: { image_embed_enabled: true } });

    const first = await fetchModelFeatures();
    const second = await fetchModelFeatures();

    expect(first).toEqual({ image_embed_enabled: true });
    expect(second).toEqual({ image_embed_enabled: true });
    expect(mockedGet).toHaveBeenCalledTimes(1);
  });

  it("unwraps a nested data envelope", async () => {
    mockedGet.mockResolvedValue({
      data: { data: { image_embed_enabled: false, image_embed_required: true } },
    });

    const features = await fetchModelFeatures();

    expect(features).toEqual({ image_embed_enabled: false, image_embed_required: true });
  });

  it("falls back to a safe default when the request fails", async () => {
    mockedGet.mockRejectedValue(new Error("network error"));

    const features = await fetchModelFeatures();

    expect(features).toEqual({ image_embed_enabled: true, image_embed_required: false });
  });

  it("forces a refetch and bypasses the cache when force=true", async () => {
    mockedGet.mockResolvedValue({ data: { image_embed_enabled: true } });
    await fetchModelFeatures();

    mockedGet.mockResolvedValue({ data: { image_embed_enabled: false } });
    const forced = await fetchModelFeatures(true);

    expect(forced).toEqual({ image_embed_enabled: false });
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });
});

describe("useModelFeatures", () => {
  beforeEach(() => {
    invalidateModelFeaturesCache();
    mockedGet.mockReset();
  });

  afterEach(() => {
    invalidateModelFeaturesCache();
  });

  it("starts in loading state then resolves to ready", async () => {
    mockedGet.mockResolvedValue({ data: { image_embed_enabled: true } });

    const { result } = renderHook(() => useModelFeatures());

    expect(result.current.status).toBe("loading");

    await waitFor(() => {
      expect(result.current.status).toBe("ready");
    });
    expect(result.current).toEqual({
      status: "ready",
      features: { image_embed_enabled: true },
    });
  });

  it("reloads when the model-features-changed event fires", async () => {
    mockedGet.mockResolvedValue({ data: { image_embed_enabled: true } });
    const { result } = renderHook(() => useModelFeatures());
    await waitFor(() => expect(result.current.status).toBe("ready"));

    mockedGet.mockResolvedValue({ data: { image_embed_enabled: false } });
    act(() => {
      notifyModelFeaturesChanged();
    });

    await waitFor(() => {
      expect(result.current).toEqual({
        status: "ready",
        features: { image_embed_enabled: false },
      });
    });
  });

  it("exposes the change event name used across the app", () => {
    expect(MODEL_FEATURES_CHANGED_EVENT).toBe("lazymind:model-features-changed");
  });
});
