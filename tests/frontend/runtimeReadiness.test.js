import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import {
  resolveRuntimeCapabilityState,
  shouldWaitForRuntimeCapability,
  waitForCapability,
} from "../../frontend/src/runtime/readiness.ts";

function status(readyServices, overallStatus = "starting") {
  const allServices = [
    "process-supervisor",
    "local-proxy",
    "auth-service",
    "core",
    "frontend",
    "lazyllm-algo",
    "chat",
    "lazyllm-doc-server",
    "lazyllm-parse-server",
    "lazyllm-parse-worker",
  ];
  return {
    overallStatus,
    services: Object.fromEntries(
      allServices.map((name) => [
        name,
        { status: readyServices.includes(name) ? "running" : "starting" },
      ]),
    ),
  };
}

const uiServices = [
  "process-supervisor",
  "local-proxy",
  "auth-service",
  "core",
  "frontend",
];

describe("desktop runtime readiness", () => {
  it.each([
    ["cloud", false],
    ["local", false],
    ["desktop", true],
  ])("waits for the sidecar only in %s mode", (mode, expected) => {
    expect(shouldWaitForRuntimeCapability(mode)).toBe(expected);
  });

  it("makes configuration available before chat services finish starting", () => {
    expect(
      resolveRuntimeCapabilityState(status(uiServices), "configuration"),
    ).toBe("ready");
    expect(resolveRuntimeCapabilityState(status(uiServices), "chat")).toBe(
      "starting",
    );
  });

  it("tracks chat and parser independently", () => {
    expect(
      resolveRuntimeCapabilityState(
        status([...uiServices, "chat"]),
        "chat",
      ),
    ).toBe("ready");
    expect(
      resolveRuntimeCapabilityState(
        status([...uiServices, "chat"]),
        "parser",
      ),
    ).toBe("starting");
  });

  it("keeps base chat available when the knowledge algorithm fails", () => {
    const runtimeStatus = status([...uiServices, "chat"], "failed");
    runtimeStatus.services["lazyllm-algo"].status = "failed";

    expect(resolveRuntimeCapabilityState(runtimeStatus, "chat")).toBe("ready");
    expect(resolveRuntimeCapabilityState(runtimeStatus, "parser")).toBe(
      "failed",
    );
  });

  it("does not block functional capabilities on stale supervisor metadata", () => {
    const runtimeStatus = status(
      [...uiServices, "chat"],
      "stale",
    );
    runtimeStatus.services["process-supervisor"].status = "stale";

    expect(resolveRuntimeCapabilityState(runtimeStatus, "chat")).toBe("ready");
  });

  it("keeps polling when a required service is temporarily stale", async () => {
    const staleStatus = status(uiServices);
    staleStatus.services.chat.status = "stale";
    const readStatus = vi
      .fn()
      .mockResolvedValueOnce({ ok: true, data: staleStatus })
      .mockResolvedValueOnce({
        ok: true,
        data: status([...uiServices, "chat"]),
      });

    await waitForCapability("chat", readStatus, {
      pollIntervalMs: 0,
      timeoutMs: 100,
    });

    expect(readStatus).toHaveBeenCalledTimes(2);
  });

  it("allows a temporarily failed service to recover before timing out", async () => {
    const failedStatus = status(uiServices);
    failedStatus.services.chat.status = "failed";
    const readStatus = vi
      .fn()
      .mockResolvedValueOnce({ ok: true, data: failedStatus })
      .mockResolvedValueOnce({
        ok: true,
        data: status([...uiServices, "chat"]),
      });

    await waitForCapability("chat", readStatus, {
      pollIntervalMs: 0,
      timeoutMs: 100,
    });

    expect(readStatus).toHaveBeenCalledTimes(2);
  });

  it("waits without reporting an error and dispatches once when ready", async () => {
    const onWaiting = vi.fn();
    const readStatus = vi
      .fn()
      .mockResolvedValueOnce({ ok: true, data: status(uiServices) })
      .mockResolvedValueOnce({
        ok: true,
        data: status([...uiServices, "chat"]),
      });

    await waitForCapability("chat", readStatus, {
      onWaiting,
      pollIntervalMs: 0,
      timeoutMs: 100,
    });

    expect(onWaiting).toHaveBeenCalledTimes(1);
    expect(readStatus).toHaveBeenCalledTimes(2);
  });

  it("fails immediately when a required service reports failure", async () => {
    const failedStatus = status(uiServices);
    failedStatus.services.chat.status = "failed";

    await expect(
      waitForCapability(
        "chat",
        async () => ({ ok: true, data: failedStatus }),
        { failFast: true, pollIntervalMs: 0, timeoutMs: 100 },
      ),
    ).rejects.toMatchObject({ code: "failed" });
  });

  it("times out instead of dispatching while services remain unavailable", async () => {
    await expect(
      waitForCapability(
        "parser",
        async () => ({ ok: true, data: status(uiServices) }),
        { pollIntervalMs: 0, timeoutMs: 0 },
      ),
    ).rejects.toMatchObject({ code: "timeout" });
  });

  it("supports cancelling a queued operation", async () => {
    const controller = new AbortController();
    controller.abort();

    await expect(
      waitForCapability(
        "chat",
        async () => ({ ok: true, data: status(uiServices) }),
        { signal: controller.signal },
      ),
    ).rejects.toMatchObject({ name: "AbortError" });
  });

  it("checks model availability after configuration services are ready", () => {
    const guardSource = readFileSync(
      new URL(
        "../../frontend/src/modules/chat/hooks/useChatModelProviderGuard.ts",
        import.meta.url,
      ),
      "utf8",
    );

    const configurationWaitIndex = guardSource.indexOf(
      'waitForRuntimeCapability("configuration"',
    );
    const modelRequestIndex = guardSource.indexOf(
      "fetchCurrentUser,",
      configurationWaitIndex,
    );

    expect(configurationWaitIndex).toBeGreaterThan(-1);
    expect(configurationWaitIndex).toBeLessThan(modelRequestIndex);
    expect(guardSource).toContain(
      'waitForRuntimeCapability("chat", { signal: controller.signal })',
    );
  });

  it("retries transient desktop configuration requests without showing an error", () => {
    const guardSource = readFileSync(
      new URL(
        "../../frontend/src/modules/chat/hooks/useChatModelProviderGuard.ts",
        import.meta.url,
      ),
      "utf8",
    );

    expect(guardSource).toContain("requestWithStartupRetry");
    expect(guardSource).toContain(
      "setStatus(desktopRuntime ? \"loading\" : \"error\")",
    );
    expect(guardSource).toContain("if (!desktopRuntime) {");
  });

  it("keeps an early welcome message queued until chat becomes available", () => {
    const chatLayoutSource = readFileSync(
      new URL(
        "../../frontend/src/modules/chat/pages/chatLayout/index.tsx",
        import.meta.url,
      ),
      "utf8",
    );

    expect(chatLayoutSource).toContain("if (pendingMessage && chatEnabled)");
  });
});
