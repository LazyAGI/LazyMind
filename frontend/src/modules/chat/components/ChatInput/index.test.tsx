import { act, fireEvent, render as renderComponent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { getRuntimeMode } from "@/runtime/mode";
import {
  NEW_CHAT_MODEL_SELECTION_KEY,
  useModelSelectionStore,
  type ChatModelSelection,
  type ChatModelSelectionRequest,
} from "@/modules/chat/store/modelSelection";
import ChatInput from ".";
import { createRef, Profiler, useState, type ReactNode } from "react";
import { useChatInputStore, type ChatModeDraft } from "../../store/chatInput";
import { useChatMessageStore } from "../../store/chatMessage";
import { useWorkflowStore } from "../../store/workflowPanel";
import type { ChatFileList, ChatInputImperativeProps } from "./types";
import type { ChatMention } from "./MentionEditor";
import { listSkillLinkedWorkflows } from "@/modules/workflow/workflowDraftApi";
import { message } from "antd";
import { MemoryRouter } from "react-router-dom";

const render = (ui: ReactNode) => renderComponent(ui, { wrapper: MemoryRouter });

vi.mock("@/runtime/mode", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/runtime/mode")>()),
  getRuntimeMode: vi.fn(() => "local"),
}));

vi.mock("../SkillRecording", () => ({ default: () => null }));

