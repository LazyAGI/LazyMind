import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ToolConfigurationCard, { configurationPath } from "./index";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get } }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
const action = { id: "a", history_id: "h", service: "mail", label: "Mailbox", status: "needs_configuration", version: 1 };
const response = (actions = [action]) => ({ data: { data: { actions } } });

describe("configuration cards", () => {
  beforeEach(() => { get.mockReset(); vi.restoreAllMocks(); });
  it("dedicates one mailbox entry and rechecks before opening configuration", async () => {
    get.mockResolvedValue(response());
    const destination = { opener: window, close: vi.fn(), location: { replace: vi.fn() } };
    vi.spyOn(window, "open").mockReturnValue(destination as unknown as Window);
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    fireEvent.click(await screen.findByText("toolConfiguration.connectMail"));
    await waitFor(() => expect(destination.location.replace).toHaveBeenCalledWith("/cloud-documents/mail"));
    expect(get).toHaveBeenCalledTimes(2);
    expect(get.mock.calls[0][1].params).toEqual({ history_id: "h" });
  });
  it("does not start OAuth when a historical card is already ready", async () => {
    get.mockResolvedValueOnce(response()).mockResolvedValue(response([{ ...action, status: "ready", version: 2 }]));
    const destination = { opener: window, close: vi.fn(), location: { replace: vi.fn() } };
    vi.spyOn(window, "open").mockReturnValue(destination as unknown as Window);
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    fireEvent.click(await screen.findByText("toolConfiguration.connectMail"));
    await screen.findByText("toolConfiguration.ready");
    expect(destination.close).toHaveBeenCalled();
    expect(destination.location.replace).not.toHaveBeenCalled();
  });
  it("refreshes only the matching task on a structured event", async () => {
    get.mockResolvedValue(response([]));
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    window.dispatchEvent(new CustomEvent("tool-configuration-updated", { detail: { conversationId: "other", historyId: "h" } }));
    expect(get).toHaveBeenCalledTimes(1);
    get.mockResolvedValue(response());
    window.dispatchEvent(new CustomEvent("tool-configuration-updated", { detail: { conversationId: "c", historyId: "h" } }));
    await screen.findByText("Mailbox");
  });
  it("opens OAuth directly for an unconnected MCP and preserves the conversation", async () => {
    get.mockResolvedValue(response([{ ...action, service: "mcp:personal-notion", status: "needs_authorization" }]));
    const destination = { opener: window, close: vi.fn(), location: { replace: vi.fn() } };
    vi.spyOn(window, "open").mockReturnValue(destination as unknown as Window);
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    fireEvent.click(await screen.findByText("toolConfiguration.authorize"));
    await waitFor(() => expect(destination.location.replace).toHaveBeenCalledWith("/oauth/mcp/connect?server=personal-notion&conversation=c"));
  });
  it("offers explicit continuation only when a connection is ready and the task ended", async () => {
    get.mockResolvedValue(response([{ ...action, service: "mcp:notion", status: "ready" }]));
    const onContinue = vi.fn();
    const view = render(<ToolConfigurationCard conversationId="c" historyId="h" active onContinue={onContinue} />);
    await screen.findByText("toolConfiguration.ready");
    expect(screen.queryByText("toolConfiguration.continue")).toBeNull();
    view.rerender(<ToolConfigurationCard conversationId="c" historyId="h" active={false} onContinue={onContinue} />);
    fireEvent.click(await screen.findByText("toolConfiguration.continue"));
    expect(onContinue).toHaveBeenCalledOnce();
  });
  it("uses fixed configuration routes, never a URL supplied by an action", () => {
    expect(configurationPath("mcp:untrusted")).toBe("/settings?section=mcp");
    expect(configurationPath("https://example.com")).toBe("/settings?section=system_tools");
  });
});
