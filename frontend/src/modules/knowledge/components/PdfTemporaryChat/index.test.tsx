import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import PdfTemporaryChat from "./index";
import { ChatConversationsRequestActionEnum } from "@/api/generated/chatbot-client";

const requests = vi.hoisted(() => ({ options: [] as Array<{ payload: string }> }));
const locale = vi.hoisted(() => ({ language: "en-US" }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: locale }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: { post: vi.fn() },
  getLocalizedErrorMessage: (error: unknown) => error instanceof Error ? error.message : "request failed",
}));
const ensureDocumentParsed = vi.hoisted(() => vi.fn().mockResolvedValue({ status: "parsing" }));
const retryDocumentParsed = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
vi.mock("@/modules/knowledge/api/pdfArtifacts", () => ({ ensureDocumentParsed, retryDocumentParsed }));
vi.mock("@/modules/chat/utils/request", () => ({
  CHAT_STREAM_URL: "/chat", CHAT_RESUME_STREAM_URL: "/resume", ChatServiceApi: vi.fn(),
}));
vi.mock("@/modules/chat/utils/message", () => ({ buildChatMessageListFromHistory: vi.fn() }));
vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { POST: "POST" },
  SSE: class { constructor(_url: string, options: { payload: string }) { requests.options.push(options); } },
}));
const chatActions = vi.hoisted(() => ({
  openSSE: undefined as undefined | ((...args: any[]) => unknown),
  allowLocalWorkspace: undefined as boolean | undefined,
  composer: {} as Record<string, unknown>,
  createNewChat: vi.fn(),
  sendMessage: vi.fn().mockResolvedValue(true),
  prepareMessage: vi.fn(),
}));
vi.mock("@/modules/chat/components/newChatContainer", async () => {
  const React = await import("react");
  return {
    default: React.forwardRef(({ onOpenSSE, allowLocalWorkspace, ...composer }: any, ref) => {
      chatActions.openSSE = onOpenSSE;
      chatActions.allowLocalWorkspace = allowLocalWorkspace;
      chatActions.composer = composer;
      React.useImperativeHandle(ref, () => ({
        createNewChat: chatActions.createNewChat,
        sendMessage: chatActions.sendMessage,
        prepareMessage: chatActions.prepareMessage,
      }));
      return <>{composer.initialCard}<button onClick={() => onOpenSSE([{ query: "Summarize this document" }], "query", {})}>Ask document</button></>;
    }),
  };
});

