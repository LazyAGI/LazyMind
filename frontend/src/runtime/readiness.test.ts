import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ status: vi.fn(), mode: vi.fn() }));
vi.mock("./desktopBridge", () => ({ runtimeStatus: mocks.status }));
vi.mock("./mode", () => ({ getRuntimeMode: mocks.mode }));
import { waitForRuntimeCapability } from "./readiness";

const healthy = () => ({ status: "ok", routes: [
  { name: "authservice-route", enabled: true, health: "healthy", healthCode: 200 },
  { name: "core-route", enabled: true, health: "healthy", healthCode: 200 },
  { name: "chat-route", enabled: true, health: "healthy", healthCode: 200 },
] });

describe("desktop readiness in an external browser", () => {
  beforeEach(() => {
    mocks.mode.mockReturnValue("desktop");
    mocks.status.mockResolvedValue({ ok: false, reason: "unavailable" });
  });
  afterEach(() => vi.restoreAllMocks());

  it.each(["configuration", "chat"] as const)("checks real proxy health for %s without IPC", async capability => {
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(healthy())));
    await expect(waitForRuntimeCapability(capability, { timeoutMs: 0 })).resolves.toBeUndefined();
    expect(fetch).toHaveBeenCalledWith("/_local/status", expect.objectContaining({ cache: "no-store" }));
  });

  it.each(["missing", "unhealthy", "disabled", "malformed", "http-error", "parser"])("does not assume readiness for %s", async failure => {
    const data = healthy();
    if (failure === "missing") data.routes.pop();
    if (failure === "unhealthy") data.routes[2].health = "unreachable";
    if (failure === "disabled") data.routes[2].enabled = false;
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(failure === "malformed" ? {} : data), { status: failure === "http-error" ? 503 : 200 }));
    await expect(waitForRuntimeCapability(failure === "parser" ? "parser" : "chat", { timeoutMs: 0 })).rejects.toMatchObject({ code: "timeout" });
  });

  it("retains Electron readiness and does not bypass a failed IPC call", async () => {
    const fetch = vi.spyOn(globalThis, "fetch");
    mocks.status.mockResolvedValue({ ok: true, data: { services: { chat: { status: "failed" } } } });
    await expect(waitForRuntimeCapability("chat", { failFast: true })).rejects.toMatchObject({ code: "failed" });
    mocks.status.mockResolvedValue({ ok: false, reason: "failed" });
    await expect(waitForRuntimeCapability("chat", { timeoutMs: 0 })).rejects.toMatchObject({ code: "timeout" });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("rechecks unhealthy upstreams until chat is ready", async () => {
    const starting = healthy();
    starting.routes[2].health = "unreachable";
    const fetch = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify(starting)))
      .mockResolvedValueOnce(new Response(JSON.stringify(healthy())));
    await expect(waitForRuntimeCapability("chat", { pollIntervalMs: 1 })).resolves.toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("cancels an outstanding browser health request", async () => {
    const controller = new AbortController();
    vi.spyOn(globalThis, "fetch").mockImplementation((_url, options) => new Promise((_resolve, reject) => {
      options?.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
      controller.abort();
    }));
    await expect(waitForRuntimeCapability("chat", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
  });

  it("leaves cloud and local readiness unchanged", async () => {
    const fetch = vi.spyOn(globalThis, "fetch");
    for (const mode of ["cloud", "local"]) {
      mocks.mode.mockReturnValue(mode);
      await waitForRuntimeCapability("chat");
    }
    expect(fetch).not.toHaveBeenCalled();
  });
});
