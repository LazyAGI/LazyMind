import { describe, expect, it, vi } from "vitest";
import {
  recoveryActionAfterFailure,
  recoveryDelayForAttempt,
  StreamRecoveryRegistry,
  isTemporaryStreamFailure,
  preserveProviderRetryAfterReconciliation,
} from "./streamRecovery";

describe("stream recovery policy", () => {
  it("retries transient SSE failures and reconciles permanent client failures", () => {
    for (const status of [0, 408, 425, 429, 500, 502, 503]) {
      expect(isTemporaryStreamFailure(status)).toBe(true);
    }
    for (const status of [400, 401, 403, 404, 409, 422]) {
      expect(isTemporaryStreamFailure(status)).toBe(false);
    }
  });

  it("uses five bounded attempts, reconciliation, then three ten-second attempts", () => {
    expect(Array.from({ length: 8 }, (_, index) => recoveryDelayForAttempt(index + 1))).toEqual([
      1_000,
      2_000,
      4_000,
      8_000,
      10_000,
      10_000,
      10_000,
      10_000,
    ]);
    expect(recoveryActionAfterFailure(4)).toBe("retry");
    expect(recoveryActionAfterFailure(5)).toBe("reconcile");
    expect(recoveryActionAfterFailure(6)).toBe("retry");
    expect(recoveryActionAfterFailure(7)).toBe("retry");
    expect(recoveryActionAfterFailure(8)).toBe("final-reconcile");
  });

  it("keeps attempts and timers isolated by conversation", () => {
    vi.useFakeTimers();
    const registry = new StreamRecoveryRegistry();
    const first = registry.ensure("conversation-1");
    const second = registry.ensure("conversation-2");
    first.attempt = 4;
    second.attempt = 1;
    first.timer = setTimeout(() => {}, 1_000);
    second.timer = setTimeout(() => {}, 2_000);

    registry.clear("conversation-1");

    expect(registry.get("conversation-1")).toBeUndefined();
    expect(registry.get("conversation-2")?.attempt).toBe(1);
    expect(vi.getTimerCount()).toBe(1);
    registry.clearAll();
    expect(vi.getTimerCount()).toBe(0);
    vi.useRealTimers();
  });

  it("does not let LazyMind reconciliation clear provider retry state", () => {
    const retry = { retry_index: 1, max_attempts: 3 };
    const cached = [{ role: "assistant", model_retry: retry }];
    expect(
      preserveProviderRetryAfterReconciliation(
        [{ role: "assistant", delta: "partial" }],
        cached,
        "assistant",
      )[0].model_retry,
    ).toEqual(retry);
    expect(
      preserveProviderRetryAfterReconciliation(
        [{ role: "assistant", run_status: "failed" }],
        cached,
        "assistant",
      )[0].model_retry,
    ).toBeUndefined();
  });
});
