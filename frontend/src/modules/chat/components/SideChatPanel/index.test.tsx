import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import {
  createRef,
  forwardRef,
  useEffect,
  useImperativeHandle,
  type ComponentProps,
} from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SideChatPanel from "./index";
import {
  createSideChat,
  deleteSideChat,
  patchSideChatThinkingDepth,
  retainSideChat,
} from "./api";

const mocks = vi.hoisted(() => ({
  latestChatProps: null as any,
  closeStream: vi.fn(),
  warning: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  chatMounts: 0,
  chatUnmounts: 0,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, params?: { name?: string }) =>
      key === "chat.sideChat.sourceFromConversation"
        ? `来自${params?.name}`
        : key,
  }),
}));

vi.mock("@/i18n", () => ({
  default: { language: "zh-CN", resolvedLanguage: "zh-CN", exists: (key: string) => key === "errors.2000509", t: (key: string) => key },
}));

vi.mock("antd", async (importOriginal) => {
  const actual = await importOriginal<typeof import("antd")>();
  return {
    ...actual,
    Modal: (props: ComponentProps<typeof actual.Modal>) => (
      <actual.Modal {...props} transitionName="" maskTransitionName="" />
    ),
    message: {
      warning: mocks.warning,
      success: mocks.success,
      error: mocks.error,
    },
  };
});

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({}) },
}));

vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: {
    getState: () => ({ thinkingDepth: "medium" }),
  },
}));

vi.mock("@/modules/chat/utils/StreamManager", () => ({
  streamManager: { closeAndCleanup: mocks.closeStream },
}));

vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { POST: "POST" },
  SSE: class MockSSE {
    readyState = 1;
    constructor(
      public url: string,
      public options: Record<string, any>,
    ) {}
    addEventListener() {}
    removeEventListener() {}
    close() {
      this.readyState = 2;
    }
  },
}));

vi.mock("../newChatContainer", () => ({
  default: forwardRef(function MockChatContainer(props: any, ref) {
    mocks.latestChatProps = props;
    useImperativeHandle(ref, () => ({ focusInput: vi.fn() }));
    useEffect(() => {
      mocks.chatMounts += 1;
      return () => {
        mocks.chatUnmounts += 1;
      };
    }, []);
    return <div data-testid="side-chat-conversation" />;
  }),
}));

vi.mock("./api", () => ({
  createSideChat: vi.fn(),
  deleteSideChat: vi.fn(),
  patchSideChatThinkingDepth: vi.fn(),
  retainSideChat: vi.fn(),
}));

const child = {
  id: "child-1",
  displayName: "侧聊",
  parentConversationId: "parent-1",
  parentDisplayName: "主对话",
  relationType: "sidechat" as const,
  selectedText: "选中的内容",
  searchConfig: { dataset_list: [{ id: "kb-1" }] },
  thinkingDepth: "high" as const,
  isEphemeral: true,
};

async function renderSideChat(
  source?: ComponentProps<typeof SideChatPanel>["source"],
) {
  const onClose = vi.fn();
  const view = render(
    <SideChatPanel
      open
      parentConversationId="parent-1"
      source={source}
      onClose={onClose}
    />,
  );
  await screen.findByTestId("side-chat-conversation");
  return { ...view, onClose };
}

function sendSideChatQuestion() {
  act(() => {
    mocks.latestChatProps.onOpenSSE(
      [{ input_type: "text", text: "question" }],
      "CHAT_ACTION_NEXT",
      {},
    );
  });
}

