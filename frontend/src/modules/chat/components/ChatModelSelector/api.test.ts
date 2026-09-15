import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchChatModelCatalog, updateConversationChatModel } from "./api";

const request = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({
  axiosInstance: { request, defaults: {} },
  BASE_URL: "/base",
}));

const selection = { mode: "fixed", model_id: "model-1", version: 3 };

describe("chat model API", () => {
  beforeEach(() => request.mockReset());

  it.each([false, true])("loads catalogs with envelope=%s", async (envelope) => {
    const catalog = {
      selection, providers: [], auto_available: true, switch_allowed: true,
    };
    request.mockResolvedValue({ data: envelope ? { data: catalog } : catalog });
    const signal = new AbortController().signal;
    expect(await fetchChatModelCatalog("chat/1", signal)).toEqual(catalog);
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/base/api/core/chat/models?conversation_id=chat%2F1",
        method: "GET", signal, silentError: true,
      }),
    );
    await fetchChatModelCatalog();
    expect(request).toHaveBeenLastCalledWith(
      expect.objectContaining({ url: "/base/api/core/chat/models" }),
    );
  });

  it.each([selection, { selection }, { data: selection }, { data: { selection } }])(
    "preserves update response compatibility: %j",
    async (data) => {
      request.mockResolvedValue({ data });
      const signal = new AbortController().signal;
      const result = await updateConversationChatModel(
        "chat/1", { mode: "fixed", model_id: "model-1" }, 2, signal,
      );
      expect(result).toEqual(selection);
      expect(request).toHaveBeenCalledWith(
        expect.objectContaining({
          url: "/base/api/core/conversations/chat%2F1/model",
          method: "PATCH", signal, silentError: true,
          data: JSON.stringify({
            mode: "fixed", model_id: "model-1", expected_version: 2,
          }),
        }),
      );
    },
  );

  it("leaves version conflicts to the selector's refresh and retry handling", async () => {
    const conflict = { response: { status: 409 } };
    request.mockRejectedValueOnce(conflict);
    await expect(
      updateConversationChatModel("chat-1", { mode: "auto" }, 2),
    ).rejects.toBe(conflict);
    expect(request).toHaveBeenCalledTimes(1);
  });
});
