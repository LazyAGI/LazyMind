import { describe, expect, it, vi, beforeEach } from "vitest";
import { createRef } from "react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import BatchChatComponent, { type BatchChatImperativeProps } from "./index";

const mockListDatasets = vi.fn();
const mockBatchChat = vi.fn();
const mockPresignAttachment = vi.fn();
const mockGetBatchChatJob = vi.fn();
const mockPreviewBatchChatJobResult = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/components/ui", () => ({
  RiskTip: () => <span>risk-tip-stub</span>,
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => `errors.${code}`,
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatFileServiceApi: () => ({
    fileServicePresignAttachment: mockPresignAttachment,
  }),
  ChatServiceApi: () => ({
    conversationServicePreviewBatchChatJobResult: mockPreviewBatchChatJobResult,
    conversationServiceGetBatchChatJob: mockGetBatchChatJob,
    conversationServiceBatchChat: mockBatchChat,
  }),
  DatabaseBaseServiceApi: () => ({}),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: mockListDatasets,
  }),
}));

describe("BatchChatComponent", () => {
  beforeEach(() => {
    localStorage.clear();
    mockListDatasets.mockReset();
    mockBatchChat.mockReset();
    mockPresignAttachment.mockReset();
    mockGetBatchChatJob.mockReset();
    mockPreviewBatchChatJobResult.mockReset();
    mockListDatasets.mockResolvedValue({
      data: { datasets: [{ dataset_id: "ds-1", display_name: "Dataset One" }] },
    });
  });

  it("is hidden until opened through the imperative handle", () => {
    const ref = createRef<BatchChatImperativeProps>();
    render(<BatchChatComponent cancelFn={vi.fn()} ref={ref} />);
    expect(screen.queryByText("chat.batchChat")).not.toBeInTheDocument();
  });

  it("opens the modal and loads the knowledge base list", async () => {
    const ref = createRef<BatchChatImperativeProps>();
    render(<BatchChatComponent cancelFn={vi.fn()} ref={ref} />);

    ref.current?.onOpen();

    expect(await screen.findByText("chat.batchChat")).toBeInTheDocument();
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalledWith({ pageSize: 1000 }));
  });

  it("cancels back out of the modal via the cancel button", async () => {
    const ref = createRef<BatchChatImperativeProps>();
    render(<BatchChatComponent cancelFn={vi.fn()} ref={ref} />);
    ref.current?.onOpen();
    await screen.findByText("chat.batchChat");

    fireEvent.click(screen.getByText("common.cancel"));

    // jsdom lacks the `AnimationEvent` constructor, so rc-motion (used by
    // antd's Modal) falls back to listening for the vendor-prefixed
    // `webkitAnimationEnd` name instead of the plain `animationend` that
    // `fireEvent.animationEnd` dispatches. Without it the exit animation
    // never resolves and the wrapper is left visible; dispatch that event
    // manually on every node still carrying a motion class. Note antd keeps
    // the modal mounted (no `destroyOnClose`), so once the animation
    // resolves it becomes `display: none` rather than leaving the DOM.
    await waitFor(() => {
      document
        .querySelectorAll(".ant-fade-leave, .ant-zoom-leave")
        .forEach((node) => {
          node.dispatchEvent(new Event("webkitAnimationEnd", { bubbles: true }));
          fireEvent.transitionEnd(node);
        });
      expect(screen.getByText("chat.batchChat")).not.toBeVisible();
    }, { timeout: 3000 });
  });
});