beforeEach(() => {
  locale.language = "en-US";
  requests.options.length = 0;
  chatActions.createNewChat.mockClear();
  chatActions.sendMessage.mockReset().mockResolvedValue(true);
  chatActions.prepareMessage.mockClear();
  ensureDocumentParsed.mockClear();
  retryDocumentParsed.mockClear();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("document question context", () => {
  it("prepares the same text again for a new selection action without duplicating unchanged selections", async () => {
    vi.useFakeTimers();
    ensureDocumentParsed.mockResolvedValueOnce({ status: "parsed" });
    const selection = { source: "pdf" as const, text: "capabilities", page: 1 };
    const props = { datasetId: "dataset-a", documentId: "document-md", fileName: "source.md", onClose: vi.fn() };
    const { rerender } = render(<PdfTemporaryChat {...props} selection={selection} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(chatActions.prepareMessage).toHaveBeenCalledOnce();

    rerender(<PdfTemporaryChat {...props} selection={selection} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(chatActions.prepareMessage).toHaveBeenCalledOnce();

    rerender(<PdfTemporaryChat {...props} selection={{ ...selection }} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(chatActions.prepareMessage).toHaveBeenCalledTimes(2);
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ citeMessage: "capabilities", appendCitations: true });
  });

  it("starts on-demand parsing when document chat is first opened", async () => {
    render(<PdfTemporaryChat datasetId="dataset-a" documentId="document-pdf" fileName="source.pdf" onClose={() => {}} />);
    await act(async () => { await Promise.resolve(); });
    expect(ensureDocumentParsed).toHaveBeenCalledWith("dataset-a", "document-pdf");
    expect(screen.getByText("knowledge.pdfChatParsing")).toBeInTheDocument();
  });

  it("shows a failed status with its reason without implicitly retrying", async () => {
    ensureDocumentParsed.mockResolvedValueOnce({ status: "failed", error: { message: "Reader could not decode the PDF" } });
    render(<PdfTemporaryChat datasetId="dataset-a" documentId="broken-pdf" fileName="broken.pdf" onClose={() => {}} />);
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("knowledge.pdfChatParseFailed")).toBeInTheDocument();
    expect(screen.getByText("Reader could not decode the PDF")).toBeInTheDocument();
    expect(ensureDocumentParsed).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "knowledge.pdfChatRetryParse" })).toBeInTheDocument();
    expect(retryDocumentParsed).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "knowledge.pdfChatRetryParse" }));
    await act(async () => { await Promise.resolve(); });
    expect(retryDocumentParsed).toHaveBeenCalledWith("dataset-a", "broken-pdf");
    expect(screen.getByText("knowledge.pdfChatParsing")).toBeInTheDocument();
    expect(screen.queryByText("Reader could not decode the PDF")).not.toBeInTheDocument();
  });

  it("distinguishes chat text readiness from formula parsing", async () => {
    ensureDocumentParsed.mockResolvedValueOnce({ status: "parsed" });
    render(<PdfTemporaryChat datasetId="dataset-a" documentId="ready-pdf" fileName="ready.pdf" onClose={() => {}} />);
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByRole("status")).toHaveTextContent("Paper ready");
    expect(screen.queryByText("knowledge.pdfChatParsing")).not.toBeInTheDocument();
  });

  it("replaces a running indicator with the terminal parser failure and stops polling", async () => {
    vi.useFakeTimers();
    ensureDocumentParsed.mockResolvedValueOnce({ status: "parsing" }).mockResolvedValueOnce({ status: "failed", error: { message: "Root storage unavailable" } });
    render(<PdfTemporaryChat datasetId="dataset-a" documentId="stalled-pdf" fileName="stalled.pdf" onClose={() => {}} />);
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("knowledge.pdfChatParsingHint")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(screen.getByText("Root storage unavailable")).toBeInTheDocument();
    expect(screen.queryByText("knowledge.pdfChatParsing")).not.toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(ensureDocumentParsed).toHaveBeenCalledTimes(2);
    expect(retryDocumentParsed).not.toHaveBeenCalled();
  });

  it("refreshes an unknown status without requesting a new parse attempt", async () => {
    ensureDocumentParsed.mockRejectedValueOnce(new Error("Status service unavailable")).mockResolvedValueOnce({ status: "parsed" });
    render(<PdfTemporaryChat datasetId="dataset-a" documentId="unknown-pdf" fileName="unknown.pdf" onClose={() => {}} />);
    await act(async () => { await Promise.resolve(); });
    fireEvent.click(screen.getByRole("button", { name: "knowledge.pdfChatRefreshStatus" }));
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByRole("status")).toHaveTextContent("Paper ready");
    expect(screen.queryByText("Status service unavailable")).not.toBeInTheDocument();
    expect(retryDocumentParsed).not.toHaveBeenCalled();
  });

  it("does not turn a failed status lookup into parsing", async () => {
    ensureDocumentParsed.mockRejectedValueOnce(new Error("unavailable"));
    render(<PdfTemporaryChat datasetId="dataset-a" documentId="unknown-pdf" fileName="unknown.pdf" onClose={() => {}} />);
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("knowledge.pdfChatUnknown")).toBeInTheDocument();
    expect(screen.queryByText("knowledge.pdfChatParsing")).not.toBeInTheDocument();
    expect(retryDocumentParsed).not.toHaveBeenCalled();
  });

  it.each(["pdf", "txt", "md", "docx", "html", "xlsx", "pptx"])("submits %s questions scoped to the active document", (extension) => {
    render(<PdfTemporaryChat datasetId="dataset-a" documentId={`document-${extension}`} fileName={`source.${extension}`} onClose={() => {}} />);
    expect(chatActions.allowLocalWorkspace).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Ask document" }));
    const payload = JSON.parse(requests.options[0].payload);
    expect(payload.document_context).toEqual({
      dataset_id: "dataset-a", document_id: `document-${extension}`, file_name: `source.${extension}`,
    });
    expect(payload.initial_conversation_settings.source_document_id).toBe(`document-${extension}`);
    expect(payload.surface).toBe("knowledge_document_preview");
    expect(payload.conversation.search_config.dataset_list).toEqual([{ id: "dataset-a" }]);
    expect(payload.input).toEqual([{ query: "Summarize this document" }]);
  });

  it("forwards the selected text and its paragraph independently of chunks", () => {
    render(<PdfTemporaryChat
      datasetId="dataset-a"
      documentId="document-pdf"
      fileName="source.pdf"
      selection={{
        source: "pdf",
        text: "batch size tokens",
        context: "In LLM training, batch size is commonly reported as batch size tokens.",
        page: 4,
        bbox: [10, 20, 30, 40],
      }}
      onClose={() => {}}
    />);
    fireEvent.click(screen.getByRole("button", { name: "Ask document" }));
    const payload = JSON.parse(requests.options[0].payload);
    expect(payload.document_context).toMatchObject({
      selected_text: "batch size tokens",
      paragraph_text: "In LLM training, batch size is commonly reported as batch size tokens.",
      page: 4,
      bbox: [10, 20, 30, 40],
    });
  });

  it("starts a new temporary chat and sends a concise contextual translation request", async () => {
    vi.useFakeTimers();
    let accept!: (sent: boolean) => void;
    chatActions.sendMessage.mockImplementationOnce(() => new Promise<boolean>(resolve => { accept = resolve; }));
    const selection = {
      source: "pdf" as const,
      text: "Workloads",
      context: "Diverse Workloads Require Flexible Compute",
      page: 3,
    };
    render(<PdfTemporaryChat
      datasetId="dataset-a"
      documentId="document-pdf"
      fileName="source.pdf"
      selection={selection}
      translationRequest={{ id: 1, selection }}
      onClose={() => {}}
    />);

    // Finish selection preparation while translation acceptance is still pending.
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(chatActions.createNewChat).toHaveBeenCalledOnce();
    expect(chatActions.sendMessage).toHaveBeenCalledOnce();
    expect(chatActions.sendMessage).toHaveBeenCalledWith(expect.objectContaining({
      citeMessage: "Workloads",
      text: expect.stringContaining("当前语境下最合适的中文释义"),
    }));
    await act(async () => accept(true));
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ text: "", citeMessages: [] });
  });

  it("keeps citations cleared when translation succeeds before selection preparation", async () => {
    vi.useFakeTimers();
    chatActions.sendMessage.mockResolvedValueOnce(true);
    const selection = { source: "pdf" as const, text: "Workloads", page: 3 };
    const props = {
      datasetId: "dataset-a", documentId: "document-pdf", fileName: "source.pdf",
      selection, translationRequest: { id: 1, selection }, onClose: vi.fn(),
    };
    const { rerender } = render(<PdfTemporaryChat {...props} />);

    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(chatActions.sendMessage).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ text: "", citeMessages: [] });

    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(chatActions.prepareMessage).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ text: "", citeMessages: [] });

    rerender(<PdfTemporaryChat {...props} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(chatActions.sendMessage).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenCalledOnce();

    // A fresh selection action can quote the same text after translation.
    rerender(<PdfTemporaryChat {...props} selection={{ ...selection }} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(chatActions.prepareMessage).toHaveBeenCalledTimes(2);
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ citeMessage: "Workloads", appendCitations: true });
  });

  it("keeps the selection available when translation is not sent", async () => {
    vi.useFakeTimers();
    chatActions.sendMessage.mockResolvedValueOnce(false);
    const selection = { source: "pdf" as const, text: "Workloads", page: 3 };
    render(<PdfTemporaryChat
      datasetId="dataset-a"
      documentId="document-pdf"
      fileName="source.pdf"
      selection={selection}
      translationRequest={{ id: 1, selection }}
      onClose={() => {}}
    />);

    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(chatActions.sendMessage).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ citeMessage: "Workloads", appendCitations: true });
  });

  it("does not consume the new chat selection when an earlier translation finishes", async () => {
    vi.useFakeTimers();
    let accept!: (sent: boolean) => void;
    chatActions.sendMessage.mockImplementationOnce(() => new Promise<boolean>(resolve => { accept = resolve; }));
    const selection = { source: "pdf" as const, text: "Workloads", page: 3 };
    render(<PdfTemporaryChat
      datasetId="dataset-a"
      documentId="document-pdf"
      fileName="source.md"
      selection={selection}
      translationRequest={{ id: 1, selection }}
      onClose={() => {}}
    />);

    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    fireEvent.click(screen.getByRole("button", { name: /knowledge.pdfChatNew/ }));
    await act(async () => accept(true));
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });

    expect(chatActions.sendMessage).toHaveBeenCalledOnce();
    expect(chatActions.createNewChat).toHaveBeenCalledTimes(2);
    expect(chatActions.prepareMessage).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ citeMessage: "Workloads", appendCitations: true });
  });
});


