import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useConversationTrail } from "./useConversationTrail";

const { getTrail } = vi.hoisted(() => ({
  getTrail: vi.fn(),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceGetConversationTrail: getTrail,
  }),
}));

describe("useConversationTrail", () => {
  it("loads every paginated trail page", async () => {
    getTrail
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              history_id: "history-1",
              seq: 1,
              summary: "第一轮",
            },
          ],
          next_page_token: "page-2",
        },
      })
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              history_id: "history-2",
              seq: 2,
              summary: "第二轮",
            },
          ],
        },
      });

    const { result } = renderHook(() =>
      useConversationTrail({ conversationId: "conversation-1" }),
    );

    await waitFor(() => expect(result.current.items).toHaveLength(2));

    expect(getTrail).toHaveBeenNthCalledWith(
      1,
      { name: "conversation-1", pageSize: 100 },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(getTrail).toHaveBeenNthCalledWith(
      2,
      { name: "conversation-1", pageSize: 100, pageToken: "page-2" },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });
});
