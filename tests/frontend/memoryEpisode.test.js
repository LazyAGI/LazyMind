import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  groupEpisodesByRecordedDate,
  mergeEpisodePages,
  sortEpisodesByRecordedTime,
} from "../../frontend/src/modules/memory/episodeViewModel.ts";

const episodeClientMocks = vi.hoisted(() => ({
  apiCoreMemoryEpisodesEpisodeIdDelete: vi.fn(),
  apiCoreMemoryEpisodesEpisodeIdGet: vi.fn(),
  apiCoreMemoryEpisodesGet: vi.fn(),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class Configuration {},
  DefaultApiFactory: () => episodeClientMocks,
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
  BASE_URL: "/gateway",
}));

import {
  deleteEpisode,
  getEpisode,
  listEpisodes,
} from "../../frontend/src/modules/memory/episodeApi.ts";

beforeEach(() => {
  episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdDelete.mockReset();
  episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdGet.mockReset();
  episodeClientMocks.apiCoreMemoryEpisodesGet.mockReset();
});

describe("Episode creation-time list", () => {
  it("sorts a page by creation time without mutating the API result", () => {
    const source = [
      { id: "older", recordedAtMs: 10 },
      { id: "newer", recordedAtMs: 30 },
      { id: "middle", recordedAtMs: 20 },
    ];

    const sorted = sortEpisodesByRecordedTime(source);

    expect(sorted.map((item) => item.id)).toEqual([
      "newer",
      "middle",
      "older",
    ]);
    expect(source.map((item) => item.id)).toEqual([
      "older",
      "newer",
      "middle",
    ]);
  });

  it("sorts episodes by creation time descending and groups them by local date", () => {
    const groups = groupEpisodesByRecordedDate(
      [
        { id: "older", recordedAtMs: Date.UTC(2026, 6, 23, 8, 0) },
        { id: "newer", recordedAtMs: Date.UTC(2026, 6, 24, 10, 0) },
        { id: "same-day", recordedAtMs: Date.UTC(2026, 6, 24, 9, 0) },
      ],
      "UTC",
    );

    expect(groups.map((group) => group.dateKey)).toEqual([
      "2026-07-24",
      "2026-07-23",
    ]);
    expect(groups[0].items.map((item) => item.id)).toEqual([
      "newer",
      "same-day",
    ]);
    expect(groups[1].items.map((item) => item.id)).toEqual(["older"]);
  });

  it("appends cursor pages without duplicating an episode", () => {
    const merged = mergeEpisodePages(
      [
        { id: "first", recordedAtMs: 20 },
        { id: "shared", recordedAtMs: 10 },
      ],
      [
        { id: "shared", recordedAtMs: 10 },
        { id: "last", recordedAtMs: 5 },
      ],
    );

    expect(merged.map((item) => item.id)).toEqual([
      "first",
      "shared",
      "last",
    ]);
  });
});

describe("Episode API seam", () => {
  it("normalizes a cursor page returned by Core", async () => {
    episodeClientMocks.apiCoreMemoryEpisodesGet.mockResolvedValue({
      data: {
        data: {
          items: [
            {
              id: "episode-1",
              conversation_id: "conversation-1",
              episode_type: "decision",
              hit_count: 3,
              occurred_at_ms: 10,
              recorded_at_ms: 20,
              source_kind: "chat_explicit",
              summary: "Use Core as the authority",
            },
          ],
          next_page_token: "next-2",
          total_size: 5,
        },
      },
    });

    await expect(
      listEpisodes({ pageSize: 20, pageToken: "next-1" }),
    ).resolves.toEqual({
      items: [
        {
          id: "episode-1",
          conversationId: "conversation-1",
          episodeType: "decision",
          hitCount: 3,
          occurredAtMs: 10,
          recordedAtMs: 20,
          sourceKind: "chat_explicit",
          summary: "Use Core as the authority",
        },
      ],
      nextPageToken: "next-2",
      totalSize: 5,
    });
    expect(episodeClientMocks.apiCoreMemoryEpisodesGet).toHaveBeenCalledWith({
      pageSize: 20,
      pageToken: "next-1",
    });
  });

  it("loads one Episode detail through the generated Core client", async () => {
    episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdGet.mockResolvedValue({
      data: {
        data: {
          id: "episode-1",
          conversation_id: "conversation-1",
          episode_type: "result",
          hit_count: 2,
          occurred_at_ms: 10,
          recorded_at_ms: 20,
          source_kind: "memory_review",
          summary: "Core returned the detail",
        },
      },
    });

    await expect(getEpisode("episode-1")).resolves.toMatchObject({
      id: "episode-1",
      episodeType: "result",
      summary: "Core returned the detail",
    });
    expect(
      episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdGet,
    ).toHaveBeenCalledWith({ episodeId: "episode-1" });
  });

  it("preserves Core request errors for the page to render", async () => {
    const requestError = new Error("request failed");
    episodeClientMocks.apiCoreMemoryEpisodesGet.mockRejectedValue(requestError);

    await expect(listEpisodes()).rejects.toBe(requestError);
  });

  it("deletes only the selected Episode id and preserves errors", async () => {
    episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdDelete.mockResolvedValueOnce(
      { data: null },
    );
    await deleteEpisode("episode/1");

    expect(
      episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdDelete,
    ).toHaveBeenCalledWith({ episodeId: "episode/1" });

    const requestError = new Error("delete failed");
    episodeClientMocks.apiCoreMemoryEpisodesEpisodeIdDelete.mockRejectedValueOnce(
      requestError,
    );
    await expect(deleteEpisode("episode-2")).rejects.toBe(requestError);
  });
});
