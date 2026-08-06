import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, waitFor, fireEvent } from "@/test/testUtils";
import ChatConfigPopover from "./ChatConfigModal";

const mockGetConversationDetail = vi.fn();
const mockGetChatSettings = vi.fn();
const mockPatchPluginSettings = vi.fn();

vi.mock("../../utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceGetConversationDetail: (...args: unknown[]) =>
      mockGetConversationDetail(...args),
  }),
  ConversationSettingsApi: () => ({
    getChatSettings: (...args: unknown[]) => mockGetChatSettings(...args),
    patchPluginSettings: (...args: unknown[]) => mockPatchPluginSettings(...args),
  }),
  parseConversationPluginSettings: (conversation: unknown) =>
    (conversation as { plugin_settings?: unknown })?.plugin_settings ?? null,
}));

describe("ChatConfigPopover", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetChatSettings.mockResolvedValue({ data: {} });
    mockPatchPluginSettings.mockResolvedValue({});
  });

  it("renders the trigger", () => {
    renderWithProviders(<ChatConfigPopover />);
    expect(screen.getByText("chat.conversationConfig")).toBeInTheDocument();
  });

  it("fetches default settings from the server on first open for a new conversation", async () => {
    mockGetChatSettings.mockResolvedValue({ data: { enable_subagent: false } });
    renderWithProviders(<ChatConfigPopover />);
    fireEvent.click(screen.getByText("chat.conversationConfig"));
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByText("chat.conversationConfigEnableSubagent")).toBeInTheDocument(),
    );
  });

  it("fetches conversation-specific settings when a real conversation id is provided", async () => {
    mockGetConversationDetail.mockResolvedValue({
      data: { conversation: { plugin_settings: { enable_plugin: false } } },
    });
    renderWithProviders(<ChatConfigPopover conversationId="conv-1" />);
    fireEvent.click(screen.getByText("chat.conversationConfig"));
    await waitFor(() =>
      expect(mockGetConversationDetail).toHaveBeenCalledWith({ conversation: "conv-1" }),
    );
    expect(mockGetChatSettings).not.toHaveBeenCalled();
  });

  it("does not fetch from server for temp conversation ids", async () => {
    renderWithProviders(<ChatConfigPopover conversationId="temp_abc" />);
    fireEvent.click(screen.getByText("chat.conversationConfig"));
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());
    expect(mockGetConversationDetail).not.toHaveBeenCalled();
  });

  it("saves plugin execution mode changes and persists them for a real conversation", async () => {
    const onSave = vi.fn();
    renderWithProviders(
      <ChatConfigPopover conversationId="conv-1" initialSettings={{ enable_plugin: true, plugin_mode: "auto" }} onSave={onSave} />,
    );
    fireEvent.click(screen.getByText("chat.conversationConfig"));
    await waitFor(() =>
      expect(screen.getByText("chat.conversationConfigPluginApproval")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("chat.conversationConfigPluginApproval"));
    await waitFor(() =>
      expect(mockPatchPluginSettings).toHaveBeenCalledWith(
        "conv-1",
        expect.objectContaining({ enable_plugin: true, plugin_mode: "dynamic" }),
      ),
    );
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ plugin_mode: "dynamic" }),
    );
  });

  it("toggles the subagent switch", async () => {
    const onSave = vi.fn();
    renderWithProviders(
      <ChatConfigPopover initialSettings={{ enable_subagent: true }} onSave={onSave} />,
    );
    fireEvent.click(screen.getByText("chat.conversationConfig"));
    await waitFor(() =>
      expect(screen.getByText("chat.conversationConfigEnableSubagent")).toBeInTheDocument(),
    );
    const switchInput = document.querySelector(".ant-switch") as HTMLElement;
    fireEvent.click(switchInput);
    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ enable_subagent: false })),
    );
  });

  it("disables the 'disabled' execution mode option when a plugin session is active", async () => {
    renderWithProviders(
      <ChatConfigPopover initialSettings={{ enable_plugin: true }} hasPluginSession />,
    );
    fireEvent.click(screen.getByText("chat.conversationConfig"));
    await waitFor(() =>
      expect(screen.getByText("chat.conversationConfigPluginDisabled")).toBeInTheDocument(),
    );
    const disabledOption = screen
      .getByText("chat.conversationConfigPluginDisabled")
      .closest("label");
    expect(disabledOption).toHaveClass("ant-segmented-item-disabled");
  });
});
