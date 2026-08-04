import { afterEach, describe, expect, it, vi } from "vitest";
import Polling from "./polling";

describe("Polling", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("calls request immediately and schedules the next loop on success", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockResolvedValue({ ok: true });
    const onSuccess = vi.fn();
    const polling = new Polling();

    polling.start({ interval: 1000, request, onSuccess });

    await vi.advanceTimersByTimeAsync(0);
    expect(request).toHaveBeenCalledTimes(1);
    expect(onSuccess).toHaveBeenCalledWith({ ok: true });

    await vi.advanceTimersByTimeAsync(1000);
    expect(request).toHaveBeenCalledTimes(2);
  });

  it("invokes onError and keeps polling when the request rejects", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockRejectedValue(new Error("boom"));
    const onError = vi.fn();
    const polling = new Polling();

    polling.start({ interval: 500, request, onError });

    await vi.advanceTimersByTimeAsync(0);
    expect(onError).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(500);
    expect(request).toHaveBeenCalledTimes(2);
  });

  it("stops scheduling further loops after cancel is called", async () => {
    vi.useFakeTimers();
    const request = vi.fn().mockResolvedValue({});
    const onSuccess = vi.fn();
    const polling = new Polling();

    polling.start({ interval: 1000, request, onSuccess });
    await vi.advanceTimersByTimeAsync(0);
    expect(request).toHaveBeenCalledTimes(1);

    polling.cancel();
    await vi.advanceTimersByTimeAsync(5000);
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("ignores a resolved response from a loop that was cancelled mid-flight", async () => {
    vi.useFakeTimers();
    let resolveRequest: (value: unknown) => void = () => {};
    const request = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveRequest = resolve;
        }),
    );
    const onSuccess = vi.fn();
    const polling = new Polling();

    polling.start({ interval: 1000, request, onSuccess });
    polling.cancel();
    resolveRequest({ ok: true });
    await vi.advanceTimersByTimeAsync(0);

    expect(onSuccess).not.toHaveBeenCalled();
  });
});
