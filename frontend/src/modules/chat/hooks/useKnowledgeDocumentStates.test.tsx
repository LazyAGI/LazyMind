import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { KnowledgeOperation } from "../utils/knowledgeOperations";
import { knowledgeDocumentKey, useKnowledgeDocumentStates } from "./useKnowledgeDocumentStates";

const get = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get } }));

const operations = (...ids: string[]): KnowledgeOperation[] => [{
  call_id: "upload",
  action: "knowledge.document.upload",
  status: "success",
  items: ids.map((document_id) => ({
    name: document_id,
    resource_status: "created",
    resource_ref: { kind: "document", knowledge_base_id: "kb", document_id },
  })),
}];
const key = (document_id: string) => knowledgeDocumentKey({ knowledge_base_id: "kb", document_id });
const response = (processing_status: string) => ({ data: { processing_status } });
function deferred() {
  let resolve!: (value: ReturnType<typeof response>) => void;
  const promise = new Promise<ReturnType<typeof response>>((done) => { resolve = done; });
  return { promise, resolve };
}

beforeEach(() => {
  vi.useFakeTimers();
  Object.defineProperty(document, "hidden", { configurable: true, value: false });
  get.mockReset();
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

it("restarts polling after a manual refresh finds a failed document running", async () => {
  get.mockResolvedValueOnce(response("failed"))
    .mockResolvedValueOnce(response("running"))
    .mockResolvedValue(response("ready"));
  const { result } = renderHook(() => useKnowledgeDocumentStates(operations("doc")));
  await act(async () => {});
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(get).toHaveBeenCalledTimes(1);
  await act(async () => { await result.current.refresh(); });
  expect(result.current.current[key("doc")].status).toBe("running");
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(result.current.current[key("doc")].status).toBe("ready");
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(3);
});

it("retains the last state on a query error and polls until the query recovers", async () => {
  get.mockResolvedValueOnce(response("ready"))
    .mockRejectedValueOnce(new Error("network"))
    .mockResolvedValue(response("ready"));
  const { result } = renderHook(() => useKnowledgeDocumentStates(operations("doc")));
  await act(async () => {});
  await act(async () => { await result.current.refresh(); });
  expect(result.current.current[key("doc")]).toMatchObject({ status: "ready", unavailable: true });
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(result.current.current[key("doc")]).toEqual({ status: "ready", reason: undefined });
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(3);
});

it("pauses while hidden, including when an in-flight request completes", async () => {
  Object.defineProperty(document, "hidden", { configurable: true, value: true });
  const pending = deferred();
  get.mockReturnValueOnce(pending.promise).mockResolvedValue(response("running"));
  renderHook(() => useKnowledgeDocumentStates(operations("doc")));
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(get).not.toHaveBeenCalled();
  act(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(get).toHaveBeenCalledTimes(1);
  act(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await act(async () => { pending.resolve(response("running")); });
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(1);
  await act(async () => {
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(get).toHaveBeenCalledTimes(2);
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(get).toHaveBeenCalledTimes(3);
});

it("coalesces explicit refreshes behind an in-flight full query", async () => {
  const pending = deferred();
  get.mockReturnValueOnce(pending.promise).mockResolvedValue(response("ready"));
  const { result } = renderHook(() => useKnowledgeDocumentStates(operations("doc")));
  let refreshed!: Promise<void[]>;
  act(() => { refreshed = Promise.all([result.current.refresh(), result.current.refresh()]); });
  expect(get).toHaveBeenCalledTimes(1);
  await act(async () => { pending.resolve(response("failed")); await refreshed; });
  expect(get).toHaveBeenCalledTimes(2);
  expect(result.current.current[key("doc")].status).toBe("ready");
  expect(result.current.refreshing).toBe(false);
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(2);
});

it("follows a narrower in-flight poll with one full refresh and one polling chain", async () => {
  const pending = deferred();
  get.mockImplementation(async (url: string) => response(url.endsWith("/failed") ? "failed" : "running"));
  const { result } = renderHook(() => useKnowledgeDocumentStates(operations("failed", "active")));
  await act(async () => {});
  get.mockReturnValueOnce(pending.promise);
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(get).toHaveBeenCalledTimes(3);
  expect(get.mock.calls[2][0]).toContain("/active");
  let refreshed!: Promise<void[]>;
  act(() => { refreshed = Promise.all([result.current.refresh(), result.current.refresh()]); });
  expect(get).toHaveBeenCalledTimes(3);
  get.mockResolvedValueOnce(response("ready")).mockResolvedValueOnce(response("running"))
    .mockResolvedValue(response("ready"));
  await act(async () => { pending.resolve(response("ready")); await refreshed; });
  expect(get).toHaveBeenCalledTimes(5);
  expect(get.mock.calls.slice(3).map(([url]) => url)).toEqual([
    "/api/core/datasets/kb/documents/active", "/api/core/datasets/kb/documents/failed",
  ]);
  expect(result.current.current[key("failed")].status).toBe("running");
  expect(result.current.current[key("active")].status).toBe("ready");
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(get).toHaveBeenCalledTimes(6);
  expect(get.mock.calls[5][0]).toContain("/failed");
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(6);
});

it("discards a queued refresh and late response when the document set changes", async () => {
  const oldQuery = deferred();
  const newQuery = deferred();
  get.mockReturnValueOnce(oldQuery.promise).mockReturnValueOnce(newQuery.promise)
    .mockResolvedValue(response("ready"));
  const { result, rerender } = renderHook(({ id }) => useKnowledgeDocumentStates(operations(id)), {
    initialProps: { id: "old" },
  });
  let refreshed!: Promise<void>;
  act(() => { refreshed = result.current.refresh(); });
  rerender({ id: "new" });
  await act(async () => { oldQuery.resolve(response("failed")); await refreshed; });
  expect(get).toHaveBeenCalledTimes(2);
  expect(result.current.current[key("old")]).toBeUndefined();
  expect(result.current.refreshing).toBe(true);
  await act(async () => { newQuery.resolve(response("running")); });
  expect(result.current.current[key("new")].status).toBe("running");
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(get).toHaveBeenCalledTimes(3);
  expect(get.mock.calls[2][0]).toContain("/new");
  expect(result.current.current[key("new")].status).toBe("ready");
});

it("does not revive a previous request after switching away and back to the same documents", async () => {
  const oldQuery = deferred();
  get.mockReturnValueOnce(oldQuery.promise).mockResolvedValue(response("ready"));
  const { result, rerender } = renderHook(({ id }) => useKnowledgeDocumentStates(operations(id)), {
    initialProps: { id: "a" },
  });
  rerender({ id: "b" });
  await act(async () => {});
  rerender({ id: "a" });
  await act(async () => {});
  await act(async () => { oldQuery.resolve(response("running")); });
  expect(result.current.current[key("a")].status).toBe("ready");
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(3);
});

it("discards queued work and stops scheduling after unmount", async () => {
  const pending = deferred();
  get.mockReturnValueOnce(pending.promise);
  const { result, unmount } = renderHook(() => useKnowledgeDocumentStates(operations("doc")));
  let refreshed!: Promise<void>;
  act(() => { refreshed = result.current.refresh(); });
  unmount();
  await act(async () => { pending.resolve(response("running")); await refreshed; });
  await act(async () => { await vi.advanceTimersByTimeAsync(9000); });
  expect(get).toHaveBeenCalledTimes(1);
});
