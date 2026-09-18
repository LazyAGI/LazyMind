import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { useThreadControls } from "./useThreadControls";

vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn(), post: vi.fn() } }));
const get = vi.mocked(axiosInstance.get);
const post = vi.mocked(axiosInstance.post);
const response = (status: string, status_source = "live") => ({ data: { code: 0, data: { thread: { status, status_source } } } });

describe("independent task controls", () => {
  beforeEach(() => { vi.resetAllMocks(); get.mockResolvedValue(response("running")); });
  afterEach(() => vi.useRealTimers());

  it.each(["cancelling", "failed"])("restores %s cleanup as read-only while keeping cancel recovery available", async (runtime_status) => {
    get.mockResolvedValue({ data: { code: 0, data: { thread: {
      status: runtime_status === "failed" ? "failed" : "running",
      runtime_status, cleanup_pending: true, status_source: "live",
    } } } });
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.thread?.runtime_status).toBe(runtime_status));
    expect(result.current.readOnly).toBe(true);
    expect(result.current.canCancel).toBe(true);
    expect(post).not.toHaveBeenCalled();
  });

  it("restores and unmounts using reads only", async () => {
    const { result, unmount } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.thread?.status).toBe("running"));
    unmount();
    expect(post).not.toHaveBeenCalled();
    expect(get.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it("allows cancellation with unavailable status and reuses command_id after a lost response", async () => {
    get.mockRejectedValue(new Error("unavailable"));
    post.mockRejectedValue(new Error("lost response"));
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.checking).toBe(false));
    expect(result.current.canCancel).toBe(true);
    await act(async () => { await result.current.cancel(); });
    expect(result.current.cancelState).toBe("failed");
    await act(async () => { await result.current.cancel(); });
    expect(post).toHaveBeenCalledTimes(2);
    expect(post.mock.calls[1][1]).toEqual(post.mock.calls[0][1]);
    expect(get).toHaveBeenCalledTimes(3);
  });

  it("deduplicates clicks and lets a live completion win the cancel race", async () => {
    let finish!: () => void;
    post.mockImplementation(() => new Promise(resolve => { finish = () => resolve({ data: {} }); }));
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.thread?.status).toBe("running"));
    let pending!: Promise<void>;
    act(() => { pending = result.current.cancel(); void result.current.cancel(); });
    expect(post).toHaveBeenCalledTimes(1);
    get.mockResolvedValue(response("ended"));
    await act(async () => { finish(); await pending; });
    expect(result.current.thread?.status).toBe("ended");
    expect(result.current.canCancel).toBe(false);
    expect(result.current.readOnly).toBe(true);
  });

  it("does not claim cancellation success from cached terminal state", async () => {
    post.mockResolvedValue({ data: {} });
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.thread?.status).toBe("running"));
    get.mockResolvedValue(response("canceled", "cached"));
    await act(async () => { await result.current.cancel(); });
    expect(result.current.cancelState).toBe("pending");
    expect(result.current.canCancel).toBe(true);
  });

  it("ignores a late status from the previous task", async () => {
    let old!: (value: ReturnType<typeof response>) => void;
    get.mockImplementationOnce(() => new Promise(resolve => { old = resolve; }));
    const { result, rerender } = renderHook(({ id }) => useThreadControls(id), { initialProps: { id: "old" } });
    rerender({ id: "new" });
    await waitFor(() => expect(result.current.thread?.status).toBe("running"));
    await act(async () => { old(response("canceled")); });
    expect(result.current.thread?.status).toBe("running");
  });
});
