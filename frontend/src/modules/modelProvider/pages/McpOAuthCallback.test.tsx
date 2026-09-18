import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
const api = vi.hoisted(() => ({ finishMcpOAuth: vi.fn(), discoverMcpServerTools: vi.fn() }));
vi.mock("@/modules/memory/toolApi", () => ({ ...api, MCP_OAUTH_SERVER_KEY: "mcp-server" }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import McpOAuthCallback from "./McpOAuthCallback";

describe("MCP OAuth callback", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    window.history.replaceState(null, "", "/oauth/mcp/callback?code=secret-code&state=opaque-state");
    api.finishMcpOAuth.mockResolvedValue(undefined);
    api.discoverMcpServerTools.mockResolvedValue({ success: true, tools: [] });
  });
  it("scrubs credentials before submission and discovers without granting tools", async () => {
    sessionStorage.setItem("mcp-server", "owned-server");
    api.finishMcpOAuth.mockImplementation(async () => {
      expect(window.location.search).toBe("");
      expect(sessionStorage.length).toBe(0);
    });
    render(<MemoryRouter><McpOAuthCallback /></MemoryRouter>);
    await screen.findByText("admin.memoryMcpOAuthSuccess");
    expect(screen.getByRole("link")).toHaveAttribute("href", "/settings?section=mcp");
    expect(api.finishMcpOAuth).toHaveBeenCalledWith("owned-server", "secret-code", "opaque-state");
    expect(api.discoverMcpServerTools).toHaveBeenCalledWith("owned-server");
  });
  it("cannot replay after session storage is lost", async () => {
    render(<MemoryRouter><McpOAuthCallback /></MemoryRouter>);
    await screen.findByText("admin.memoryMcpOAuthError");
    expect(api.finishMcpOAuth).not.toHaveBeenCalled();
    expect(window.location.search).toBe("");
  });
  it("drops the code and identity when login expires", async () => {
    sessionStorage.setItem("mcp-server", "owned-server");
    api.finishMcpOAuth.mockRejectedValue({ response: { status: 401 } });
    render(<MemoryRouter><McpOAuthCallback /></MemoryRouter>);
    await screen.findByText("admin.memoryMcpOAuthError");
    expect(sessionStorage.length).toBe(0);
    expect(window.location.search).toBe("");
    expect(api.discoverMcpServerTools).not.toHaveBeenCalled();
  });
  it("distinguishes discovery failure after a successful grant", async () => {
    sessionStorage.setItem("mcp-server", "owned-server");
    api.discoverMcpServerTools.mockRejectedValue(new Error("unavailable"));
    render(<MemoryRouter><McpOAuthCallback /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText("admin.memoryMcpOAuthDiscoverError")).toBeInTheDocument());
  });
});