describe("SideChatPanel", () => {
  beforeEach(() => {
    vi.mocked(createSideChat).mockReset().mockResolvedValue(child);
    vi.mocked(deleteSideChat).mockReset().mockResolvedValue(undefined);
    vi.mocked(retainSideChat)
      .mockReset()
      .mockResolvedValue({ ...child, isEphemeral: false });
    vi.mocked(patchSideChatThinkingDepth)
      .mockReset()
      .mockResolvedValue(undefined);
    mocks.closeStream.mockReset();
    mocks.warning.mockReset();
    mocks.success.mockReset();
    mocks.error.mockReset();
    mocks.chatMounts = 0;
    mocks.chatUnmounts = 0;
    mocks.latestChatProps = null;
  });

  it("keeps the same live conversation mounted while its parent is not visible", async () => {
    const onClose = vi.fn();
    const props = {
      embedded: true,
      open: true,
      parentConversationId: "parent-1",
      onClose,
    };
    const view = render(<SideChatPanel {...props} visible />);
    await screen.findByTestId("side-chat-conversation");
    sendSideChatQuestion();
    act(() => mocks.latestChatProps.onStreamingChange(true));
    view.rerender(<SideChatPanel {...props} visible={false} />);
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(mocks.closeStream).not.toHaveBeenCalled();
    expect(mocks.chatUnmounts).toBe(0);
    expect(document.querySelector(".side-chat-embedded")).toHaveAttribute(
      "style",
      expect.stringContaining("display: none"),
    );
    view.rerender(<SideChatPanel {...props} visible />);
    expect(createSideChat).toHaveBeenCalledTimes(1);
    expect(mocks.latestChatProps.sessionId).toBe("child-1");
  });

  it.each([
    ["chat.sideChat.clear", "chat.sideChat.clearTitle"],
    ["chat.sideChat.close", "chat.sideChat.closeConfirmTitle"],
  ])("hides the %s confirmation when its parent is not visible", async (action, title) => {
    const onClose = vi.fn();
    const props = { open: true, parentConversationId: "parent-1", onClose };
    const view = render(<SideChatPanel {...props} visible />);
    await screen.findByTestId("side-chat-conversation");
    sendSideChatQuestion();
    fireEvent.click(screen.getByRole("button", { name: action }));
    await waitFor(() => expect(screen.getByText(title)).toBeVisible());

    view.rerender(<SideChatPanel {...props} visible={false} />);
    await waitFor(() => {
      expect(screen.queryByText(title)).not.toBeInTheDocument();
    });
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    view.rerender(<SideChatPanel {...props} visible />);
    await waitFor(() => expect(screen.getByText(title)).toBeVisible());
    expect(createSideChat).toHaveBeenCalledTimes(1);
  });

  it("creates an isolated child and inherits its chat settings", async () => {
    await renderSideChat({ selectedText: "选中的内容", historyId: "history-1" });

    expect(screen.getByTestId("side-chat-conversation")).toBeInTheDocument();
    expect(createSideChat).toHaveBeenCalledWith(
      "parent-1",
      {
        selectedText: "选中的内容",
        historyId: "history-1",
      },
      "medium",
    );
    expect(mocks.latestChatProps).toMatchObject({
      sessionId: "child-1",
      concurrentStream: true,
      showHistoryButton: false,
      showSkillDeposit: false,
      showConversationConfig: false,
      allowKnowledgeBaseSelection: false,
      thinkingDepth: "high",
      chatConfig: { knowledgeBaseId: ["kb-1"] },
    });
    expect(mocks.latestChatProps.setChatConfig).toBeUndefined();
    expect(document.querySelector(".ant-drawer-mask")).toBeNull();
    expect(
      screen.getByRole("button", { name: "chat.sideChat.retain" }),
    ).toBeDisabled();
  });

  it("sends only the side-chat contract and keeps inherited knowledge read-only", async () => {
    await renderSideChat();

    expect(mocks.latestChatProps).toMatchObject({
      allowMentions: false,
      allowKnowledgeBaseSelection: false,
      showConversationConfig: false,
      showSkillDeposit: false,
      showModelSelector: true,
    });

    const prepareClientConversationId = vi.fn();
    let stream: any;
    act(() => {
      stream = mocks.latestChatProps.onOpenSSE(
        [{ input_type: "text", text: "hello" }],
        "CHAT_ACTION_NEXT",
        {},
        {
          thinking_depth: "max",
          mentions: [{ type: "workflow", resource_id: "wf" }],
          run_in_background: true,
          __prepareClientConversationId: prepareClientConversationId,
        },
      );
    });
    expect(prepareClientConversationId).toHaveBeenCalledWith("child-1");
    const payload = JSON.parse(stream.options.payload);
    expect(payload).toMatchObject({
      conversation_id: "child-1",
      basic_chat_only: true,
      use_memory: false,
      thinking_depth: "max",
      client_request_id: expect.any(String),
    });
    expect(payload).not.toHaveProperty("mentions");
    expect(payload).not.toHaveProperty("run_in_background");

    await act(async () => {
      await mocks.latestChatProps.onThinkingDepthChange("low");
      const nextConfig = {
        knowledgeBaseId: ["kb-2"],
        creators: ["creator-2"],
        tags: ["tag-2"],
      };
      await mocks.latestChatProps.setChatConfigFn(nextConfig);
    });
    expect(patchSideChatThinkingDepth).toHaveBeenCalledWith("child-1", "low");
    expect(mocks.latestChatProps.chatConfig.knowledgeBaseId).toEqual(["kb-1"]);
    const resumed = mocks.latestChatProps.onOpenResumeSSE(
      "child-1", {}, { historyId: "history-1", afterSequence: 2 },
    );
    expect(JSON.parse(resumed.options.payload)).toEqual({
      conversation_id: "child-1",
      history_id: "history-1",
      after_sequence: 2,
      basic_chat_only: true,
      use_memory: false,
    });
  });

  it("blocks closing as soon as a request is submitted for runtime startup", async () => {
    await renderSideChat();

    act(() => mocks.latestChatProps.onRequestPendingChange(true));

    expect(
      screen.getByRole("button", { name: "chat.sideChat.close" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "chat.sideChat.retain" }),
    ).toBeDisabled();
    expect(deleteSideChat).not.toHaveBeenCalled();
  });

  it.each([
    {
      name: "deletes an unretained child before closing",
      status: undefined,
      deleteCalls: 1,
    },
    {
      name: "retries a discard that briefly overlaps server-side generation cleanup",
      status: 409,
      deleteCalls: 2,
    },
    {
      name: "treats an already discarded child as a successful close",
      status: 404,
      deleteCalls: 1,
    },
  ])("$name", async ({ status, deleteCalls }) => {
    if (status) {
      vi.mocked(deleteSideChat).mockRejectedValueOnce({ response: { status } });
    }
    const { onClose } = await renderSideChat();

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));

    const dialog = (await screen.findByText("chat.sideChat.closeConfirmTitle")).closest<HTMLElement>('[role="dialog"]')!;
    expect(deleteSideChat).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.sideChat.closeAndDiscard" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(deleteSideChat).toHaveBeenCalledWith("child-1");
    expect(deleteSideChat).toHaveBeenCalledTimes(deleteCalls);
    expect(mocks.closeStream).toHaveBeenCalledWith("child-1");
  });

  it("confirms before discarding a side chat with messages", async () => {
    const { onClose } = await renderSideChat();
    sendSideChatQuestion();

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
    expect(screen.getByText("chat.sideChat.closeConfirmTitle")).toBeInTheDocument();
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(
      screen.getByRole("button", { name: "chat.sideChat.closeAndDiscard" }),
    );
    await waitFor(() => expect(deleteSideChat).toHaveBeenCalledWith("child-1"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("discards a generating draft when a new selection remounts the panel", async () => {
    const props = { open: true, parentConversationId: "parent-1", source: { selectedText: "选文" }, onClose: vi.fn() };
    const view = render(<SideChatPanel key="selection-1" {...props} />);
    await screen.findByTestId("side-chat-conversation");
    act(() => mocks.latestChatProps.onStreamingChange(true));
    vi.mocked(createSideChat).mockResolvedValueOnce({ ...child, id: "child-2" });
    view.rerender(<SideChatPanel key="selection-2" {...props} />);
    await waitFor(() => expect(createSideChat).toHaveBeenCalledTimes(2));
    expect(deleteSideChat).toHaveBeenCalledWith("child-1");
    expect(mocks.closeStream).toHaveBeenCalledWith("child-1");
    expect(mocks.warning).not.toHaveBeenCalled();
  });

  it.each(["discard", "retain"])("confirms a close from references and %s before closing", async (choice) => {
    const ref = createRef<{ requestClose: () => void }>();
    const onClose = vi.fn();
    const view = render(<SideChatPanel ref={ref} open embedded parentConversationId="parent-1" onClose={onClose} />);
    await screen.findByTestId("side-chat-conversation");
    sendSideChatQuestion();
    view.rerender(<SideChatPanel ref={ref} open embedded visible={false} closeConfirmationVisible parentConversationId="parent-1" onClose={onClose} />);

    act(() => ref.current?.requestClose());
    expect(await screen.findByRole("dialog", { name: "chat.sideChat.closeConfirmTitle" })).toBeVisible();
    expect(onClose).not.toHaveBeenCalled();
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(retainSideChat).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: choice === "retain" ? "chat.sideChat.retain" : "chat.sideChat.closeAndDiscard" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    if (choice === "retain") {
      expect(retainSideChat).toHaveBeenCalledWith("child-1");
      expect(deleteSideChat).not.toHaveBeenCalled();
    } else {
      expect(deleteSideChat).toHaveBeenCalledWith("child-1");
      expect(retainSideChat).not.toHaveBeenCalled();
    }
  });

  it("shows a hidden empty side chat's discard failure and preserves it until retry succeeds", async () => {
    vi.mocked(deleteSideChat).mockRejectedValueOnce(new Error("delete failed"));
    const ref = createRef<{ requestClose: () => void }>();
    const onClose = vi.fn();
    render(<SideChatPanel ref={ref} open embedded visible={false} closeConfirmationVisible parentConversationId="parent-1" onClose={onClose} />);
    await screen.findByTestId("side-chat-conversation");

    act(() => ref.current?.requestClose());

    const dialog = await screen.findByRole("dialog", { name: "chat.sideChat.closeConfirmTitle" });
    expect(deleteSideChat).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.sideChat.closeAndDiscard" }));
    await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent("errors.2000509"));
    expect(within(dialog).getByRole("alert")).toBeVisible();
    expect(within(dialog).getByRole("button", { name: "chat.sideChat.retain" })).toBeDisabled();
    expect(onClose).not.toHaveBeenCalled();
    expect(createSideChat).toHaveBeenCalledTimes(1);
    expect(mocks.chatMounts).toBe(1);
    expect(mocks.chatUnmounts).toBe(0);

    const retryButton = within(dialog).getByRole("button", { name: /chat.sideChat.closeAndDiscard/ });
    expect(retryButton).toBeEnabled();
    fireEvent.click(retryButton);

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(deleteSideChat).toHaveBeenCalledTimes(2);
    expect(deleteSideChat).toHaveBeenLastCalledWith("child-1");
    expect(retainSideChat).not.toHaveBeenCalled();
  });

  it.each([true, false])("confirms closing an empty side chat (visible: %s) and preserves it on cancel", async (visible) => {
    const ref = createRef<{ requestClose: () => void }>();
    const onClose = vi.fn();
    render(<SideChatPanel ref={ref} open embedded visible={visible} closeConfirmationVisible parentConversationId="parent-1" onClose={onClose} />);
    await screen.findByTestId("side-chat-conversation");

    act(() => ref.current?.requestClose());

    const dialog = await screen.findByRole("dialog", { name: "chat.sideChat.closeConfirmTitle" });
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(within(dialog).getByRole("button", { name: "chat.sideChat.retain" })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.sideChat.continue" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(mocks.chatUnmounts).toBe(0);
    expect(createSideChat).toHaveBeenCalledTimes(1);

    act(() => ref.current?.requestClose());
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "chat.sideChat.closeAndDiscard" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(deleteSideChat).toHaveBeenCalledTimes(1);
    expect(deleteSideChat).toHaveBeenCalledWith("child-1");
  });

  it("keeps a side chat open when retaining from the close confirmation fails", async () => {
    vi.mocked(retainSideChat).mockRejectedValue(new Error("failed"));
    const { onClose } = await renderSideChat();
    sendSideChatQuestion();
    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
    const dialog = (await screen.findByText("chat.sideChat.closeConfirmTitle")).closest<HTMLElement>('[role="dialog"]')!;
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.sideChat.retain" }));
    await waitFor(() => expect(retainSideChat).toHaveBeenCalled());
    expect(onClose).not.toHaveBeenCalled();
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(dialog).toBeVisible();
  });

  it("rejects replacement of an active unretained draft", async () => {
    const view = await renderSideChat({ selectedText: "第一段" });

    sendSideChatQuestion();
    view.rerender(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        source={{ selectedText: "第二段" }}
        onClose={vi.fn()}
      />,
    );
    await waitFor(() => expect(mocks.warning).toHaveBeenCalled());
    expect(createSideChat).toHaveBeenCalledTimes(1);
  });

  it("keeps a retained side chat active when its source changes during generation", async () => {
    const view = await renderSideChat({ selectedText: "第一段" });

    sendSideChatQuestion();
    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.retain" }));
    await waitFor(() => expect(retainSideChat).toHaveBeenCalledWith("child-1"));

    act(() => mocks.latestChatProps.onStreamingChange(true));
    view.rerender(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        source={{ selectedText: "第二段" }}
        onClose={vi.fn()}
      />,
    );

    await waitFor(() =>
      expect(mocks.warning).toHaveBeenCalledWith(
        "chat.sideChat.generatingUnavailable",
      ),
    );
    expect(createSideChat).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("side-chat-conversation")).toBeInTheDocument();
  });

  it("preserves the mounted transcript when clearing fails", async () => {
    vi.mocked(deleteSideChat).mockRejectedValueOnce(new Error("delete failed"));
    await renderSideChat();
    expect(mocks.chatMounts).toBe(1);

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.clear" }));
    await screen.findByText("chat.sideChat.clearTitle");
    const clearButtons = screen.getAllByRole("button", {
      name: "chat.sideChat.clear",
    });
    fireEvent.click(clearButtons[clearButtons.length - 1]);

    expect(await screen.findByText("errors.2000509")).toBeInTheDocument();
    expect(screen.getByTestId("side-chat-conversation")).toBeInTheDocument();
    expect(mocks.chatMounts).toBe(1);
    expect(mocks.chatUnmounts).toBe(0);
  });

  it("confirms closing a retained child without offering discard or deleting it", async () => {
    const { onClose } = await renderSideChat();
    sendSideChatQuestion();

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.retain" }));
    await waitFor(() => expect(retainSideChat).toHaveBeenCalledWith("child-1"));
    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
    let dialog = (await screen.findByText("chat.sideChat.closeRetainedConfirmTitle")).closest<HTMLElement>('[role="dialog"]')!;
    expect(within(dialog).getByText("chat.sideChat.closeRetainedConfirmDescription")).toBeVisible();
    expect(within(dialog).queryByRole("button", { name: "chat.sideChat.closeAndDiscard" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "chat.sideChat.retain" })).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.sideChat.continue" }));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
    dialog = (await screen.findByText("chat.sideChat.closeRetainedConfirmTitle")).closest<HTMLElement>('[role="dialog"]')!;
    fireEvent.click(within(dialog).getByRole("button", { name: "chat.sideChat.close" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(deleteSideChat).not.toHaveBeenCalled();
    expect(retainSideChat).toHaveBeenCalledTimes(1);
  });
});
