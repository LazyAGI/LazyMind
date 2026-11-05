import { afterEach, describe, expect, it, vi } from "vitest";
import { modelProvidersApi, updateProviderRequestConfig } from "./api";

afterEach(() => vi.restoreAllMocks());

describe("request configuration API", () => {
  it("saves advanced settings when OpenAI Responses is the default protocol", async () => {
    const request = vi.spyOn(modelProvidersApi, "apiCoreModelProvidersModelProviderIdRequestConfigPut")
      .mockResolvedValue({ data: { data: { default_protocol: "openai-responses", protocols: [] } } } as never);
    const input = { default_protocol: "openai-responses" as const, endpoints: { "openai-responses": "" }, advanced: { headers: { "X-Probe": "value" } } };

    await expect(updateProviderRequestConfig("provider-a", input)).resolves.toMatchObject({ default_protocol: "openai-responses" });
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ updateProviderRequestConfigOpenAPIRequest: input }));
  });
});
