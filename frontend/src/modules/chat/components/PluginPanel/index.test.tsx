import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { PluginPanel } from "./index";
import type { PluginSession, PluginUI } from "@/modules/chat/store/pluginPanel";

const mockUsePluginSession = vi.fn();
const mockUsePluginStore = vi.fn();
const mockFetchPluginUI = vi.fn();
const mockSetFocusedTab = vi.fn();
const mockSetFocusedSortOrder = vi.fn();
const mockBumpDismissedRefresh = vi.fn();
const mockDismissSession = vi.fn();
const mockRefresh = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key),
    i18n: { language: "en-US" },
  }),
}));

vi.mock("@/modules/chat/hooks/usePlugin", () => ({
  usePluginSession: (conversationId: string) => mockUsePluginSession(conversationId),
}));

vi.mock("@/modules/chat/store/pluginPanel", () => ({
  usePluginStore: Object.assign(
    (selector: (s: unknown) => unknown) => mockUsePluginStore(selector),
    { getState: () => mockUsePluginStore((s: unknown) => s) },
  ),
}));

vi.mock("@/modules/chat/utils/chunkUpload", () => ({
  uploadFileInChunks: vi.fn(),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  PluginSessionApi: () => ({
    dismissSession: (...args: unknown[]) => mockDismissSession(...args),
  }),
}));

vi.mock("@/components/StateGraphModal", () => ({
  default: ({ open }: { open: boolean }) =>
    open ? <div data-testid="state-graph-modal" /> : null,
}));

vi.mock("./SlotComponents", () => ({
  SlotRenderer: ({ slot }: { slot: { slot_id: string } }) => (
    <div data-testid="slot-renderer">{slot.slot_id}</div>
  ),
  SlotDownloadContext: { Provider: ({ children }: { children: React.ReactNode }) => <>{children}</> },
  SlotEditingContext: { Provider: ({ children }: { children: React.ReactNode }) => <>{children}</> },
}));

function pluginStoreState(overrides: Record<string, unknown> = {}) {
  return {
    bumpDismissedRefresh: mockBumpDismissedRefresh,
    autoRunningByConversation: {},
    fetchPluginUI: mockFetchPluginUI,
    setFocusedTab: mockSetFocusedTab,
    setFocusedSortOrder: mockSetFocusedSortOrder,
    focusedTabByConversation: {},
    pluginUIByPlugin: {},
    ...overrides,
  };
}

function baseSession(overrides: Partial<PluginSession> = {}): PluginSession {
  return {
    session_id: "sess-1",
    conversation_id: "conv-1",
    plugin_id: "writer-plugin",
    status: "waiting",
    slots: [],
    ...overrides,
  } as PluginSession;
}

describe("PluginPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockFetchPluginUI.mockResolvedValue({} as PluginUI);
    mockUsePluginStore.mockImplementation((selector: (s: unknown) => unknown) =>
      selector(pluginStoreState()),
    );
    mockUsePluginSession.mockReturnValue({
      session: null,
      loading: false,
      refresh: mockRefresh,
    });
  });

  it("renders nothing when there is no active session and not loading", () => {
    const { container } = render(<PluginPanel conversationId="conv-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows a loading placeholder while the session is being fetched for the first time", () => {
    mockUsePluginSession.mockReturnValue({ session: null, loading: true, refresh: mockRefresh });
    render(<PluginPanel conversationId="conv-1" />);
    expect(screen.getByRole("status", { name: "chat.pluginPanelLoading" })).toBeInTheDocument();
  });

  it("renders the panel header with plugin id and status once a session is active", async () => {
    mockUsePluginSession.mockReturnValue({
      session: baseSession(),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    expect(screen.getByText("writer-plugin")).toBeInTheDocument();
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalledWith("writer-plugin"));
  });

  it("renders slots via AutoSlotGrid when the plugin UI has no declared tabs", async () => {
    mockUsePluginSession.mockReturnValue({
      session: baseSession({
        slots: [{ slot_id: "s1", slot: "outline", selected: true, revision: 1 }],
      }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());
    expect(screen.getByTestId("slot-renderer")).toHaveTextContent("s1");
  });

  it("shows a waiting placeholder in AutoSlotGrid when there are no slots yet", async () => {
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ slots: [] }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());
    expect(screen.getByText("chat.pluginWaitingForResults")).toBeInTheDocument();
  });

  it("shows Retry and Continue footer actions while the session is waiting", async () => {
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ status: "waiting" }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());
    expect(screen.getByText("chat.pluginRetry")).toBeInTheDocument();
    expect(screen.getByText("chat.pluginContinue")).toBeInTheDocument();
  });

  it("calls onSendMessage with the continue translation key when Continue is clicked", async () => {
    const onSendMessage = vi.fn();
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ status: "waiting" }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" onSendMessage={onSendMessage} />);
    fireEvent.click(screen.getByText("chat.pluginContinue"));
    await waitFor(() => expect(onSendMessage).toHaveBeenCalledWith("chat.pluginContinue"));
  });

  it("calls onStop when the Stop button is clicked while the session is active", async () => {
    const onStop = vi.fn();
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ status: "active" }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" onStop={onStop} />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());
    fireEvent.click(screen.getByText("chat.pluginStop"));
    expect(onStop).toHaveBeenCalled();
  });

  it("toggles collapsed state when the collapse button is clicked", async () => {
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ slots: [{ slot_id: "s1", slot: "outline", selected: true, revision: 1 }] }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());
    expect(screen.getByTestId("slot-renderer")).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("chat.pluginPanelCollapse"));
    expect(screen.queryByTestId("slot-renderer")).not.toBeInTheDocument();
  });

  it("dismisses the session via a confirm popover, calling the dismiss API and bumping the refresh counter", async () => {
    mockDismissSession.mockResolvedValue({});
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ status: "completed" }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());

    fireEvent.click(screen.getByLabelText("chat.pluginDismissBtn"));
    fireEvent.click(await screen.findByText("chat.pluginDismissConfirmOk"));

    await waitFor(() => expect(mockDismissSession).toHaveBeenCalledWith("sess-1"));
    await waitFor(() => expect(mockBumpDismissedRefresh).toHaveBeenCalledWith("conv-1"));
  });

  it("opens the state graph modal when the status badge is clicked", async () => {
    mockUsePluginSession.mockReturnValue({
      session: baseSession({ status: "waiting" }),
      loading: false,
      refresh: mockRefresh,
    });
    render(<PluginPanel conversationId="conv-1" />);
    await waitFor(() => expect(mockFetchPluginUI).toHaveBeenCalled());
    fireEvent.click(screen.getByTitle("chat.pluginViewWorkflow"));
    expect(screen.getByTestId("state-graph-modal")).toBeInTheDocument();
  });
});