it("uses the paper composer and a draft model catalog without disabling keyboard invocations",()=>{
 render(<PdfTemporaryChat datasetId="d" documentId="draft-paper-test" fileName="paper.pdf" onClose={vi.fn()}/>);
 expect(chatActions.composer).toMatchObject({composerVariant:"paper",showModelSelector:true,modelSelectionDraft:true});
 expect(chatActions.composer.allowMentions).not.toBe(false);
});


it('removes the fixed new/save action row only from paper chat',()=>{
 const props={datasetId:'d',documentId:'paper-actions-test',onClose:vi.fn()};
 const {rerender,container}=render(<PdfTemporaryChat {...props} fileName="paper.pdf"/>);
 expect(container.querySelector(".pdf-temporary-chat--paper")).not.toBeNull();
 expect(screen.getByRole('button',{name:/knowledge.pdfChatNew/})).toBeInTheDocument();
 expect(screen.queryByRole('button',{name:/knowledge.pdfChatSave/})).not.toBeInTheDocument();
 rerender(<PdfTemporaryChat {...props} fileName="notes.md"/>);
 expect(container.querySelector(".pdf-temporary-chat--paper")).toBeNull();
 expect(screen.getByRole('button',{name:/knowledge.pdfChatNew/})).toBeInTheDocument();
 expect(screen.getByRole('button',{name:/knowledge.pdfChatSave/})).toBeInTheDocument();
});


