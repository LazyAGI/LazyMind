import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";
import { pickScanAgent, sleep, waitForCloudSyncRun } from "./cloudSync";
import type { ScanV2AgentHint, ScanV2Client } from "./scanAccessors";

const t = ((key: string) => key) as unknown as TFunction;

describe("sleep", () => {
  it("resolves after the given delay using window.setTimeout", async () => {
    vi.useFakeTimers();
    const promise = sleep(1000);
    vi.advanceTimersByTime(1000);
    await expect(promise).resolves.toBeUndefined();
    vi.useRealTimers();
  });
});

describe("pickScanAgent", () => {
  const agents: ScanV2AgentHint[] = [
    { agent_id: "a1", status: "offline" },
    { agent_id: "a2", status: "online" },
    { agent_id: "a3", status: "active" },
  ];

  it("prefers the explicitly preferred agent when present", () => {
    expect(pickScanAgent(agents, "a1")).toEqual(agents[0]);
  });

  it("falls back to an online/active/running agent", () => {
    expect(pickScanAgent(agents, "missing")).toEqual(agents[1]);
  });

  it("falls back to the first agent when none are online", () => {
    const offlineOnly = [{ agent_id: "a1", status: "offline" }];
    expect(pickScanAgent(offlineOnly)).toEqual(offlineOnly[0]);
  });
});

function createMockClient(overrides: Partial<ScanV2Client> = {}) {
  return {
    getSource: vi.fn(),
    getSourceSummary: vi.fn(),
    ...overrides,
  } as unknown as ScanV2Client;
}

describe("waitForCloudSyncRun", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("resolves once the summary reports a completed run", async () => {
    const client = createMockClient({
      getSource: vi.fn().mockResolvedValue({ data: { bindings: [] } }),
      getSourceSummary: vi.fn().mockResolvedValue({
        data: { last_success_at: "2026-01-01T00:00:00Z" },
      }),
    });

    const result = await waitForCloudSyncRun(client, "source-1", t, ["run-1"]);
    expect(result).toEqual({ run_ids: ["run-1"], status: "SUCCEEDED" });
  });

  it("rejects when a binding reports a failed status", async () => {
    const client = createMockClient({
      getSource: vi.fn().mockResolvedValue({
        data: { bindings: [{ status: "FAILED", last_error: "2000123" }] },
      }),
      getSourceSummary: vi.fn().mockResolvedValue({ data: {} }),
    });

    await expect(waitForCloudSyncRun(client, "source-1", t)).rejects.toThrow();
  });

  it("rejects with a timeout error when the deadline passes without success", async () => {
    const client = createMockClient({
      getSource: vi.fn().mockResolvedValue({ data: { bindings: [] } }),
      getSourceSummary: vi.fn().mockResolvedValue({ data: {} }),
    });

    const promise = waitForCloudSyncRun(client, "source-1", t);
    const assertion = expect(promise).rejects.toThrow();
    await vi.runAllTimersAsync();
    await assertion;
  });
});
