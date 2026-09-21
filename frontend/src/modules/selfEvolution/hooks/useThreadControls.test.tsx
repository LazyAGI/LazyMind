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

  it("pauses and resumes with separate commands and keeps manual input available when paused", async () => {
    get.mockResolvedValue({ data: { data: { thread: { status: "running", runtime_status: "running", status_source: "live" } } } });
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.canPause).toBe(true));
    post.mockResolvedValue({ data: {} });
    get.mockResolvedValue({ data: { data: { thread: { status: "paused", runtime_status: "paused", status_source: "live" } } } });
    await act(async () => { await result.current.pause(); });
    expect(post.mock.calls[0][0]).toMatch(/\/pause$/);
    expect(result.current.canResume).toBe(true);
    expect(result.current.readOnly).toBe(false);
    get.mockResolvedValue({ data: { data: { thread: { status: "running", runtime_status: "running", status_source: "live" } } } });
    await act(async () => { await result.current.resume(); });
    expect(post.mock.calls[1][0]).toMatch(/\/resume$/);
    expect(post.mock.calls[1][1]).not.toEqual(post.mock.calls[0][1]);
    expect(result.current.canPause).toBe(true);
  });

  it.each([
    ["paused", "running", "live", false], // A checkpoint is not a runtime pause.
    ["paused", "paused", "cached", false],
    ["canceled", "cancelled", "live", false],
    ["running", "pausing", "live", false],
    ["failed", "failed", "live", true],
  ])("does not resume %s / %s / %s", async (status, runtime_status, status_source, cleanup_pending) => {
    get.mockResolvedValue({ data: { data: { thread: { status, runtime_status, status_source, cleanup_pending } } } });
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.thread?.status).toBe(status));
    expect(result.current.canResume).toBe(false);
    await act(async () => { await result.current.resume(); });
    expect(post).not.toHaveBeenCalled();
  });

  it("blocks duplicate controls and messaging until pause is confirmed", async () => {
    get.mockResolvedValue({ data: { data: { thread: { status: "running", runtime_status: "running", status_source: "live" } } } });
    let finish!: () => void;
    post.mockImplementation(() => new Promise(resolve => { finish = () => resolve({ data: {} }); }));
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.canPause).toBe(true));
    let pending!: Promise<void>;
    act(() => { pending = result.current.pause(); void result.current.pause(); void result.current.cancel(); });
    expect(post).toHaveBeenCalledOnce();
    expect(result.current.readOnly).toBe(true);
    get.mockResolvedValue({ data: { data: { thread: { status: "paused", runtime_status: "paused", status_source: "live" } } } });
    await act(async () => { finish(); await pending; });
    expect(result.current.readOnly).toBe(false);
  });

  it("accepts a live paused state after the control response is lost", async () => {
    get.mockResolvedValue({ data: { data: { thread: { status: "running", runtime_status: "running", status_source: "live" } } } });
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.canPause).toBe(true));
    post.mockRejectedValue(new Error("response lost"));
    get.mockResolvedValue({ data: { data: { thread: { status: "paused", runtime_status: "paused", status_source: "live" } } } });
    await act(async () => { await result.current.pause(); });
    expect(result.current.canResume).toBe(true);
    expect(result.current.actionError).toBeUndefined();
    expect(result.current.readOnly).toBe(false);
  });

  it("keeps pause failure recoverable without duplicating the command on retry", async () => {
    get.mockResolvedValue({ data: { data: { thread: { status: "running", runtime_status: "running", status_source: "live" } } } });
    const { result } = renderHook(() => useThreadControls("thread-a"));
    await waitFor(() => expect(result.current.canPause).toBe(true));
    post.mockRejectedValue(new Error("unavailable"));
    await act(async () => { await result.current.pause(); });
    expect(result.current.actionError).toBe("pause");
    expect(result.current.canPause).toBe(true);
    await act(async () => { await result.current.pause(); });
    expect(post.mock.calls[1][1]).toEqual(post.mock.calls[0][1]);
  });

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

  it.each(["cancel", "pause", "resume"] as const)("does not interrupt a slow %s confirmation with background polling", async action => {
    vi.useFakeTimers();
    const status = action === "resume" ? "paused" : "running";
    const observed = { data: { data: { thread: { status, runtime_status: status, status_source: "live" } } } };
    get.mockResolvedValue(observed);
    post.mockResolvedValue({ data: {} });
    const { result, unmount } = renderHook(() => useThreadControls("thread-a"));
    await act(async () => {});
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    await act(async () => { await result.current[action](); });
    let finish!: () => void;
    get.mockImplementationOnce(() => new Promise(resolve => { finish = () => resolve(observed); }));
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    const signal = get.mock.calls[2][1]?.signal;
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(get).toHaveBeenCalledTimes(3);
    expect(signal?.aborted).toBe(false);
    await act(async () => { finish(); });
    unmount();
    expect(signal?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("waits for the pause command response before polling for confirmation", async () => {
    vi.useFakeTimers();
    get.mockResolvedValue({ data: { data: { thread: { status: "running", runtime_status: "running", status_source: "live" } } } });
    let finish!: () => void;
    post.mockImplementationOnce(() => new Promise(resolve => { finish = () => resolve({ data: {} }); }));
    const { result, unmount } = renderHook(() => useThreadControls("thread-a"));
    await act(async () => {});
    let pending!: Promise<void>;
    act(() => { pending = result.current.pause(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(get).toHaveBeenCalledOnce();
    await act(async () => { finish(); await pending; });
    expect(get).toHaveBeenCalledTimes(2);
    unmount();
  });

  it.each(["cancel", "pause", "resume"] as const)("bounds %s confirmation attempts and returns to background polling", async action => {
    vi.useFakeTimers();
    const status = action === "resume" ? "paused" : "running";
    get.mockResolvedValue({ data: { data: { thread: { status, runtime_status: status, status_source: "live" } } } });
    post.mockResolvedValue({ data: {} });
    const { result, unmount } = renderHook(() => useThreadControls("thread-a"));
    await act(async () => {});
    await act(async () => { await result.current[action](); });
    for (let attempt = 0; attempt < 15; attempt += 1) {
      await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    }
    expect(get).toHaveBeenCalledTimes(17);
    expect(result.current.pendingAction).toBeUndefined();
    if (action === "cancel") expect(result.current.cancelState).toBe("unknown");
    else expect(result.current.actionError).toBe(action);
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(get).toHaveBeenCalledTimes(18);
    unmount();
  });

  it("waits for a background read to finish before scheduling another", async () => {
    vi.useFakeTimers();
    const { unmount } = renderHook(() => useThreadControls("thread-a"));
    await act(async () => {});
    let finish!: () => void;
    get.mockImplementationOnce(() => new Promise(resolve => { finish = () => resolve(response("running")); }));
    await act(async () => { await vi.advanceTimersByTimeAsync(20000); });
    expect(get).toHaveBeenCalledTimes(2);
    expect(get.mock.calls[1][1]?.signal?.aborted).toBe(false);
    await act(async () => { finish(); });
    unmount();
  });
});
