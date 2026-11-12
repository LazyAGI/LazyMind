import { axiosInstance } from "@/components/request";
import { afterEach, describe, expect, it, vi } from "vitest";
import { previewEmbeddingSwitch, clearEmbeddingChunks } from "./api";

afterEach(() => vi.restoreAllMocks());

describe("embedding switch preview API", () => {
  it("sends the candidate identity and unwraps the server decision", async () => {
    const preview = {
      outcome: "clear_required" as const,
      indexed_knowledge_bases: 2,
      active_model_name: "Old embedding",
      candidate_model_name: "New embedding",
      signature_status: "verified",
      generation: 5,
      can_switch: false,
    };
    const post = vi.spyOn(axiosInstance, "post").mockResolvedValue({ data: { data: preview } });

    await expect(previewEmbeddingSwitch({ model_id: "new-embedding", source: "own" }))
      .resolves.toEqual(preview);
    expect(post).toHaveBeenCalledWith(
      expect.stringContaining("/api/core/model_providers/embedding/switch-preview"),
      { model_id: "new-embedding", source: "own" },
      expect.objectContaining({ silentError: true, timeout: expect.any(Number) }),
    );
    expect(post.mock.calls[0]?.[2]?.timeout).toBeGreaterThan(90000);
  });
});

describe("embedding clear and activation API", () => {
  const request = { model_id: "new-model", source: "own" as const, generation: 8, confirm_clear_chunks: true as const };

  it.each([true, false])("sends confirmation and unwraps the activation acknowledgement (enveloped=%s)", async (enveloped) => {
    const result = { cleared: true, activated: true };
    const post = vi.spyOn(axiosInstance, "post").mockResolvedValue({ data: enveloped ? { data: result } : result });
    await expect(clearEmbeddingChunks(request)).resolves.toEqual(result);
    expect(post).toHaveBeenCalledWith(expect.stringContaining("/embedding/clear-chunks"), request,
      expect.objectContaining({ silentError: true, timeout: expect.any(Number) }));
  });

  it("propagates activation failure and replays the same confirmed request on retry", async () => {
    const failure = new Error("activation unavailable");
    const post = vi.spyOn(axiosInstance, "post")
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce({ data: { data: { cleared: true, activated: true } } });
    await expect(clearEmbeddingChunks(request)).rejects.toBe(failure);
    await expect(clearEmbeddingChunks(request)).resolves.toEqual({ cleared: true, activated: true });
    expect(post.mock.calls.map(([, input]) => input)).toEqual([request, request]);
  });
});