const promptMocks = vi.hoisted(() => ({ polish: vi.fn() }));
const rag = vi.hoisted(() => ({ availability: "ready" }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: rag.availability }),
}));
afterEach(() => { rag.availability = "ready"; });
const datasets = vi.hoisted(() => ({ list: vi.fn(() => new Promise(() => undefined)) }));
vi.mock("@/modules/chat/utils/request", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/modules/chat/utils/request")>()),
  PromptServiceApi: () => ({ promptServicePolishPrompt: promptMocks.polish }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: datasets.list,
  }),
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
    allowedSpreadsheetTypes: [".csv", ".tsv", ".xls", ".xlsx"],
    allowedFileTypes: [".pdf", ".csv", ".tsv", ".xls", ".xlsx"],
    allowedTextTypes: [".txt"],
    allowedUploadTypes: [".png", ".pdf", ".csv", ".tsv", ".xls", ".xlsx", ".txt"],
    default: React.forwardRef(function MockImageUpload(props: {
      types: string[];
      max: number;
      listNum: number;
      updateFiles: (files: (File & { uid: string; uri: string })[]) => void;
    }, ref) {
      const files = React.useRef<(File & { uid: string; uri: string })[]>([]);
      React.useImperativeHandle(ref, () => ({
        clear: () => { files.current = []; props.updateFiles([]); },
        getFiles: () => files.current,
        getUploadingCount: () => 0,
        openFileDialog: vi.fn(),
        removeFile: (uid: string) => {
          files.current = files.current.filter(file => file.uid !== uid);
          props.updateFiles(files.current);
        },
        uploadFiles: (incoming: File[]) => {
          for (const file of incoming) {
            if (files.current.length >= props.max) break;
            files.current.push(Object.assign(file, { uid: file.name, uri: `/uploads/${file.name}` }));
          }
          props.updateFiles(files.current);
        },
      }));
      return <>
        <span data-testid="upload-slots" data-max={props.max} data-count={props.listNum} />
        <button type="button" data-testid="attach-test-pdf" data-accept={props.types.join(",")}
        onClick={() => {
          const file = Object.assign(new File(["PDF"], "paper.pdf", { type: "application/pdf" }), {
            uid: "pdf-1", uri: "/uploads/paper.pdf",
          });
          files.current = [file];
          props.updateFiles([file]);
        }}>attach PDF</button>
      </>;
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

vi.mock("../ShowChatFileList", () => ({
  default: ({ fileList, onRemove }: { fileList: ChatFileList[]; onRemove: (uid: string) => void }) => (
    <div aria-label="attachments">{fileList.map((file, index) => (
      <button key={index} onClick={() => onRemove(file.uid)}>{file.name}</button>
    ))}</div>
  ),
}));
const contextPreview = vi.hoisted(() => ({ build: null as null | (() => Record<string, any>) }));
vi.mock("./ContextUsageButton", () => ({ default: (props: { buildRequest: () => Record<string, any> }) => { contextPreview.build = props.buildRequest; return null; } }));
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
  beforeEach(() => {
    vi.mocked(getRuntimeMode).mockReturnValue("local");
  });

  it.each([
    ["cloud", true, true, false],
    ["local", true, true, true],
    ["desktop", true, true, true],
    ["cloud", false, true, false],
    ["local", false, true, false],
    ["desktop", false, true, false],
    ["cloud", true, false, false],
    ["local", true, false, false],
    ["desktop", true, false, false],
  ] as const)("workspace visibility: %s, task=%s, allowed=%s → %s", (runtime, runInBackground, allowLocalWorkspace, visible) => {
    vi.mocked(getRuntimeMode).mockReturnValue(runtime);
    const { container } = render(<ChatInput value="" onChange={vi.fn()} isChatContent
      runInBackground={runInBackground} allowLocalWorkspace={allowLocalWorkspace}
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);

    expect(Boolean(screen.queryByTestId("local-workspace-control"))).toBe(visible);
    expect(Boolean(container.querySelector(".chat-input-environment"))).toBe(visible);
    expect(container.querySelector(".chat-input-surface")?.classList.contains("has-environment")).toBe(visible);
  });

  it('captures the visible draft before a mode switch request', () => {
    const rects = vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList);
    try {
      render(<ChatInput value='Keep historical draft' onChange={vi.fn()} isChatContent sessionId='draft-source' runInBackground
        showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);
      window.dispatchEvent(new CustomEvent('lazymind:chat-capture-mode-draft', { detail: { runInBackground: false } }));
      expect(useChatInputStore.getState().modeDraft).toMatchObject({ text: 'Keep historical draft', mentions: [] });
    } finally { rects.mockRestore(); useChatInputStore.getState().setModeDraft(); }
  });

  it('keeps uploads unique and removable across repeated welcome mode switches', async () => {
    const rects = vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList);
    const ref = createRef<ChatInputImperativeProps>();
    let runInBackground = false;
    const input = (draft?: ChatModeDraft) => <ChatInput ref={ref} value="Summarize" onChange={vi.fn()}
      isChatContent={false} runInBackground={runInBackground}
      transferredDraft={draft} conversationFiles={draft?.files}
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />;
    try {
      const view = render(input());
      await act(async () => ref.current?.uploadFiles([new File(['test'], 'photo.png', { type: 'image/png' })]));
      const attachments = () => within(screen.getByLabelText('attachments')).queryAllByRole('button');
      await waitFor(() => expect(attachments()).toHaveLength(1));
      for (let index = 0; index < 4; index++) {
        runInBackground = !runInBackground;
        fireEvent(window, new CustomEvent('lazymind:chat-capture-mode-draft', { detail: { runInBackground } }));
        const draft = useChatInputStore.getState().modeDraft;
        expect(draft?.files).toHaveLength(1);
        view.rerender(input(draft));
        expect(attachments()).toHaveLength(1);
        // The uploader still owns one local file and must retain its full capacity.
        expect(screen.getByTestId('upload-slots')).toHaveAttribute('data-max', '3');
      }
      expect(contextPreview.build?.().input).toEqual([
        expect.objectContaining({ input_type: 'text' }),
        { input_type: 'image', uri: '/uploads/photo.png', filename: 'photo.png' },
      ]);
      fireEvent.click(attachments()[0]);
      await waitFor(() => expect(attachments()).toHaveLength(0));
      runInBackground = !runInBackground;
      fireEvent(window, new CustomEvent('lazymind:chat-capture-mode-draft', { detail: { runInBackground } }));
      const draft = useChatInputStore.getState().modeDraft;
      expect(draft?.files).toEqual([]);
      view.rerender(input(draft));
      expect(attachments()).toHaveLength(0);
      fireEvent.click(screen.getByRole('button', { name: 'chat.send' }));
      await waitFor(() => expect(useChatMessageStore.getState().pendingMessage).toMatchObject({ referencedFiles: [] }));
    } finally {
      rects.mockRestore();
      act(() => { useChatInputStore.getState().setModeDraft(); useChatMessageStore.getState().clearPendingMessage(); });
    }
  });

  it('captures uploaded files as soon as the attachment is committed', async () => {
    const rects = vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList);
    const ref = createRef<ChatInputImperativeProps>();
    const captured: ChatModeDraft[] = [];
    try {
      render(<Profiler id="composer" onRender={() => {
        // A synchronous mode switch can happen after the DOM commits but before
        // passive effects run, just as the async upload finishes in CI.
        if (captured.length || !screen.queryByRole('button', { name: 'photo.png' })) return;
        const detail: { runInBackground: boolean; draft?: ChatModeDraft } = { runInBackground: true };
        window.dispatchEvent(new CustomEvent('lazymind:chat-capture-mode-draft', { detail }));
        if (detail.draft) captured.push(detail.draft);
      }}>
        <ChatInput ref={ref} value="Summarize" onChange={vi.fn()} isChatContent={false}
          runInBackground={false} showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />
      </Profiler>);
      await act(async () => ref.current?.uploadFiles([new File(['test'], 'photo.png', { type: 'image/png' })]));
      await waitFor(() => expect(captured.length).toBeGreaterThan(0));
      for (const draft of captured) {
        expect(draft.files).toEqual([expect.objectContaining({
          id: 'upload:photo.png', input: { input_type: 'image', uri: '/uploads/photo.png', filename: 'photo.png' },
        })]);
      }
    } finally {
      rects.mockRestore();
      act(() => useChatInputStore.getState().setModeDraft());
    }
  });

  it('counts only restored-only uploads against new upload slots', async () => {
    const ref = createRef<ChatInputImperativeProps>();
    const draft = { text: 'Summarize', mentions: [], files: [{ id: 'upload:old', name: 'old.png', input: {
      input_type: 'image' as const, uri: '/uploads/old.png', filename: 'old.png',
    } }] };
    render(<ChatInput ref={ref} value={draft.text} onChange={vi.fn()} isChatContent={false}
      transferredDraft={draft} conversationFiles={draft.files}
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);
    expect(screen.getByTestId('upload-slots')).toHaveAttribute('data-max', '2');
    await act(async () => ref.current?.uploadFiles([new File(['test'], 'new.png', { type: 'image/png' })]));
    await waitFor(() => expect(within(screen.getByLabelText('attachments')).getAllByRole('button')).toHaveLength(2));
    // The restored file has already reduced max; it must not also count as local.
    expect(screen.getByTestId('upload-slots')).toHaveAttribute('data-count', '1');
    expect(screen.getByTestId('upload-slots')).toHaveAttribute('data-max', '2');
    await act(async () => ref.current?.uploadFiles([new File(['test'], 'other.png', { type: 'image/png' })]));
    await waitFor(() => expect(within(screen.getByLabelText('attachments')).getAllByRole('button')).toHaveLength(3));
    fireEvent.click(screen.getByRole('button', { name: 'old.png' }));
    expect(within(screen.getByLabelText('attachments')).getAllByRole('button')).toHaveLength(2);
    expect(screen.getByTestId('upload-slots')).toHaveAttribute('data-max', '3');
    expect(contextPreview.build?.().input).toHaveLength(3);
  });

  it('adapts a restored current-conversation file identically for preview and send', async () => {
    const mention = { mention_id: 'file-chip', type: 'file' as const, resource_id: 'history:h1:0', display_name: 'report.pdf', start: 0, end: 10 };
    useChatInputStore.getState().saveInputContent('file-conversation', 'report.pdf 总结', [mention]);
    const input = { input_type: 'file' as const, uri: '/uploads/report.pdf', filename: 'report.pdf' };
    const onSend = vi.fn();
    render(<ChatInput value='report.pdf 总结' onChange={vi.fn()} isChatContent sessionId='file-conversation'
      onSend={onSend} conversationFiles={[{ id: mention.resource_id, name: mention.display_name, input }]}
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);
    expect(contextPreview.build?.()).toMatchObject({ mentions: [], input: [expect.objectContaining({ input_type: 'text' }), input] });
    fireEvent.click(screen.getByRole('button', { name: 'chat.send' }));
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(expect.objectContaining({ mentions: [], referencedFiles: [input] })));
  });

  afterEach(() => {
    vi.clearAllMocks();
    useModelSelectionStore.getState().resetForNewChat();
  });

  it("accepts and sends PDF attachments without RAG while leaving knowledge search disabled", async () => {
    const createObjectURL = URL.createObjectURL;
    const revokeObjectURL = URL.revokeObjectURL;
    URL.createObjectURL = vi.fn(() => "blob:test-pdf");
    URL.revokeObjectURL = vi.fn();
    onTestFinished(() => {
      URL.createObjectURL = createObjectURL;
      URL.revokeObjectURL = revokeObjectURL;
    });
    rag.availability = "missing";
    const onSend = vi.fn();
    render(<ChatInput value="Read this PDF" onChange={vi.fn()} onSend={onSend} isChatContent />);
    const upload = screen.getByTestId("attach-test-pdf");
    expect(upload).toHaveAttribute("data-accept", expect.stringContaining(".pdf"));
    fireEvent.click(upload);
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(expect.objectContaining({
      fileList: [expect.objectContaining({ name: "paper.pdf" })],
      files: [expect.objectContaining({ name: "paper.pdf" })],
      chatConfigSnapshot: expect.objectContaining({ knowledgeBaseId: [] }),
    })));
  });

  it("does not mount workspace polling for document previews", () => {
    const props = { value: "", onChange: vi.fn(), isChatContent: true, sessionId: "pdf-preview", runInBackground: true };
    const view = render(<ChatInput {...props} allowLocalWorkspace={false} />);
    expect(screen.queryByTestId("local-workspace-control")).not.toBeInTheDocument();
    view.rerender(<ChatInput {...props} sessionId="conversation-1" />);
    expect(screen.getByTestId("local-workspace-control")).toBeInTheDocument();
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
        runInBackground
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        skillDepositStats={{ userTurns: 3, toolCallTurns: 8 }}
        onSkillDeposit={onSkillDeposit}
        showThinkingDepth={false}
      />,
    );

    const sendButton = screen.getByRole("button", { name: "chat.send" });
    fireEvent.click(screen.getByRole("button", { name: "chat.addResource" }));
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
    fireEvent.click(screen.getByRole("button", { name: "chat.addResource" }));
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

  it("queues keyboard input while a response is streaming", async () => {
    const onSend = vi.fn().mockResolvedValue(true);
    render(
      <ChatInput
        value="adjust the direction"
        onChange={vi.fn()}
        onSend={onSend}
        onStopGeneration={vi.fn()}
        isChatContent
        isStreaming
        sessionId="conversation-1"
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        showSkillDeposit={false}
        showThinkingDepth={false}
      />,
    );

    fireEvent.keyDown(screen.getByRole("textbox", { name: "message editor" }), { key: "Enter" });
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(
      expect.objectContaining({ text: "adjust the direction" }),
    ));
    expect(screen.getByRole("button", { name: "chat.send" })).toBeInTheDocument();
  });

  it("switches the streaming action between stop and send with the draft", () => {
    const props = {
      onChange: vi.fn(),
      onSend: vi.fn(),
      onStopGeneration: vi.fn(),
      isChatContent: true,
      isStreaming: true,
      sessionId: "conversation-1",
      showConversationConfig: false,
      showHistoryButton: false,
      showPromptSuggestions: false,
      showSkillDeposit: false,
      showThinkingDepth: false,
    };
    const { rerender } = render(<ChatInput {...props} value="" />);

    expect(screen.getByRole("button", { name: "chat.stopGenerate" })).toBeInTheDocument();

    rerender(<ChatInput {...props} value="guide this answer" />);
    expect(screen.getByRole("button", { name: "chat.send" })).toBeInTheDocument();

    rerender(<ChatInput {...props} value="" />);
    expect(screen.getByRole("button", { name: "chat.stopGenerate" })).toBeInTheDocument();
  });

  it("keeps the draft when queue submission is refused", async () => {
    const onChange = vi.fn();
    render(
      <ChatInput
        value="keep this draft"
        onChange={onChange}
        onSend={vi.fn().mockResolvedValue(false)}
        isChatContent
        isStreaming
        sessionId="conversation-1"
        showConversationConfig={false}
        showHistoryButton={false}
        showPromptSuggestions={false}
        showSkillDeposit={false}
        showThinkingDepth={false}
      />,
    );
    onChange.mockClear();
    fireEvent.keyDown(screen.getByRole("textbox", { name: "message editor" }), { key: "Enter" });
    await act(async () => { await Promise.resolve(); });
    expect(onChange).not.toHaveBeenCalledWith("");
  });

  it.each([false, true])('keeps stop clickable regardless of send readiness (streaming: %s)', (isStreaming) => {
    const onStopGeneration = vi.fn();
    const { rerender } = render(<ChatInput value="" onChange={vi.fn()} isChatContent
      isStreaming={isStreaming} canStopGeneration onStopGeneration={onStopGeneration} disabled
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false}
      showSkillDeposit={false} showThinkingDepth={false} />);
    const stop = screen.getByRole('button', { name: 'chat.stopGenerate' });
    expect(stop).toBeEnabled();
    fireEvent.click(stop);
    expect(onStopGeneration).toHaveBeenCalledOnce();
    rerender(<ChatInput value="" onChange={vi.fn()} isChatContent canStopGeneration={false}
      onStopGeneration={onStopGeneration} disabled showConversationConfig={false}
      showHistoryButton={false} showPromptSuggestions={false} showSkillDeposit={false} showThinkingDepth={false} />);
    expect(screen.getByRole('button', { name: 'chat.send' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'chat.stopGenerate' })).not.toBeInTheDocument();
  });

  it("keeps Send available when a workflow runs independently of the foreground Chat", () => {
    useWorkflowStore.setState({ sessionByConversation: { 'background-chat': {
      session_id: 'background-workflow', conversation_id: 'background-chat', controller_host: 'lazymind',
      workflow_id: 'workflow', workflow_mode: 'auto', status: 'active', current_step_id: '', created_at: '', updated_at: '',
      projection: { display: { status: 'running', reason: 'executing', approval_step_id: '' } },
    } } });
    onTestFinished(() => useWorkflowStore.setState({ sessionByConversation: {} }));
    render(<ChatInput value="hello" onChange={vi.fn()} sessionId="background-chat" isChatContent
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false}
      showSkillDeposit={false} showThinkingDepth={false} />);
    expect(screen.getByRole('button', { name: 'chat.send' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'chat.pause' })).not.toBeInTheDocument();
  });

  it("hides workspace selection and omits its fields in quick question mode", async () => {
    const onSend = vi.fn();
    const baseProps = {
      value: "hello", onChange: vi.fn(), onSend, isChatContent: true,
      showConversationConfig: false, showHistoryButton: false, showPromptSuggestions: false,
      showSkillDeposit: false, showThinkingDepth: false,
    };
    const { rerender } = render(<ChatInput {...baseProps} runInBackground />);
    fireEvent.click(screen.getByRole("button", { name: "select workspace" }));
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenLastCalledWith(expect.objectContaining({
        workspace_id: "grant-alpha", workspace_permission_mode: "allow_all",
      })));

    rerender(<ChatInput {...baseProps} runInBackground={false} />);
    expect(screen.queryByTestId("local-workspace-control")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
    const resetPayload = onSend.mock.calls[onSend.mock.calls.length - 1]?.[0];
    expect(resetPayload).not.toHaveProperty("workspace_id");
    expect(resetPayload).not.toHaveProperty("workspace_permission_mode");
  });

  it("does not show workspace selection in a formal quick question", () => {
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

    expect(screen.queryByTestId("local-workspace-control")).not.toBeInTheDocument();
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

  it("reports whether the compact resource picker is open, aligns its action icons, and closes after choosing an action", async () => {
    render(<ChatInput value="" onChange={vi.fn()} isChatContent
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} showSkillDeposit={false} />);

    const trigger = screen.getByRole("button", { name: "chat.addResource" });
    expect(trigger).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    const attachment = await screen.findByRole("button", { name: /chat\.addAttachment/ });
    const menu = document.getElementById(trigger.getAttribute("aria-controls")!)!;
    expect(menu).toContainElement(attachment);
    const actions = within(menu).getAllByRole("button");
    expect(actions).toHaveLength(4);
    for (const action of actions) {
      expect.soft(action.firstElementChild).toHaveClass("chat-add-resource-menu-icon");
    }
    const popoverInner = menu.closest<HTMLElement>(".ant-popover-inner")!;
    expect(popoverInner).not.toBeNull();
    expect.soft(popoverInner.style.height).toBe("");
    expect.soft(menu.closest<HTMLElement>(".ant-popover-inner-content")!.style.height).toBe("");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("button", { name: /chat\.promptTemplate/ }));
    expect(trigger).toHaveAttribute("aria-expanded", "false");
  });

  it("keeps the resource picker closed when the composer is disabled", () => {
    render(<ChatInput value="" onChange={vi.fn()} isChatContent disabled
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);

    const trigger = screen.getByRole("button", { name: "chat.addResource" });
    fireEvent.click(trigger);

    expect(trigger).toHaveAttribute("aria-disabled", "true");
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("button", { name: /chat\.addAttachment/ })).not.toBeInTheDocument();
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

    fireEvent.click(screen.getByRole("button", { name: "chat.addResource" }));

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

describe("ChatInput optional reranking", () => {
  afterEach(() => vi.restoreAllMocks());

  it.each(["selected", "mention"])("sends %s knowledge bases without reranking and notifies once per composer", async (source) => {
    const info = vi.spyOn(message, "info").mockImplementation(() => (() => {}) as never);
    const onSend = vi.fn();
    const mention: ChatMention = { mention_id: "kb", type: "knowledge_base", resource_id: "test-kb", display_name: "知识库" };
    if (source === "selected") {
      datasets.list.mockResolvedValueOnce({ data: { datasets: [{ dataset_id: "test-kb", display_name: "Test KB" }] } });
    }
    render(<ChatInput value="请检索知识库" onChange={vi.fn()} onSend={onSend} isChatContent sessionId={`optional-${source}`}
      embeddingReady multimodalEmbeddingReady rerankReady={false}
      chatConfig={source === "selected" ? { knowledgeBaseId: ["test-kb"] } : {}}
      boundMentions={source === "mention" ? [mention] : []}
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);

    if (source === "selected") {
      fireEvent.click(screen.getByRole("button", { name: "chat.addResource" }));
      fireEvent.click(await screen.findByRole("button", { name: /chat\.knowledgeBase/ }));
      expect(await screen.findByText("Test KB")).toBeInTheDocument();
      expect(screen.getByPlaceholderText("chat.searchKnowledge")).toBeEnabled();
    }

    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
    expect(onSend).toHaveBeenLastCalledWith(expect.objectContaining(source === "selected"
      ? { chatConfigSnapshot: expect.objectContaining({ knowledgeBaseId: ["test-kb"] }) }
      : { mentions: [mention] }));
    expect(info).toHaveBeenCalledWith({ key: "knowledge-rerank-optional", content: "chat.knowledgeSearchWithoutRerank", duration: 5 });

    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2));
    expect(info).toHaveBeenCalledTimes(1);
  });

  it.each([
    { ready: true, kb: true },
    { ready: null, kb: true },
    { ready: false, kb: false },
  ])("does not notify for unrelated or unconfirmed searches: %j", async ({ ready, kb }) => {
    const info = vi.spyOn(message, "info").mockImplementation(() => (() => {}) as never);
    const onSend = vi.fn();
    render(<ChatInput value="hello" onChange={vi.fn()} onSend={onSend} isChatContent sessionId="optional-no-notice"
      rerankReady={ready} chatConfig={{ knowledgeBaseId: kb ? ["test-kb"] : [] }}
      showConversationConfig={false} showHistoryButton={false} showPromptSuggestions={false} />);
    fireEvent.click(screen.getByRole("button", { name: "chat.send" }));
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(1));
    expect(info).not.toHaveBeenCalled();
  });
});
