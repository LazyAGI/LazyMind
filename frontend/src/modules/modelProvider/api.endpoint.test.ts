import { afterEach, describe, expect, it, vi } from "vitest";
import { modelProvidersApi, updateProviderEndpoint } from "./api";

afterEach(() => vi.restoreAllMocks());

describe("provider endpoint API", () => {
  it("keeps save errors local to the endpoint editor", async () => {
    const error = Object.assign(new Error("Clear the old text vectors before editing the active embedding model"), {
      response: { status: 409, data: { code: 2005039 } },
    });
    const request = vi.spyOn(modelProvidersApi, "apiCoreModelProvidersModelProviderIdEndpointPut")
      .mockRejectedValue(error);

    await expect(updateProviderEndpoint("provider-a", "https://api.example.test/v1"))
      .rejects.toBe(error);
    expect(request).toHaveBeenCalledOnce();
    expect(request).toHaveBeenCalledWith({
      modelProviderId: "provider-a",
      updateModelProviderEndpointOpenAPIRequest: { base_url: "https://api.example.test/v1" },
    }, expect.objectContaining({ silentError: true }));
  });

  it("returns the saved canonical endpoint", async () => {
    const saved = { id: "provider-a", base_url: "https://api.example.test/v1" };
    vi.spyOn(modelProvidersApi, "apiCoreModelProvidersModelProviderIdEndpointPut")
      .mockResolvedValue({ data: { data: saved } } as never);

    await expect(updateProviderEndpoint("provider-a", "https://api.example.test"))
      .resolves.toEqual(saved);
  });
});
