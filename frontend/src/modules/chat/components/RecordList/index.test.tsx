import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, waitFor, fireEvent, act } from "@/test/testUtils";
import RecordList, { type RecordListImperativeProps } from "./index";

const mockListConversations = vi.fn();
const mockDeleteConversation = vi.fn();
const mockBatchDelete = vi.fn();
const mockExport = vi.fn();

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: (...args: unknown[]) =>
      mockListConversations(...args),
    conversationServiceDeleteConversation: (...args: unknown[]) =>
      mockDeleteConversation(...args),
  }),
}));

vi.mock("@/api/generated/core-client", async () => {
  const actual = await vi.importActual<typeof import("@/api/generated/core-client")>(
    "@/api/generated/core-client",
  );
  return {
    ...actual,
    ConversationsApiFactory: () => ({
      apiCoreConversationExportPost: (...args: unknown[]) => mockExport(...args),
    }),
    DefaultApiFactory: () => ({
      apiCoreConversationsBatchDeletePost: (...args: unknown[]) =>
        mockBatchDelete(...args),
    }),
  };
});

vi.mock("@/modules/chat/utils/download", () => ({
  downloadStream: vi.fn(),
}));

function conv(id: string, name: string, updateTime: string) {
  return { conversation_id: id, display_name: name, update_time: updateTime };
}

describe("RecordList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    if (!Element.prototype.scrollTo) {
      Element.prototype.scrollTo = vi.fn();
    }
    mockListConversations.mockResolvedValue({
      data: { conversations: [conv("c1", "First chat", new Date().toISOString())], next_page_token: "" },
    });
    mockDeleteConversation.mockResolvedValue({});
    mockBatchDelete.mockResolvedValue({ data: { deleted_count: 1 } });
  });

  it("loads and displays the conversation history", async () => {
    renderWithProviders(
      <RecordList currentSessionId="" onSelected={vi.fn()} onRemove={vi.fn()} />,
    );
    await waitFor(() => expect(mockListConversations).toHaveBeenCalled());
    expect(await screen.findByText("First chat")).toBeInTheDocument();
  });

  it("shows the empty state when there is no history", async () => {
    mockListConversations.mockResolvedValue({ data: { conversations: [], next_page_token: "" } });
    renderWithProviders(
      <RecordList currentSessionId="" onSelected={vi.fn()} onRemove={vi.fn()} />,
    );
    await waitFor(() => expect(screen.getByText("chat.noConversations")).toBeInTheDocument());
  });

  it("calls onSelected when a non-active conversation item is clicked", async () => {
    const onSelected = vi.fn();
    renderWithProviders(
      <RecordList currentSessionId="" onSelected={onSelected} onRemove={vi.fn()} />,
    );
    const item = await screen.findByText("First chat");
    fireEvent.click(item.closest(".record")!);
    expect(onSelected).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: "c1" }));
  });

  it("deletes a conversation when the close icon is clicked", async () => {
    const onRemove = vi.fn();
    renderWithProviders(
      <RecordList currentSessionId="" onSelected={vi.fn()} onRemove={onRemove} />,
    );
    await screen.findByText("First chat");
    const closeIcon = document.querySelector(".record .close") as HTMLElement;
    fireEvent.click(closeIcon);
    expect(mockDeleteConversation).toHaveBeenCalledWith({ conversation: "c1" });
    expect(onRemove).toHaveBeenCalled();
  });

  it("refreshes the history via the imperative ref", async () => {
    const ref = createRef<RecordListImperativeProps>();
    renderWithProviders(
      <RecordList ref={ref} currentSessionId="" onSelected={vi.fn()} onRemove={vi.fn()} />,
    );
    await waitFor(() => expect(mockListConversations).toHaveBeenCalledTimes(1));
    act(() => {
      ref.current?.refresh();
    });
    await waitFor(() => expect(mockListConversations).toHaveBeenCalledTimes(2));
  });

  it("searches conversations via the search input", async () => {
    renderWithProviders(
      <RecordList currentSessionId="" onSelected={vi.fn()} onRemove={vi.fn()} />,
    );
    await screen.findByText("First chat");
    const searchInput = document.querySelector(".record-toolbar-search input") as HTMLInputElement;
    fireEvent.change(searchInput, { target: { value: "hello" } });
    fireEvent.click(document.querySelector(".ant-input-search-button")!);
    await waitFor(() => {
      const lastCall = mockListConversations.mock.calls.at(-1);
      expect(lastCall?.[0]).toMatchObject({ keyword: "hello" });
    });
  });

  it("shows a warning and skips deleting when no conversations are checked in batch mode", async () => {
    renderWithProviders(
      <RecordList
        currentSessionId=""
        onSelected={vi.fn()}
        onRemove={vi.fn()}
        showBatchActions
      />,
    );
    await screen.findByText("First chat");
    fireEvent.click(screen.getByText("chat.batch"));
    fireEvent.click(screen.getByText("common.actions"));
    fireEvent.click(screen.getByText("common.delete"));
    expect(mockBatchDelete).not.toHaveBeenCalled();
  });
});
