import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { KnowledgeMarketTaskListOpenAPIResponse } from "@/api/generated/core-client";
import { listKnowledgeMarketTaskPage } from "@/modules/knowledge/api/knowledgeMarket";
import { useKnowledgeMarketTasks } from "./useKnowledgeMarketTasks";

vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({ listKnowledgeMarketTaskPage: vi.fn() }));
const list = vi.mocked(listKnowledgeMarketTaskPage);
const summary = (count: number, page = 1): KnowledgeMarketTaskListOpenAPIResponse => ({
  items: [], total: 25, page, page_size: 10, unfinished_count: count,
});
let hidden = false;
async function flush() { await act(async () => {}); }
async function tick(ms: number) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  hidden = false;
  vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
  list.mockResolvedValue(summary(0));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

describe("knowledge market task snapshots", () => {
  it("uses the global count even with no unfinished rows on the current page", async () => {
    list.mockResolvedValue(summary(3));
    const { result } = renderHook(() => useKnowledgeMarketTasks(true, ""));
    await flush();
    expect(result.current.data?.unfinished_count).toBe(3);
    await act(async () => { result.current.setPage(2); });
    expect(list).toHaveBeenLastCalledWith(2, expect.objectContaining({ silentError: true }));
    await tick(2000);
    expect(list).toHaveBeenCalledTimes(3);
  });

  it("checks for new external jobs at zero and speeds up while unfinished", async () => {
    const { result } = renderHook(() => useKnowledgeMarketTasks(false, ""));
    await flush();
    list.mockResolvedValue(summary(2));
    await tick(14999);
    expect(list).toHaveBeenCalledTimes(1);
    await tick(1);
    expect(result.current.data?.unfinished_count).toBe(2);
    list.mockResolvedValue(summary(0));
    await tick(2000);
    expect(result.current.data?.unfinished_count).toBe(0);
    await tick(2000);
    expect(list).toHaveBeenCalledTimes(3);
  });

  it("preserves the last count on failure and retries without inventing zero", async () => {
    list.mockResolvedValueOnce(summary(4)).mockRejectedValueOnce(new Error("offline"));
    const { result } = renderHook(() => useKnowledgeMarketTasks(false, ""));
    await flush();
    await tick(2000);
    expect(result.current.data?.unfinished_count).toBe(4);
    expect(result.current.queryFailed).toBe(true);
    await tick(2000);
    expect(result.current.data?.unfinished_count).toBe(0);
    expect(result.current.queryFailed).toBe(false);
  });

  it("keeps the count unknown after an initial failure", async () => {
    list.mockRejectedValueOnce(new Error("offline"));
    const { result } = renderHook(() => useKnowledgeMarketTasks(false, ""));
    await flush();
    expect(result.current.data).toBeUndefined();
    expect(result.current.queryFailed).toBe(true);
  });

  it("pauses when hidden, resumes immediately, and cleans up on unmount", async () => {
    list.mockResolvedValue(summary(1));
    const { unmount } = renderHook(() => useKnowledgeMarketTasks(false, ""));
    await flush();
    hidden = true;
    act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    await tick(20000);
    expect(list).toHaveBeenCalledTimes(1);
    hidden = false;
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(list).toHaveBeenCalledTimes(2);
    unmount();
    await tick(20000);
    act(() => { window.dispatchEvent(new Event("focus")); });
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("serializes focus refreshes and ignores a response from an obsolete request", async () => {
    let resolve!: (value: KnowledgeMarketTaskListOpenAPIResponse) => void;
    list.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const { result, rerender } = renderHook(({ key }) => useKnowledgeMarketTasks(false, key), { initialProps: { key: "old" } });
    act(() => { window.dispatchEvent(new Event("focus")); window.dispatchEvent(new Event("focus")); });
    expect(list).toHaveBeenCalledTimes(1);
    rerender({ key: "new" });
    await flush();
    expect(result.current.data?.unfinished_count).toBe(0);
    await act(async () => { resolve(summary(9)); });
    expect(result.current.data?.unfinished_count).toBe(0);
    expect(list).toHaveBeenCalledTimes(2);
  });
});
