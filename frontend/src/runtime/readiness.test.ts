import { describe, expect, it, vi } from "vitest";
import {
  RUNTIME_CAPABILITY_SERVICES,
  RuntimeReadinessError,
  resolveRuntimeCapabilityState,
  waitForCapability,
} from "./readiness";
import type { DesktopRuntimeStatus } from "./desktopBridge";

function buildStatus(
  statuses: Record<string, string>,
): DesktopRuntimeStatus {
  const services: Record<string, { status?: string }> = {};
  for (const [name, status] of Object.entries(statuses)) {
    services[name] = { status };
  }
  return { services };
}

describe("resolveRuntimeCapabilityState", () => {
  it("returns ready when every required service is running or ready", () => {
    const status = buildStatus({
      "local-proxy": "running",
      "auth-service": "ready",
      core: "running",
      frontend: "ready",
    });
    expect(resolveRuntimeCapabilityState(status, "configuration")).toBe("ready");
  });

  it("returns starting when a required service has not started yet", () => {
    const status = buildStatus({
      "local-proxy": "running",
      "auth-service": "starting",
      core: "running",
      frontend: "running",
    });
    expect(resolveRuntimeCapabilityState(status, "configuration")).toBe("starting");
  });

  it("returns failed when any required service reports failed, even if others are ready", () => {
    const status = buildStatus({
      "local-proxy": "running",
      "auth-service": "failed",
      core: "running",
      frontend: "running",
    });
    expect(resolveRuntimeCapabilityState(status, "configuration")).toBe("failed");
  });

  it("treats missing service entries as not-ready (starting)", () => {
    const status: DesktopRuntimeStatus = { services: {} };
    expect(resolveRuntimeCapabilityState(status, "parser")).toBe("starting");
  });

  it("exposes the expected required services per capability", () => {
    expect(RUNTIME_CAPABILITY_SERVICES.chat).toContain("chat");
    expect(RUNTIME_CAPABILITY_SERVICES.parser).toContain("lazyllm-parse-worker");
  });
});

describe("RuntimeReadinessError", () => {
  it("carries a code, message, and optional cause", () => {
    const cause = new Error("underlying");
    const error = new RuntimeReadinessError("timeout", "timed out", { cause });
    expect(error.code).toBe("timeout");
    expect(error.message).toBe("timed out");
    expect(error.name).toBe("RuntimeReadinessError");
    expect((error as Error & { cause?: unknown }).cause).toBe(cause);
  });
});

describe("waitForCapability", () => {
  it("resolves once the status reader reports ready", async () => {
    const readStatus = vi
      .fn()
      .mockResolvedValueOnce({ ok: true, data: buildStatus({}) })
      .mockResolvedValueOnce({
        ok: true,
        data: buildStatus({
          "local-proxy": "running",
          "auth-service": "running",
          core: "running",
          frontend: "running",
        }),
      });

    await expect(
      waitForCapability("configuration", readStatus, {
        pollIntervalMs: 1,
        timeoutMs: 1000,
      }),
    ).resolves.toBeUndefined();
    expect(readStatus).toHaveBeenCalledTimes(2);
  });

  it("rejects with a timeout RuntimeReadinessError when the deadline passes", async () => {
    const readStatus = vi.fn().mockResolvedValue({ ok: true, data: buildStatus({}) });

    await expect(
      waitForCapability("configuration", readStatus, {
        pollIntervalMs: 1,
        timeoutMs: 5,
      }),
    ).rejects.toMatchObject({ code: "timeout" });
  });

  it("throws immediately on a failed capability when failFast is set", async () => {
    const readStatus = vi.fn().mockResolvedValue({
      ok: true,
      data: buildStatus({
        "local-proxy": "failed",
        "auth-service": "running",
        core: "running",
        frontend: "running",
      }),
    });

    await expect(
      waitForCapability("configuration", readStatus, { failFast: true }),
    ).rejects.toMatchObject({ code: "failed" });
  });

  it("rejects with an AbortError when the signal is already aborted", async () => {
    const controller = new AbortController();
    controller.abort();
    const readStatus = vi.fn();

    await expect(
      waitForCapability("configuration", readStatus, { signal: controller.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(readStatus).not.toHaveBeenCalled();
  });
});
