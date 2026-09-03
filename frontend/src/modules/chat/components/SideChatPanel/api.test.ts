import { beforeEach, describe, expect, it, vi } from "vitest";

const requestMocks = vi.hoisted(() => ({
  patch: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { patch: requestMocks.patch },
  BASE_URL: "/base",
}));

import { patchSideChatKnowledge } from "./api";

describe("patchSideChatKnowledge", () => {
  beforeEach(() => {
    requestMocks.patch.mockReset().mockResolvedValue({});
  });

  it("persists knowledge bases, creators, and tags", async () => {
    await patchSideChatKnowledge("child/1", {
      knowledgeBaseId: ["kb-1"],
      creators: ["creator-1"],
      tags: ["tag-1"],
    });

    expect(requestMocks.patch).toHaveBeenCalledWith(
      "/base/api/core/conversations/child%2F1:search-config",
      {
        dataset_ids: ["kb-1"],
        creators: ["creator-1"],
        tags: ["tag-1"],
      },
      { silentError: true },
    );
  });

  it("sends empty arrays when optional filters are cleared", async () => {
    await patchSideChatKnowledge("child-1", {});

    expect(requestMocks.patch).toHaveBeenCalledWith(
      "/base/api/core/conversations/child-1:search-config",
      { dataset_ids: [], creators: [], tags: [] },
      { silentError: true },
    );
  });
});
