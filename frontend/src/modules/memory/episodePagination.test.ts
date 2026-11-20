import { beforeEach, expect, it, vi } from "vitest";
import { listEpisodes, type EpisodeListPage } from "./episodeApi";
import { createEpisodePager } from "./episodePagination";

vi.mock("./episodeApi", () => ({ listEpisodes: vi.fn() }));
const list = vi.mocked(listEpisodes);
beforeEach(() => list.mockReset());

const pages: EpisodeListPage[] = ["after-5", "after-10", ""].map(nextPageToken => ({
  items: [], totalSize: 15, nextPageToken,
}));

it("jumps directly to a numbered page on current Core", async () => {
  const first = { ...pages[0], supportsPageNumber: true };
  const third = { ...pages[2], supportsPageNumber: true };
  list.mockResolvedValueOnce(first).mockResolvedValueOnce(third);
  const pager = createEpisodePager(5);
  await pager.load(1);
  expect(await pager.load(3)).toBe(third);
  expect(list.mock.calls).toEqual([[{ pageSize: 5, page: 1 }], [{ pageSize: 5, page: 3 }]]);
});

it("follows and reuses cursors when an older Core ignores page numbers", async () => {
  list.mockImplementation(async ({ pageToken } = {}) =>
    pages[pageToken === "after-10" ? 2 : pageToken === "after-5" ? 1 : 0]);
  const pager = createEpisodePager(5);
  await pager.load(1);
  expect(await pager.load(3)).toBe(pages[2]);
  expect(list.mock.calls.map(([args]) => args?.pageToken ?? args?.page)).toEqual([1, "after-5", "after-10"]);
  list.mockClear();
  expect(await pager.load(2)).toBe(pages[1]);
  expect(list.mock.calls).toEqual([[{ pageSize: 5, pageToken: "after-5" }]]);
  pager.clearCursors();
  list.mockClear();
  expect(await pager.load(2)).toBe(pages[1]);
  expect(list.mock.calls).toEqual([[{ pageSize: 5, pageToken: undefined }], [{ pageSize: 5, pageToken: "after-5" }]]);
});

it("uses the saved cursor if the first page changes between requests on legacy Core", async () => {
  const changedFirst = { ...pages[0], nextPageToken: "after-4", totalSize: 16 };
  list.mockResolvedValueOnce(pages[0]).mockImplementation(async ({ pageToken } = {}) =>
    pageToken === "after-5" ? pages[1] : changedFirst);
  const pager = createEpisodePager(5);
  await pager.load(1);
  expect(await pager.load(2)).toBe(pages[1]);
  expect(list).toHaveBeenLastCalledWith({ pageSize: 5, pageToken: "after-5" });
});
