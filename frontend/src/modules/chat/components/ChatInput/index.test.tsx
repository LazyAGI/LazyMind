import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  NEW_CHAT_MODEL_SELECTION_KEY,
  useModelSelectionStore,
  type ChatModelSelection,
  type ChatModelSelectionRequest,
} from "@/modules/chat/store/modelSelection";
import ChatInput from ".";

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
      props: { onSend?: () => void },
      ref,
    ) {
      React.useImperativeHandle(ref, () => ({ focus: vi.fn() }));
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
      value: "hello", onChange: vi.fn(), onSend, isChatContent: true, runInBackground: true,
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
