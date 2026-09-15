import { beforeEach, describe, expect, it } from "vitest";
import { PDF_CHAT_CACHE_TTL_MS, readCachedPdfChat, touchCachedPdfChat } from "./cache";

describe("PDF temporary chat cache", () => {
  beforeEach(() => window.localStorage.clear());

  it("keeps and refreshes an active chat for one hour", () => {
    touchCachedPdfChat("doc-1", "chat-1", 1000);
    expect(readCachedPdfChat("doc-1", 1000 + PDF_CHAT_CACHE_TTL_MS - 1)?.conversationId).toBe("chat-1");

    touchCachedPdfChat("doc-1", "chat-1", 2000);
    expect(readCachedPdfChat("doc-1", 2000 + PDF_CHAT_CACHE_TTL_MS - 1)?.lastActiveAt).toBe(2000);
  });

  it("drops an inactive chat after one hour", () => {
    touchCachedPdfChat("doc-1", "chat-1", 1000);
    expect(readCachedPdfChat("doc-1", 1001 + PDF_CHAT_CACHE_TTL_MS)).toBeNull();
  });
});
