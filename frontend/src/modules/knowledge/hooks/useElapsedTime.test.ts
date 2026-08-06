import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import useElapsedTime from "./useElapsedTime";

describe("useElapsedTime", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns all zeros when startTime is not provided", () => {
    const { result } = renderHook(() => useElapsedTime({}));
    expect(result.current).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 0 });
  });

  it("computes elapsed time between startTime and endTime (both unix seconds)", () => {
    const start = 1_700_000_000;
    const end = start + 3661; // 1h 1m 1s later
    const { result } = renderHook(() => useElapsedTime({ startTime: start, endTime: end }));
    expect(result.current).toEqual({ days: 0, hours: 1, minutes: 1, seconds: 1 });
  });

  it("clamps to zero when endTime is before startTime", () => {
    const start = 1_700_000_100;
    const end = 1_700_000_000;
    const { result } = renderHook(() => useElapsedTime({ startTime: start, endTime: end }));
    expect(result.current).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 0 });
  });

  it("ticks every second against `now` when endTime is not provided", () => {
    vi.useFakeTimers();
    const nowSeconds = Math.floor(Date.now() / 1000);
    const start = nowSeconds - 5;
    const { result } = renderHook(() => useElapsedTime({ startTime: start }));
    expect(result.current.seconds).toBe(5);

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current.seconds).toBe(6);
  });

  it("accepts millisecond timestamps for startTime", () => {
    const startMs = 1_700_000_000_000;
    const endMs = startMs + 2000;
    const { result } = renderHook(() =>
      useElapsedTime({ startTime: startMs, endTime: endMs }),
    );
    expect(result.current.seconds).toBe(2);
  });

  it("treats 0 or empty-string startTime as unset", () => {
    const { result: r1 } = renderHook(() => useElapsedTime({ startTime: 0 }));
    expect(r1.current).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 0 });

    const { result: r2 } = renderHook(() => useElapsedTime({ startTime: "" }));
    expect(r2.current).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 0 });
  });

  it("clears the pending timeout on unmount", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(global, "clearTimeout");
    const nowSeconds = Math.floor(Date.now() / 1000);
    const { unmount } = renderHook(() => useElapsedTime({ startTime: nowSeconds }));
    unmount();
    expect(clearSpy).toHaveBeenCalled();
  });
});
