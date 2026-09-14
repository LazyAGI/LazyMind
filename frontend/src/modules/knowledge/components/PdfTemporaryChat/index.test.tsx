import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import PdfTemporaryChat from "./index";

const requests = vi.hoisted(() => ({ options: [] as Array<{ payload: string }> }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { post: vi.fn() } }));
vi.mock("@/modules/chat/utils/request", () => ({
  CHAT_STREAM_URL: "/chat", CHAT_RESUME_STREAM_URL: "/resume", ChatServiceApi: vi.fn(),
}));
vi.mock("@/modules/chat/utils/message", () => ({ buildChatMessageListFromHistory: vi.fn() }));
vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { POST: "POST" },
  SSE: class { constructor(_url: string, options: { payload: string }) { requests.options.push(options); } },
}));
const chatActions = vi.hoisted(() => ({
  createNewChat: vi.fn(),
  sendMessage: vi.fn().mockResolvedValue(true),
  prepareMessage: vi.fn(),
}));
vi.mock("@/modules/chat/components/newChatContainer", async () => {
  const React = await import("react");
  return {
    default: React.forwardRef(({ onOpenSSE }: any, ref) => {
      React.useImperativeHandle(ref, () => ({
        createNewChat: chatActions.createNewChat,
        sendMessage: chatActions.sendMessage,
        prepareMessage: chatActions.prepareMessage,
      }));
      return <button onClick={() => onOpenSSE([{ query: "Summarize this document" }], "query", {})}>Ask document</button>;
    }),
  };
});

beforeEach(() => {
  requests.options.length = 0;
  chatActions.createNewChat.mockClear();
  chatActions.sendMessage.mockClear();
  chatActions.prepareMessage.mockClear();
});
afterEach(cleanup);

describe("document question context", () => {
  it.each(["pdf", "txt", "md", "docx", "html", "xlsx", "pptx"])("submits %s questions scoped to the active document", (extension) => {
    render(<PdfTemporaryChat datasetId="dataset-a" documentId={`document-${extension}`} fileName={`source.${extension}`} onClose={() => {}} />);
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

    await act(async () => { await new Promise((resolve) => window.setTimeout(resolve, 10)); });
    expect(chatActions.createNewChat).toHaveBeenCalledOnce();
    expect(chatActions.sendMessage).toHaveBeenCalledWith(expect.objectContaining({
      citeMessage: "Workloads",
      text: expect.stringContaining("当前语境下最合适的中文释义"),
    }));
    expect(chatActions.prepareMessage).toHaveBeenLastCalledWith({ text: "", citeMessages: [] });
  });
});
