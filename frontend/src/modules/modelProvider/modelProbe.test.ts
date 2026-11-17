import { beforeEach, describe, expect, it, vi } from "vitest";
import { probeFailureCode, probeModel } from "./modelProbe";

const mocks = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("./api", () => ({
  modelProvidersApi: { apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdProbePost: mocks.request },
  unwrapModelProviderData: (value: unknown) => value && typeof value === "object" && "data" in value ? value.data : value,
}));
beforeEach(() => vi.clearAllMocks());
describe("model probe API", () => {
  it("returns measured dimensions for a successful embedding probe", async () => {
    mocks.request.mockResolvedValue({ data: { data: { status: "passed", code: "ok", model_type: "embed", elapsed_ms: 31, embedding_dimensions: 1024 } } });
    await expect(probeModel({ providerId: "p", groupId: "g", modelId: "m" })).resolves.toMatchObject({ embedding_dimensions: 1024 });
  });
  it.each([0, -1, 1.5, "1024", true])("omits invalid measured dimensions %s", async (dimensions) => {
    mocks.request.mockResolvedValue({ data: { data: { status: "passed", code: "ok", model_type: "embed", elapsed_ms: 31, embedding_dimensions: dimensions } } });
    expect(await probeModel({ providerId: "p", groupId: "g", modelId: "m" })).not.toHaveProperty("embedding_dimensions");
  });
  it("omits dimensions for a failed or unrelated probe", async () => {
    mocks.request.mockResolvedValue({ data: { data: { status: "failed", code: "invalid_response", model_type: "embed", elapsed_ms: 31, embedding_dimensions: 1024 } } });
    expect(await probeModel({ providerId: "p", groupId: "g", modelId: "m" })).not.toHaveProperty("embedding_dimensions");
    mocks.request.mockResolvedValue({ data: { data: { status: "passed", code: "ok", model_type: "llm", elapsed_ms: 31, embedding_dimensions: 1024 } } });
    expect(await probeModel({ providerId: "p", groupId: "g", modelId: "m" })).not.toHaveProperty("embedding_dimensions");
  });
  it("sends only saved identity with a bounded timeout and cancellation", async () => {
    mocks.request.mockResolvedValue({ data: { data: { status: "passed", code: "ok", model_type: "llm", elapsed_ms: 31 } } });
    const signal = new AbortController().signal;
    const result = await probeModel({ providerId: "provider", groupId: "group", modelId: "model" }, signal);
    expect(mocks.request).toHaveBeenCalledWith({ modelProviderId: "provider", groupId: "group", modelId: "model" }, expect.objectContaining({ signal, timeout: 55000, silentError: true }));
    expect(result).toEqual({ status: "passed", code: "ok", model_type: "llm", elapsed_ms: 31 });
  });
  it("rejects invalid server output without surfacing raw response values", async () => {
    mocks.request.mockResolvedValue({ data: { data: { status: "secret-token", code: "raw-secret", message: "raw-secret" } } });
    await expect(probeModel({ providerId: "p", groupId: "g", modelId: "m" })).resolves.toEqual({ status: "failed", code: "invalid_response", model_type: "", elapsed_ms: 0 });
  });
  it.each([
    ["passed", "authentication_failed"],
    ["failed", "ok"],
    ["unsupported", "timeout"],
    ["accepted", "ok"],
  ])("rejects inconsistent outcome %s/%s", async (status, code) => {
    mocks.request.mockResolvedValue({ data: { data: { status, code, model_type: "llm", elapsed_ms: 31 } } });
    await expect(probeModel({ providerId: "p", groupId: "g", modelId: "m" })).resolves.toEqual({ status: "failed", code: "invalid_response", model_type: "", elapsed_ms: 0 });
  });
  it("maps only allowlisted transport failure reasons", () => {
    expect(probeFailureCode({ response: { status: 409, data: { data: { reason: "probe_in_progress" } } } })).toBe("probe_in_progress");
    expect(probeFailureCode({ response: { status: 400, data: { data: { reason: "raw-secret" } } } })).toBe("upstream_error");
    expect(probeFailureCode({ code: "ECONNABORTED", message: "raw-secret" })).toBe("timeout");
  });
});
