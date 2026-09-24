import { beforeEach, describe, expect, it, vi } from "vitest";
import { listSkillAssetsPage } from "./skillApi";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/components/request", () => ({
  BASE_URL: "/desktop",
  axiosInstance: { defaults: {}, request },
  localizeErrorCode: vi.fn(),
}));

beforeEach(() => {
  request.mockReset().mockResolvedValue({ data: { data: {
    items: [{ skill_id: "summary", skill_name: "Summary 摘要" }],
    total: 21, page: 2, page_size: 20,
  } } });
});

describe("skill list search request", () => {
  it("sends name-only search through the generated client and preserves server pagination", async () => {
    const result = await listSkillAssetsPage({ keyword: " Summary 摘要 ", nameOnly: true, page: 2, pageSize: 20 });
    const url = new URL(request.mock.calls[0][0].url, "http://localhost");
    expect(url.pathname).toBe("/desktop/api/core/skills");
    expect(url.searchParams.get("keyword")).toBe("Summary 摘要");
    expect(url.searchParams.get("name_only")).toBe("true");
    expect(url.searchParams.get("page")).toBe("2");
    expect(url.searchParams.get("page_size")).toBe("20");
    expect(result).toMatchObject({ total: 21, page: 2, pageSize: 20, records: [{ id: "summary", name: "Summary 摘要" }] });
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("keeps full-text search as the default for other consumers", async () => {
    await listSkillAssetsPage({ keyword: "summary" });
    const url = new URL(request.mock.calls[0][0].url, "http://localhost");
    expect(url.searchParams.get("keyword")).toBe("summary");
    expect(url.searchParams.has("name_only")).toBe(false);
  });

  it("omits a blank keyword while keeping the requested name-only mode", async () => {
    await listSkillAssetsPage({ keyword: "  ", nameOnly: true });
    const url = new URL(request.mock.calls[0][0].url, "http://localhost");
    expect(url.searchParams.has("keyword")).toBe(false);
    expect(url.searchParams.get("name_only")).toBe("true");
  });
});
