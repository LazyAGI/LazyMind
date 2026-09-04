import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import {
  isPerformanceStatsEnabled,
  PERFORMANCE_STATS_EVENT,
  persistPerformanceStatsEnabled,
  setPerformanceStatsEnabled,
} from "./performanceStatsPreference";

describe("performanceStatsPreference", () => {
  const memory = new Map<string, string>();

  beforeAll(() => {
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => memory.get(key) ?? null,
        setItem: (key: string, value: string) => memory.set(key, value),
        removeItem: (key: string) => memory.delete(key),
      },
    });
  });

  afterEach(() => memory.clear());

  it("is off by default and broadcasts changes", () => {
    const listener = vi.fn();
    window.addEventListener(PERFORMANCE_STATS_EVENT, listener);
    expect(isPerformanceStatsEnabled()).toBe(false);
    setPerformanceStatsEnabled(true);
    expect(isPerformanceStatsEnabled()).toBe(true);
    expect(listener).toHaveBeenCalledOnce();
    window.removeEventListener(PERFORMANCE_STATS_EVENT, listener);
  });

  it("persists the server preference and updates local state", async () => {
    vi.mock("@/modules/user/uiPreferencesApi", () => ({
      patchUserUiPreferences: vi.fn().mockResolvedValue({}),
    }));
    await persistPerformanceStatsEnabled(true);
    expect(isPerformanceStatsEnabled()).toBe(true);
  });
});
