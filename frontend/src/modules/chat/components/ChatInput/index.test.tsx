import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  NEW_CHAT_MODEL_SELECTION_KEY,
  useModelSelectionStore,
  type ChatModelSelection,
  type ChatModelSelectionRequest,
} from "@/modules/chat/store/modelSelection";
import ChatInput from ".";
import { useState } from "react";
import { useChatInputStore } from "../../store/chatInput";
import type { ChatMention } from "./MentionEditor";
import { listSkillLinkedWorkflows } from "@/modules/workflow/workflowDraftApi";

vi.mock("../SkillRecording", () => ({ default: () => null }));

const promptMocks = vi.hoisted(() => ({ polish: vi.fn() }));
vi.mock("@/modules/chat/utils/request", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/chat/utils/request")>()),
  PromptServiceApi: () => ({ promptServicePolishPrompt: promptMocks.polish }),
}));

vi.mock("@/modules/workflow/workflowDraftApi", () => ({
  listSkillLinkedWorkflows: vi.fn().mockResolvedValue({ workflows: [] }),
}));

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("../ChatModelSelector", () => ({
  default: ({
    onSavingChange,
  }: {
    onSavingChange?: (saving: boolean) => void;
  }) => (
    <>
      <button type="button" onClick={() => onSavingChange?.(true)}>
        begin model save
      </button>
      <button type="button" onClick={() => onSavingChange?.(false)}>
        finish model save
      </button>
    </>
  ),
}));

vi.mock("./MentionEditor", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    default: React.forwardRef(function MockMentionEditor(
      props: { onSend?: () => void; initialMentions?: ChatMention[]; onMentionsChange?: (mentions: ChatMention[]) => void },
      ref,
    ) {
      React.useImperativeHandle(ref, () => ({ focus: vi.fn(), setPlainText: () => props.onMentionsChange?.([]) }));
      React.useEffect(() => { if (props.initialMentions?.length) props.onMentionsChange?.(props.initialMentions); }, []);
      return (
        <textarea
          aria-label="message editor"
          onKeyDown={(event) => {
            if (event.key === "Enter") props.onSend?.();
          }}
        />
      );
    }),
  };
});

vi.mock("../ImageUpload", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    allowedImageTypes: [".png"],
    allowedFileTypes: [".pdf"],
    allowedTextTypes: [".txt"],
    allowedUploadTypes: [".png", ".pdf", ".txt"],
    default: React.forwardRef(function MockImageUpload(_props, ref) {
      React.useImperativeHandle(ref, () => ({
        clear: vi.fn(),
        getFiles: () => [],
        getUploadingCount: () => 0,
        openFileDialog: vi.fn(),
        removeFile: vi.fn(),
        uploadFiles: vi.fn(),
      }));
      return null;
    }),
  };
});

vi.mock("../ChatSelector", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    default: React.forwardRef(function MockChatSelector(_props, ref) {
      React.useImperativeHandle(ref, () => ({ open: vi.fn() }));
      return null;
    }),
  };
});

vi.mock("../PromptModal", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    default: React.forwardRef(function MockPromptModal(_props, ref) {
      React.useImperativeHandle(ref, () => ({ onOpen: vi.fn() }));
      return null;
    }),
  };
});

vi.mock("../BatchChat", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    default: React.forwardRef(function MockBatchChat(_props, ref) {
      React.useImperativeHandle(ref, () => ({}));
      return null;
    }),
  };
});

vi.mock("../ShowChatFileList", () => ({ default: () => null }));
vi.mock("./ContextUsageButton", () => ({ default: () => null }));
vi.mock("./LocalWorkspaceControl", () => ({
  default: ({ conversationId, onChange, onSavingChange }: { onSavingChange?: (saving: boolean) => void; conversationId?: string; onChange: (id: string | undefined, mode: "ask_as_needed" | "allow_all") => void }) => (
    <div data-testid="local-workspace-control">
      {conversationId ?? "draft"}
      <button onClick={() => onSavingChange?.(true)}>begin workspace save</button>
      <button onClick={() => onSavingChange?.(false)}>finish workspace save</button>
      <button type="button" onClick={() => onChange("grant-alpha", "allow_all")}>select workspace</button>
    </div>
  ),
}));
vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssetsPage: vi.fn().mockImplementation(
    () => new Promise<never>(() => undefined),
  ),
  TOOL_AVAILABILITY_CHANGED_EVENT: "tool-availability-changed",
}));

