import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { forwardRef, useEffect, useImperativeHandle } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SideChatPanel from "./index";
import {
  createSideChat,
  deleteSideChat,
  patchSideChatKnowledge,
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
  default: { language: "zh-CN", resolvedLanguage: "zh-CN" },
}));

vi.mock("antd", async (importOriginal) => {
  const actual = await importOriginal<typeof import("antd")>();
  return {
    ...actual,
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
  patchSideChatKnowledge: vi.fn(),
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

describe("SideChatPanel", () => {
  beforeEach(() => {
    vi.mocked(createSideChat).mockReset().mockResolvedValue(child);
    vi.mocked(deleteSideChat).mockReset().mockResolvedValue(undefined);
    vi.mocked(retainSideChat)
      .mockReset()
      .mockResolvedValue({ ...child, isEphemeral: false });
    vi.mocked(patchSideChatKnowledge).mockReset().mockResolvedValue(undefined);
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

  it("creates an isolated child and inherits its chat settings", async () => {
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        source={{ selectedText: "选中的内容", historyId: "history-1" }}
        onClose={vi.fn()}
      />,
    );

    expect(await screen.findByTestId("side-chat-conversation")).toBeInTheDocument();
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
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

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
    expect(patchSideChatKnowledge).not.toHaveBeenCalled();
  });

  it("blocks closing as soon as a request is submitted for runtime startup", async () => {
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

    act(() => mocks.latestChatProps.onRequestPendingChange(true));

    expect(
      screen.getByRole("button", { name: "chat.sideChat.close" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "chat.sideChat.retain" }),
    ).toBeDisabled();
    expect(deleteSideChat).not.toHaveBeenCalled();
  });

  it("deletes an unretained child before closing", async () => {
    const onClose = vi.fn();
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={onClose}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
    await waitFor(() => expect(deleteSideChat).toHaveBeenCalledWith("child-1"));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mocks.closeStream).toHaveBeenCalledWith("child-1");
  });

  it("retries a discard that briefly overlaps server-side generation cleanup", async () => {
    vi.mocked(deleteSideChat)
      .mockRejectedValueOnce({ response: { status: 409 } })
      .mockResolvedValueOnce(undefined);
    const onClose = vi.fn();
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={onClose}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));

    await waitFor(() => expect(deleteSideChat).toHaveBeenCalledTimes(2));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("treats an already discarded child as a successful close", async () => {
    vi.mocked(deleteSideChat).mockRejectedValueOnce({ response: { status: 404 } });
    const onClose = vi.fn();
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={onClose}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(deleteSideChat).toHaveBeenCalledTimes(1);
  });

  it("confirms before discarding a side chat with messages", async () => {
    const onClose = vi.fn();
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={onClose}
      />,
    );
    await screen.findByTestId("side-chat-conversation");
    act(() => {
      mocks.latestChatProps.onOpenSSE(
        [{ input_type: "text", text: "question" }],
        "CHAT_ACTION_NEXT",
        {},
      );
    });

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

  it("rejects replacement of an active unretained draft", async () => {
    const view = render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        source={{ selectedText: "第一段" }}
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

    act(() => {
      mocks.latestChatProps.onOpenSSE(
        [{ input_type: "text", text: "question" }],
        "CHAT_ACTION_NEXT",
        {},
      );
    });
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
    const view = render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        source={{ selectedText: "第一段" }}
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("side-chat-conversation");

    act(() => {
      mocks.latestChatProps.onOpenSSE(
        [{ input_type: "text", text: "question" }],
        "CHAT_ACTION_NEXT",
        {},
      );
    });
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
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("side-chat-conversation");
    expect(mocks.chatMounts).toBe(1);

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.clear" }));
    await screen.findByText("chat.sideChat.clearTitle");
    const clearButtons = screen.getAllByRole("button", {
      name: "chat.sideChat.clear",
    });
    fireEvent.click(clearButtons[clearButtons.length - 1]);

    expect(await screen.findByText("chat.sideChat.clearFailed")).toBeInTheDocument();
    expect(screen.getByTestId("side-chat-conversation")).toBeInTheDocument();
    expect(mocks.chatMounts).toBe(1);
    expect(mocks.chatUnmounts).toBe(0);
  });

  it("closes a retained child without deleting it", async () => {
    render(
      <SideChatPanel
        open
        parentConversationId="parent-1"
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("side-chat-conversation");
    act(() => {
      mocks.latestChatProps.onOpenSSE(
        [{ input_type: "text", text: "question" }],
        "CHAT_ACTION_NEXT",
        {},
      );
    });

    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.retain" }));
    await waitFor(() => expect(retainSideChat).toHaveBeenCalledWith("child-1"));
    fireEvent.click(screen.getByRole("button", { name: "chat.sideChat.close" }));
    expect(deleteSideChat).not.toHaveBeenCalled();
  });
});
