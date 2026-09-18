import { beforeEach, describe, expect, it, vi } from "vitest";
import { listCloudResources } from "./cloudResourceApi";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("@/components/request", () => ({ BASE_URL: "/desktop", axiosInstance: { defaults: {}, request: (options: { url: string; method: string }) => options.method.toUpperCase() === "GET" ? get(options.url, options) : post(options.url, options) } }));

const item = (id: number) => ({
  resource_id: `cloud-${id}`, resource_type: "skill", resource_name: `Skill ${id}`,
  content_size: 10, format_schema: "lazymind.resource-manifest/v2", updated_at: "2026-09-07T00:00:00Z",
  presence_status: "download_required", local_exists: false,
});
const page = (items: ReturnType<typeof item>[], next_cursor?: string) => ({ data: { data: { items, next_cursor } } });

describe("Desktop Cloud resource pagination", () => {
  beforeEach(() => { get.mockReset(); post.mockReset(); });

  it.each(["skill", "workflow"] as const)("reads every %s cursor page without triggering a download", async (kind) => {
    get.mockResolvedValueOnce(page(Array.from({ length: 100 }, (_, i) => item(i)), "cursor-2"))
      .mockResolvedValueOnce(page([item(100)], "cursor-3"))
      .mockResolvedValueOnce(page([item(101)]));
    const result = await listCloudResources(kind);
    expect(result.map((r) => r.resource_id)).toEqual(Array.from({ length: 102 }, (_, i) => `cloud-${i}`));
    const secondURL = new URL(get.mock.calls[1][0], "http://localhost");
    expect(secondURL.pathname).toBe(`/desktop/api/core/cloud/${kind === "skill" ? "skills" : "workflows"}`);
    expect(secondURL.searchParams.get("cursor")).toBe("cursor-2");
    expect(secondURL.searchParams.get("page_size")).toBe("100");
    expect(post).not.toHaveBeenCalled();
  });

  it("follows a next cursor even when its preceding page is empty", async () => {
    get.mockResolvedValueOnce(page([], "next")).mockResolvedValueOnce(page([item(1)]));
    expect(await listCloudResources("skill")).toHaveLength(1);
  });

  it("rejects a repeated cursor rather than looping or reporting a complete list", async () => {
    get.mockResolvedValue(page([item(1)], "repeated"));
    await expect(listCloudResources("skill")).rejects.toThrow();
    expect(get.mock.calls.length).toBeLessThanOrEqual(3);
  });

  it("rejects a later-page failure rather than returning a truncated successful list", async () => {
    get.mockResolvedValueOnce(page([item(1)], "next")).mockRejectedValueOnce(new Error("fixture unavailable"));
    await expect(listCloudResources("skill")).rejects.toThrow("fixture unavailable");
  });

  it.each([undefined, { items: null }, { items: "wrong" }, { items: [], next_cursor: 123 }, { items: Array.from({ length: 101 }, (_, i) => item(i)) }])(
    "rejects malformed or oversized pages", async (payload) => {
      get.mockResolvedValue({ data: { data: payload } });
      await expect(listCloudResources("skill")).rejects.toThrow();
    },
  );

  it("preserves an empty successful catalog", async () => {
    get.mockResolvedValueOnce(page([]));
    expect(await listCloudResources("skill")).toEqual([]);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("propagates cancellation through pagination and makes no subsequent page request", async () => {
    const controller = new AbortController();
    let observedSignal: unknown;
    get.mockImplementation(async (_url, options) => {
      observedSignal = options.signal;
      controller.abort();
      throw new DOMException("Aborted", "AbortError");
    });
    const cancellableList = listCloudResources as (kind: "skill", options?: { signal?: AbortSignal }) => Promise<unknown>;
    await expect(cancellableList("skill", { signal: controller.signal })).rejects.toThrow();
    expect(observedSignal).toBe(controller.signal);
    expect(get).toHaveBeenCalledTimes(1);
  });
});