it.each([ChatConversationsRequestActionEnum.ChatActionNext,ChatConversationsRequestActionEnum.ChatActionRegeneration])('keeps current paper scope on %s without an attached file or selected text',action=>{
 render(<PdfTemporaryChat datasetId="paper-dataset" documentId="current-paper" fileName="current.pdf" onClose={vi.fn()}/>);
 chatActions.openSSE?.([{query:'Explain the core contribution of this paper.'}],action,{}, {
  document_context:{dataset_id:'stale-dataset',document_id:'stale-paper'},
  conversation:{search_config:{dataset_list:[]}},
 });
 const payload=JSON.parse(requests.options[0].payload);
 expect(payload.action).toBe(action);
 expect(payload.surface).toBe('knowledge_document_preview');
 expect(payload.document_context).toEqual({dataset_id:'paper-dataset',document_id:'current-paper',file_name:'current.pdf'});
 expect(payload.conversation.search_config.dataset_list).toEqual([{id:'paper-dataset'}]);
 expect(payload.input).toEqual([{query:'Explain the core contribution of this paper.'}]);
});


describe("paper assistant workspace", () => {
  it("prepares each suggested question without sending it", () => {
    render(<PdfTemporaryChat datasetId="d" documentId="suggestions" fileName="paper.pdf" onClose={vi.fn()} />);
    for (const label of ["Core contribution", "Explain the method", "Key results", "Limitations"]) {
      fireEvent.click(screen.getByRole("button", { name: new RegExp(label) }));
      expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ text: expect.any(String), appendCitations: true });
    }
    expect(chatActions.prepareMessage).toHaveBeenCalledTimes(4);
    expect(chatActions.sendMessage).not.toHaveBeenCalled();
    expect(requests.options).toHaveLength(0);
  });

  it("starts a fresh paper conversation from the header", () => {
    const onConversationChange = vi.fn();
    render(<PdfTemporaryChat datasetId="d" documentId="new-paper" fileName="paper.pdf" onClose={vi.fn()} onConversationChange={onConversationChange} />);
    fireEvent.click(screen.getByRole("button", { name: /knowledge.pdfChatNew/ }));
    expect(chatActions.createNewChat).toHaveBeenCalledOnce();
    expect(onConversationChange).toHaveBeenCalledWith(undefined);
  });

  it("removes dismissed source context from both composer and request", async () => {
    vi.useFakeTimers();
    const onClearSelection = vi.fn();
    const selection = { source: "pdf" as const, text: "A quoted result", page: 4 };
    render(<PdfTemporaryChat datasetId="d" documentId="quote" fileName="paper.pdf" selection={selection} onClearSelection={onClearSelection} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Remove source quote" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(onClearSelection).toHaveBeenCalledOnce();
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ citeMessages: [] });
    fireEvent.click(screen.getByRole("button", { name: "Ask document" }));
    expect(JSON.parse(requests.options[0].payload).document_context.selected_text).toBeUndefined();
    expect(screen.queryByText("A quoted result")).not.toBeInTheDocument();
  });
});

