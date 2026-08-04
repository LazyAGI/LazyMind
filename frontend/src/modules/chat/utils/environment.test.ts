import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildEnvironmentContext } from "./environment";

describe("buildEnvironmentContext", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-03T06:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("defaults locale to zh-CN when omitted", () => {
    const context = buildEnvironmentContext();
    expect(context.locale).toBe("zh-CN");
  });

  it("uses the provided locale when given", () => {
    const context = buildEnvironmentContext("en-US");
    expect(context.locale).toBe("en-US");
  });

  it("stamps the current time as an ISO string and includes a timezone", () => {
    const context = buildEnvironmentContext();
    expect(context.time.now).toBe("2026-08-03T06:00:00.000Z");
    expect(typeof context.time.timezone).toBe("string");
    expect(context.time.timezone.length).toBeGreaterThan(0);
  });
});
