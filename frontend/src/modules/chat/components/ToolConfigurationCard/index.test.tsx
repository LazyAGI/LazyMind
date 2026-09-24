import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
    expect(onContinue).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByText("toolConfiguration.continue"));
    expect(onContinue).toHaveBeenCalledOnce();
  });
  it("uses fixed configuration routes, never a URL supplied by an action", () => {
    expect(configurationPath("mcp:untrusted")).toBe("/settings?section=mcp");
    expect(configurationPath("https://example.com")).toBe("/settings?section=system_tools");
  });
});

const tick = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); };
const update = () => window.dispatchEvent(new CustomEvent("tool-configuration-updated", { detail: { conversationId: "c", historyId: "h" } }));

describe("configuration polling lifecycle", () => {
  let visibility: DocumentVisibilityState;
  beforeEach(() => {
    vi.useFakeTimers();
    get.mockReset();
    visibility = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  });
  afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });
  const setVisible = (value: DocumentVisibilityState) => {
    visibility = value;
    document.dispatchEvent(new Event("visibilitychange"));
  };
  it.each(["needs_configuration", "unavailable"])("polls ended %s cards every 30 seconds", async (status: string) => {
    get.mockResolvedValue(response([{ ...action, status }]));
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    await tick();
    await tick(29999);
    expect(get).toHaveBeenCalledTimes(1);
    await tick(1);
    expect(get).toHaveBeenCalledTimes(2);
  });
  it.each(["ready", "forbidden"])("keeps active polling at five seconds and stops ended %s cards", async (status: string) => {
    get.mockResolvedValue(response([{ ...action, status }]));
    const view = render(<ToolConfigurationCard conversationId="c" historyId="h" active />);
    await tick();
    await tick(5000);
    expect(get).toHaveBeenCalledTimes(2);
    view.rerender(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    await tick();
    const settled = get.mock.calls.length;
    await tick(60000);
    expect(get).toHaveBeenCalledTimes(settled);
  });
  it("pauses hidden polling and refreshes immediately on visibility restoration", async () => {
    get.mockResolvedValue(response());
    render(<ToolConfigurationCard conversationId="c" historyId="h" active />);
    await tick();
    act(() => setVisible("hidden"));
    await tick(60000);
    expect(get).toHaveBeenCalledTimes(1);
    act(() => setVisible("visible"));
    await tick();
    expect(get).toHaveBeenCalledTimes(2);
    await tick(5000);
    expect(get).toHaveBeenCalledTimes(3);
  });
  it("stops ended polling when a pending action becomes ready without continuing the task", async () => {
    get.mockResolvedValueOnce(response());
    get.mockResolvedValue(response([{ ...action, status: "ready", version: 2 }]));
    const onContinue = vi.fn();
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} onContinue={onContinue} />);
    await tick();
    await tick(30000);
    expect(screen.getByText("toolConfiguration.continue")).toBeInTheDocument();
    await tick(60000);
    expect(get).toHaveBeenCalledTimes(2);
    expect(onContinue).not.toHaveBeenCalled();
  });
  it("does not restart a timer when an in-flight request finishes while hidden", async () => {
    let resolve!: (value: ReturnType<typeof response>) => void;
    get.mockImplementation(() => new Promise((done) => { resolve = done; }));
    render(<ToolConfigurationCard conversationId="c" historyId="h" active />);
    act(() => setVisible("hidden"));
    await act(async () => resolve(response()));
    await tick(60000);
    expect(get).toHaveBeenCalledTimes(1);
  });
  it("shares an in-flight poll with configuration clicks and repeated update events", async () => {
    get.mockResolvedValueOnce(response());
    let resolve!: (value: ReturnType<typeof response>) => void;
    get.mockImplementation(() => new Promise((done) => { resolve = done; }));
    const destination = { opener: window, close: vi.fn(), location: { replace: vi.fn() } };
    vi.spyOn(window, "open").mockReturnValue(destination as unknown as Window);
    render(<ToolConfigurationCard conversationId="c" historyId="h" active />);
    await tick();
    await tick(5000);
    fireEvent.click(screen.getByText("toolConfiguration.connectMail"));
    act(() => { update(); update(); });
    await tick(15000);
    expect(get).toHaveBeenCalledTimes(2);
    await act(async () => resolve(response([{ ...action, status: "ready", version: 2 }])));
    expect(screen.getByText("toolConfiguration.ready")).toBeInTheDocument();
    expect(destination.close).toHaveBeenCalledOnce();
    expect(destination.location.replace).not.toHaveBeenCalled();
    await tick(5000);
    expect(get).toHaveBeenCalledTimes(3);
  });
  it("retries failed ended status checks at 30 seconds", async () => {
    get.mockRejectedValue(new Error("offline"));
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    await tick();
    await tick(30000);
    expect(get).toHaveBeenCalledTimes(2);
  });
  it("preserves newer versions when opening returns an older action", async () => {
    get.mockResolvedValueOnce(response([{ ...action, version: 3 }]));
    get.mockResolvedValue(response([{ ...action, status: "ready", version: 2 }]));
    const destination = { opener: window, close: vi.fn(), location: { replace: vi.fn() } };
    vi.spyOn(window, "open").mockReturnValue(destination as unknown as Window);
    render(<ToolConfigurationCard conversationId="c" historyId="h" active={false} />);
    await tick();
    fireEvent.click(screen.getByText("toolConfiguration.connectMail"));
    await tick();
    expect(screen.getByText("toolConfiguration.pending")).toBeInTheDocument();
    expect(destination.location.replace).toHaveBeenCalledWith("/cloud-documents/mail");
  });
  it("ignores stale results after history changes without overlapping requests", async () => {
    let resolve!: (value: ReturnType<typeof response>) => void;
    get.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    get.mockResolvedValue(response([{ ...action, history_id: "new", label: "New mailbox" }]));
    const view = render(<ToolConfigurationCard conversationId="c" historyId="h" active />);
    await tick();
    view.rerender(<ToolConfigurationCard conversationId="c" historyId="new" active />);
    await tick();
    expect(get).toHaveBeenCalledTimes(1);
    await act(async () => resolve(response()));
    await tick();
    expect(screen.queryByText("Mailbox")).toBeNull();
    expect(screen.getByText("New mailbox")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
  });
  it("does not refresh or navigate after unmount", async () => {
    get.mockResolvedValueOnce(response());
    let resolve!: (value: ReturnType<typeof response>) => void;
    get.mockImplementation(() => new Promise((done) => { resolve = done; }));
    const destination = { opener: window, close: vi.fn(), location: { replace: vi.fn() } };
    vi.spyOn(window, "open").mockReturnValue(destination as unknown as Window);
    const view = render(<ToolConfigurationCard conversationId="c" historyId="h" active />);
    await tick();
    fireEvent.click(screen.getByText("toolConfiguration.connectMail"));
    view.unmount();
    await act(async () => resolve(response()));
    act(() => { update(); setVisible("visible"); });
    await tick(60000);
    expect(get).toHaveBeenCalledTimes(2);
    expect(destination.close).toHaveBeenCalledOnce();
    expect(destination.location.replace).not.toHaveBeenCalled();
  });
});
