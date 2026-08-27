import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  user: vi.fn(),
}));

vi.mock("./mode", () => ({ isDesktopRuntime: () => false }));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: mocks.user },
}));

import {
  agentExecutableBindings,
  agentIntegrationStatuses,
  bindAgentExecutable,
  executorIntegrationAction,
  executorIntegrationPolicies,
} from "./desktopBridge";

describe("browser Assistant Bridge session synchronization", () => {
  beforeEach(() => {
    mocks.user.mockReset();
    vi.restoreAllMocks();
  });

  it("sends the current web session before reading Agent status", async () => {
    mocks.user.mockReturnValue({
      username: "admin",
      token: "access",
      refreshToken: "refresh",
      role: "system-admin",
      tenantId: "tenant",
    });
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ agents: {} }), { status: 200 }));

    const result = await agentIntegrationStatuses();

    expect(result).toEqual({ ok: true, data: {} });
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "http://127.0.0.1:19091/v1/session",
      expect.objectContaining({ method: "POST" }),
    );
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(init.body))).toMatchObject({
      server_url: window.location.origin,
      access_token: "access",
      refresh_token: "refresh",
    });
    expect(fetchMock.mock.calls[1][0]).toBe("http://127.0.0.1:19091/v1/agents");
  });

  it("clears the Bridge session when the web user is signed out", async () => {
    mocks.user.mockReturnValue(null);
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ agents: {} }), { status: 200 }));

    await agentIntegrationStatuses();

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "http://127.0.0.1:19091/v1/session",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("reads executor permissions from the local Bridge", async () => {
    mocks.user.mockReturnValue(null);
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({
        executors: { codex: { provider: "codex", enabled: false } },
      }), { status: 200 }));

    const result = await executorIntegrationPolicies();

    expect(result).toEqual({
      ok: true,
      data: { codex: { provider: "codex", enabled: false } },
    });
    expect(fetchMock.mock.calls[0][0]).toBe("http://127.0.0.1:19091/v1/executors");
  });

  it("changes one executor permission through the local Bridge", async () => {
    mocks.user.mockReturnValue(null);
    const fetchMock = vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(new Response(JSON.stringify({
        provider: "workbuddy", enabled: false,
      }), { status: 200 }));

    const result = await executorIntegrationAction("workbuddy", "disable");

    expect(result).toEqual({
      ok: true,
      data: { provider: "workbuddy", enabled: false },
    });
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "http://127.0.0.1:19091/v1/executors/workbuddy/disable",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("reads host-local executable bindings without syncing account credentials", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ bindings: { "codex-cli": "D:\\Agents\\codex.cmd" } }), { status: 200 }),
    );

    const result = await agentExecutableBindings();

    expect(result).toEqual({ ok: true, data: { "codex-cli": "D:\\Agents\\codex.cmd" } });
    expect(fetchMock.mock.calls[0][0]).toBe("http://127.0.0.1:19091/v1/bindings");
  });

  it("saves a host-local executable binding", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({
        target: "cursor-cli", configured: true, path: "D:\\Agents\\cursor-agent.exe",
      }), { status: 200 }),
    );

    const result = await bindAgentExecutable("cursor-cli", "D:\\Agents\\cursor-agent.exe");

    expect(result).toEqual({
      ok: true,
      data: { target: "cursor-cli", configured: true, path: "D:\\Agents\\cursor-agent.exe" },
    });
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(fetchMock.mock.calls[0][0]).toBe("http://127.0.0.1:19091/v1/bindings/cursor-cli");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({ path: "D:\\Agents\\cursor-agent.exe" });
  });
});