it("localizes paper assistant and suggested questions in Chinese", async () => {
  locale.language = "zh-CN";
  ensureDocumentParsed.mockResolvedValueOnce({status:"parsed"});
  render(<PdfTemporaryChat datasetId="d" documentId="p" fileName="p.pdf" onClose={vi.fn()} />);
  await screen.findByText("论文已就绪");
  expect(screen.getByText("论文助手")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", {name:/核心贡献/}));
  expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({text: expect.stringContaining("请解释这篇论文"), appendCitations:true});
  expect(screen.queryByText("Make sense of this paper")).toBeNull();
});

it('prepares the selection question with its citation without sending it',async()=>{
 vi.useFakeTimers();
 render(<PdfTemporaryChat datasetId="d" documentId="question-draft" fileName="paper.pdf" selection={{source:'pdf',text:'Original quote',page:2,question:'Why is this true?'}} onClose={vi.fn()}/>);
 await act(async()=>{await vi.advanceTimersByTimeAsync(50)});
 expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({text:'Why is this true?',citeMessage:'Original quote',appendCitations:true});
 expect(chatActions.sendMessage).not.toHaveBeenCalled();
});

 it('does not overwrite the chat draft when a quoted selection has a blank question',async()=>{
 vi.useFakeTimers();
 render(<PdfTemporaryChat datasetId="d" documentId="quote-preserves-draft" fileName="paper.pdf" selection={{source:'pdf',text:'Additional quote',page:2,question:'   '}} onClose={vi.fn()}/>);
 await act(async()=>{await vi.advanceTimersByTimeAsync(50)});
 expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({citeMessage:'Additional quote',appendCitations:true});
 expect(chatActions.sendMessage).not.toHaveBeenCalled();
 });