describe("ChatInput model switch save lock", () => {
  afterEach(() => {
    vi.clearAllMocks();
    useModelSelectionStore.getState().resetForNewChat();
  });

  it("blocks button, keyboard, and send handling until the workspace PUT settles", async () => {
    const onSend = vi.fn();
    const onSkillDeposit = vi.fn();
    render(
      <ChatInput
        value="hello"
        onChange={vi.fn()}
        onSend={onSend}
        isChatContent
        sessionId="conversation-1"
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        skillDepositStats={{ userTurns: 3, toolCallTurns: 8 }}
        onSkillDeposit={onSkillDeposit}
        showThinkingDepth={false}
      />,
    );

    const sendButton = screen.getByRole("button", { name: "chat.send" });
    const skillDepositButton = screen.getByRole("button", {
      name: /chat\.skillDeposit/,
    });
    expect(sendButton).toBeEnabled();
    expect(skillDepositButton).toHaveAttribute("aria-disabled", "false");

    fireEvent.click(screen.getByRole("button", { name: "begin workspace save" }));
    expect(sendButton).toBeDisabled();
    expect(skillDepositButton).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(sendButton);
    fireEvent.click(skillDepositButton);
    fireEvent.keyDown(screen.getByRole("textbox", { name: "message editor" }), {
      key: "Enter",
    });
    expect(onSend).not.toHaveBeenCalled();
    expect(onSkillDeposit).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "finish workspace save" }));
    expect(sendButton).toBeEnabled();
    expect(skillDepositButton).toHaveAttribute("aria-disabled", "false");
    fireEvent.click(sendButton);
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
  });

  it("blocks button, keyboard, and send handling until the model PATCH settles", async () => {
    const onSend = vi.fn();
    const onSkillDeposit = vi.fn();
    render(
      <ChatInput
        value="hello"
        onChange={vi.fn()}
        onSend={onSend}
        isChatContent
        sessionId="conversation-1"
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        skillDepositStats={{ userTurns: 3, toolCallTurns: 8 }}
        onSkillDeposit={onSkillDeposit}
        showThinkingDepth={false}
      />,
    );

    const sendButton = screen.getByRole("button", { name: "chat.send" });
    const skillDepositButton = screen.getByRole("button", {
      name: /chat\.skillDeposit/,
    });
    expect(sendButton).toBeEnabled();
    expect(skillDepositButton).toHaveAttribute("aria-disabled", "false");

    fireEvent.click(screen.getByRole("button", { name: "begin model save" }));
    expect(sendButton).toBeDisabled();
    expect(skillDepositButton).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(sendButton);
    fireEvent.click(skillDepositButton);
    fireEvent.keyDown(screen.getByRole("textbox", { name: "message editor" }), {
      key: "Enter",
    });
    expect(onSend).not.toHaveBeenCalled();
    expect(onSkillDeposit).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "finish model save" }));
    expect(sendButton).toBeEnabled();
    expect(skillDepositButton).toHaveAttribute("aria-disabled", "false");
    fireEvent.click(sendButton);
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
  });

  it.each<{
    label: string;
    stored: ChatModelSelection;
    expected: ChatModelSelectionRequest;
  }>([
    {
      label: "fixed",
      stored: { mode: "fixed", model_id: "model-1", version: 0 },
      expected: { mode: "fixed", model_id: "model-1" },
    },
    {
      label: "Auto",
      stored: { mode: "auto", version: 0 },
      expected: { mode: "auto" },
    },
  ])(
    "reads the latest $label selection from the new-chat store when sending",
    async ({ stored, expected }) => {
      useModelSelectionStore
        .getState()
        .setSelection(NEW_CHAT_MODEL_SELECTION_KEY, stored);
      const onSend = vi.fn();

      render(
        <ChatInput
          value="hello"
          onChange={vi.fn()}
          onSend={onSend}
          isChatContent
          showConversationConfig={false}
          showHistoryButton={false}
          showPromptSuggestions={false}
          showSkillDeposit={false}
          showThinkingDepth={false}
        />,
      );

      fireEvent.click(screen.getByRole("button", { name: "chat.send" }));

      await waitFor(() => expect(onSend).toHaveBeenCalledWith(
        expect.objectContaining({ initial_model_selection: expected }),
      ));
    },
  );

  it("does not expose side chat as a persistent input action", () => {
    render(
      <ChatInput
        value=""
        onChange={vi.fn()}
        isChatContent
        isStreaming
        disabled
        disabledReason="main workflow is busy"
        sessionId="conversation-1"
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        showSkillDeposit={false}
        showThinkingDepth={false}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "chat.sideChat.open" }),
    ).not.toBeInTheDocument();
  });

  it("clears workspace request fields when a reused draft is reset", async () => {
    const onSend = vi.fn();
    const baseProps = {
      value: "hello", onChange: vi.fn(), onSend, isChatContent: true, runInBackground: false,
      showConversationConfig: false, showHistoryButton: false, showPromptSuggestions: false,
      showSkillDeposit: false, showThinkingDepth: false,
    };
    const { rerender } = render(<ChatInput {...baseProps} configResetKey={1} />);
    fireEvent.click(screen.getByRole("button", { name: "select workspace" }));
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenLastCalledWith(expect.objectContaining({
        workspace_id: "grant-alpha", workspace_permission_mode: "allow_all",
      })));

    rerender(<ChatInput {...baseProps} configResetKey={2} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
    const resetPayload = onSend.mock.calls[onSend.mock.calls.length - 1]?.[0];
    expect(resetPayload).not.toHaveProperty("workspace_id");
    expect(resetPayload).not.toHaveProperty("workspace_permission_mode");
  });

  it("checks a formal conversation for a workspace binding", () => {
    render(
      <ChatInput
        value=""
        onChange={vi.fn()}
        isChatContent
        sessionId="conversation-1"
        runInBackground={false}
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        showSkillDeposit={false}
        showThinkingDepth={false}
      />,
    );

    expect(screen.getByTestId("local-workspace-control")).toHaveTextContent("conversation-1");
  });

  it("keeps the permission control mounted after a workspace task starts", () => {
    render(
      <ChatInput
        value=""
        onChange={vi.fn()}
        isChatContent
        sessionId="conversation-1"
        runInBackground
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        showSkillDeposit={false}
        showThinkingDepth={false}
      />,
    );

    expect(screen.getByTestId("local-workspace-control")).toHaveTextContent("conversation-1");
  });

  it("hides knowledge-base selection for inherited child conversations", async () => {
    render(
      <ChatInput
        value=""
        onChange={vi.fn()}
        isChatContent
        sessionId="child-1"
        allowKnowledgeBaseSelection={false}
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        showSkillDeposit={false}
        showThinkingDepth={false}
      />,
    );

    fireEvent.click(screen.getByText("chat.addResource"));

    expect(
      await screen.findByRole("button", { name: /chat\.addAttachment/ }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "chat.knowledgeBase" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /chat\.promptTemplate/ }),
    ).toBeInTheDocument();
  });
});


