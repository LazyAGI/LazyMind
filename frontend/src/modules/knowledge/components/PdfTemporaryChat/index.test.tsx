import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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
vi.mock("@/modules/chat/components/newChatContainer", async () => ({
  default: (await import("react")).forwardRef(({ onOpenSSE }: any, _ref) => (
    <button onClick={() => onOpenSSE([{ query: "Summarize this document" }], "query", {})}>Ask document</button>
  )),
}));

beforeEach(() => { requests.options.length = 0; });
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
    expect(payload.conversation.search_config.dataset_list).toEqual([{ id: "dataset-a" }]);
    expect(payload.input).toEqual([{ query: "Summarize this document" }]);
  });
});
