import { beforeEach, it, expect, vi } from "vitest";
import { paperGuideApi } from "./paperGuide";
const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: http }));
beforeEach(() => vi.clearAllMocks());
it("uses conditional GET and treats a 304 as unchanged rather than empty content", async () => {
  http.get.mockResolvedValue({
    status: 304,
    headers: { etag: '"v2"' },
    data: "",
  });
  const abort = new AbortController();
  const r = await paperGuideApi.status(
    { dataset: "d/a", document: "p b", language: "en" },
    { etag: '"v2"', signal: abort.signal },
  );
  expect(r).toEqual({ etag: '"v2"', data: undefined });
  const [url, config] = http.get.mock.calls[0];
  expect(url).toContain("/d%2Fa/documents/p%20b/paper-guide");
  expect(config.headers["If-None-Match"]).toBe('"v2"');
  expect(config.params.language).toBe("en");
  expect(config.signal).toBe(abort.signal);
  expect(config.validateStatus(304)).toBe(true);
  expect(config.validateStatus(403)).toBe(false);
});
it("requests corrections after the last change version using exact revision IDs", async () => {
  http.get.mockResolvedValue({ data: { data: { sections: [] } } });
  await paperGuideApi.revision(
    { dataset: "d", document: "p", language: "zh" },
    "old/r1",
    17,
  );
  expect(http.get.mock.calls[0][0]).toContain("/revisions/old%2Fr1");
  expect(http.get.mock.calls[0][1].params).toEqual({
    language: "zh",
    after_version: 17,
  });
});
