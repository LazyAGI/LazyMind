import { act, renderHook } from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { getLatestPreferenceOrganizer, submitPreferenceOrganizer, type PreferenceOrganizerTask } from "../currentMemoryApi";
import { usePreferenceOrganizer } from "./usePreferenceOrganizer";

vi.mock("../currentMemoryApi", () => ({ getLatestPreferenceOrganizer: vi.fn(), submitPreferenceOrganizer: vi.fn() }));
const task = (status: PreferenceOrganizerTask["status"], id = "task-1"): PreferenceOrganizerTask => ({ task_id: id, status, created_at: "2026-09-03T00:00:00Z" });
const flush = async () => { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); };

describe("usePreferenceOrganizer", () => {
  beforeEach(() => { vi.useFakeTimers(); vi.resetAllMocks(); Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" }); Object.defineProperty(document, "hidden", { configurable: true, value: false }); });
  afterEach(() => vi.useRealTimers());

  it("restores active tasks, polls transitions and reports completion once", async () => {
    vi.mocked(getLatestPreferenceOrganizer).mockResolvedValueOnce(task("pending")).mockResolvedValueOnce(task("running")).mockResolvedValue(task("done"));
    const onFinished = vi.fn();
    const view = renderHook(() => usePreferenceOrganizer(onFinished));
    await flush(); expect(view.result.current.task?.status).toBe("pending");
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(view.result.current.running).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); });
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(view.result.current.active).toBe(false);
    view.unmount();
  });

  it("coalesces double submit and accepts reused pending tasks", async () => {
    vi.mocked(getLatestPreferenceOrganizer).mockResolvedValue(task("pending"));
    let resolve!: (value: PreferenceOrganizerTask) => void;
    vi.mocked(submitPreferenceOrganizer).mockImplementation(() => new Promise((done) => { resolve = done; }));
    const view = renderHook(() => usePreferenceOrganizer(vi.fn())); await flush();
    let first!: Promise<void>; let second!: Promise<void>;
    act(() => { first = view.result.current.submit(); second = view.result.current.submit(); });
    expect(submitPreferenceOrganizer).toHaveBeenCalledTimes(1);
    expect(view.result.current.submitting).toBe(true);
    await act(async () => { resolve(task("pending")); await Promise.all([first, second]); });
    expect(view.result.current.submitting).toBe(false);
    expect(view.result.current.task?.task_id).toBe("task-1");
    view.unmount();
  });

  it("stops polling off page and restores state without cancelling the job", async () => {
    vi.mocked(getLatestPreferenceOrganizer).mockResolvedValue(task("running"));
    const view = renderHook(() => usePreferenceOrganizer(vi.fn())); await flush();
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(1);
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    act(() => document.dispatchEvent(new Event("visibilitychange"))); await flush();
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(2);
    view.unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(2);
    const restored = renderHook(() => usePreferenceOrganizer(vi.fn())); await flush();
    expect(restored.result.current.running).toBe(true); restored.unmount();
  });

  it("discovers background work on the idle interval and backs off after errors", async () => {
    vi.mocked(getLatestPreferenceOrganizer).mockResolvedValueOnce(null)
      .mockResolvedValueOnce(task("running")).mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValue(task("done"));
    const onFinished = vi.fn();
    const view = renderHook(() => usePreferenceOrganizer(onFinished)); await flush();
    await act(async () => { await vi.advanceTimersByTimeAsync(14999); });
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(view.result.current.running).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(view.result.current.error).toBeInstanceOf(Error);
    await act(async () => { await vi.advanceTimersByTimeAsync(14999); });
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(view.result.current.error).toBeNull();
    expect(onFinished).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(5);
    expect(onFinished).toHaveBeenCalledTimes(1);
    view.unmount();
  });

  it("refreshes immediately after submit and ignores an older in-flight poll", async () => {
    let resolveOld!: (value: apiTask) => void;
    type apiTask = PreferenceOrganizerTask | null;
    vi.mocked(getLatestPreferenceOrganizer).mockImplementationOnce(() => new Promise<apiTask>((resolve) => { resolveOld = resolve; }))
      .mockResolvedValue(task("running", "new"));
    vi.mocked(submitPreferenceOrganizer).mockResolvedValue(task("pending", "new"));
    const view = renderHook(() => usePreferenceOrganizer(vi.fn())); await flush();
    await act(async () => { await view.result.current.submit(); });
    expect(getLatestPreferenceOrganizer).toHaveBeenCalledTimes(2);
    await act(async () => { resolveOld(task("done", "old")); });
    expect(view.result.current.task?.task_id).toBe("new");
    expect(view.result.current.running).toBe(true);
    view.unmount();
  });
});
