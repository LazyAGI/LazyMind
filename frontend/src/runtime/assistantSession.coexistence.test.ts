import { afterEach, beforeEach, expect, it, vi } from "vitest";

beforeEach(() => { vi.resetModules(); vi.stubEnv("VITE_LAZYMIND_ASSISTANT_BRIDGE_URL", ""); });
afterEach(() => { vi.unstubAllEnvs(); vi.restoreAllMocks(); });

it("skips an incompatible default service and sends the session only to the compatible broker", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = String(input);
    if (url.endsWith("/health")) {
      if (url.includes(":19092/")) return new Response(JSON.stringify({ service: "lazymind-assistant-bridge", version: "v9", platform: "linux", isolated: false }));
      return new Response(JSON.stringify({ version: "v8" }));
    }
    expect(url).toBe("http://127.0.0.1:19092/v1/session");
    expect(init?.method).toBe("POST");
    return new Response("{}");
  });
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux");
  const { syncLocalAssistantSession } = await import("./assistantSession");
  await syncLocalAssistantSession({ token: "access", refreshToken: "refresh" }, "http://localhost:8090", 1000);
  expect(fetch.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
});

it("does not replay a mutation after a lost response", async () => {
  vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux");
  const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    if (String(input).endsWith("/health")) return new Response(JSON.stringify({ service: "lazymind-assistant-bridge", version: "v9", platform: "linux", isolated: false }));
    throw new TypeError("connection lost after submission");
  });
  const { assistantBridgeFetch } = await import("./assistantSession");
  await expect(assistantBridgeFetch("/agents/codex/connect", { method: "POST" }, 1000)).rejects.toThrow("connection lost");
  expect(fetch.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
});

it("never submits credentials to an unidentified local listener", async () => {
  const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ ok: true })));
  const { assistantBridgeFetch } = await import("./assistantSession");
  await expect(assistantBridgeFetch("/session", { method: "POST", body: "private" }, 1000)).rejects.toMatchObject({ code: "bridge_unavailable" });
  expect(fetch.mock.calls.every(([, init]) => !init?.body)).toBe(true);
});