describe("ChatInput saved mentions", () => {
  it.each([false, true])("filters saved and bound knowledge-base mentions before sending (runtime=%s)", async (runtime) => {
    const knowledge: ChatMention = { mention_id: 'kb', type: 'knowledge_base', resource_id: 'test-kb', display_name: '知识库', start: 0, end: 3 };
    const tool: ChatMention = { mention_id: 'tool', type: 'tool', resource_id: 'test-tool', display_name: '工具', start: 4, end: 6 };
    const sessionId = `restricted-draft-${runtime}`;
    useChatInputStore.getState().saveInputContent(sessionId, '知识库 工具 请总结', [knowledge, tool]);
    const onSend = vi.fn();
    function Composer({ allow }: { allow: boolean }) {
      const [value, setValue] = useState('');
      return <ChatInput value={value} onChange={setValue} onSend={onSend} sessionId={sessionId}
        allowKnowledgeBaseSelection={allow} boundMentions={[{ ...knowledge, resource_id: 'bound-kb' }]}
        isChatContent showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />;
    }
    const view = render(<Composer allow={runtime} />);
    if (runtime) view.rerender(<Composer allow={false} />);
    fireEvent.click(screen.getByRole('button', { name: 'chat.send' }));
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(expect.objectContaining({ mentions: [tool] })));
  });

  it.each(['工具 请总结', '请总结这份资料'])('clears draft mentions after prompt polishing to %s', async (nextPrompt) => {
    const sessionId = `polished-draft-${nextPrompt}`;
    const mention: ChatMention = { mention_id: 'tool', type: 'tool', resource_id: 'test-tool', display_name: '工具', start: 0, end: 2 };
    useChatInputStore.getState().saveInputContent(sessionId, '工具 请总结', [mention]);
    promptMocks.polish.mockResolvedValue({ data: { content: nextPrompt } });
    const onSend = vi.fn();
    function Composer() {
      const [value, setValue] = useState('');
      return <ChatInput value={value} onChange={setValue} onSend={onSend} sessionId={sessionId}
        isChatContent showConversationConfig={false} showHistoryButton={false} showPromptSuggestions />;
    }
    const view = render(<Composer />);
    fireEvent.click(screen.getByRole('button', { name: /^chat.promptSuggestionPolish/ }));
    await waitFor(() => expect(useChatInputStore.getState().getInputMentions(sessionId)).toEqual([]));
    expect(useChatInputStore.getState().getInputContent(sessionId)).toBe(nextPrompt);
    fireEvent.click(screen.getByRole('button', { name: 'chat.send' }));
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(expect.objectContaining({ text: nextPrompt, mentions: [] })));
    view.unmount();
    expect(useChatInputStore.getState().getInputMentions(sessionId)).toEqual([]);
  });

  it("does not resolve or send stale resource mentions in side chat", async () => {
    const mention: ChatMention = { mention_id: "old-side-skill", type: "skill", resource_id: "test-skill", display_name: "测试技能", start: 0, end: 4 };
    useChatInputStore.getState().saveInputContent("side-mention-draft", "测试技能 请解释", [mention]);
    const onSend = vi.fn();
    function Composer() {
      const [value, setValue] = useState("");
      return <ChatInput value={value} onChange={setValue} onSend={onSend} sessionId="side-mention-draft"
        allowMentions={false} boundMentions={[{ ...mention, type: "tool" }]}
        isChatContent showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />;
    }
    render(<Composer />);
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(expect.objectContaining({ mentions: [], text: "测试技能 请解释" })));
    expect(listSkillLinkedWorkflows).not.toHaveBeenCalled();
  });

  it("keeps the destination draft when switching from an empty temporary conversation", () => {
    useChatInputStore.getState().saveInputContent("saved-draft", "已有草稿");
    const onChange = vi.fn();
    const props = { value: "", onChange, isChatContent: true, showConversationConfig: false, showHistoryButton: false, showPromptSuggestions: false };
    const view = render(<ChatInput {...props} sessionId="temp_empty" />);
    view.rerender(<ChatInput {...props} sessionId="saved-draft" />);
    expect(onChange).toHaveBeenCalledWith("已有草稿");
    expect(useChatInputStore.getState().getInputContent("saved-draft")).toBe("已有草稿");
  });

  it("sends the restored resource identity and clears the sent draft", async () => {
    const mention: ChatMention = { mention_id: "draft-tool", type: "tool", resource_id: "test-tool", display_name: "测试工具", start: 0, end: 4 };
    useChatInputStore.getState().saveInputContent("mention-draft", "测试工具 请执行", [mention]);
    const onSend = vi.fn();
    function Composer() {
      const [value, setValue] = useState("");
      return <ChatInput value={value} onChange={setValue} onSend={onSend} sessionId="mention-draft" isChatContent showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />;
    }
    const view = render(<Composer />);
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(expect.objectContaining({ mentions: [mention], text: "测试工具 请执行" })));
    view.unmount();
    expect(useChatInputStore.getState().getInputContent("mention-draft")).toBe("");
    expect(useChatInputStore.getState().getInputMentions("mention-draft")).toEqual([]);
  });
});
