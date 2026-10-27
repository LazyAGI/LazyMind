import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ChatConversationsResponseFinishReasonEnum } from "@/api/generated/chatbot-client";
import ChatMessageContent from "./ChatMessageContent";

vi.mock("react-i18next", async (importOriginal) => ({
  ...await importOriginal<typeof import("react-i18next")>(),
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      key === "chat.modelRetrying"
        ? `model service ${values?.attempt}/${values?.max}`
        : key,
  }),
}));

vi.mock("@/modules/chat/components/MarkdownViewer", () => ({
  default: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
}));

describe("ChatMessageContent provider retry", () => {
  it("keeps the provider retry inside the assistant message", () => {
    render(
      <ChatMessageContent
        item={{
          role: "assistant",
          delta: "partial output",
          model_retry: { retry_index: 1, max_attempts: 3 },
        }}
        isThinkingCollapsed={() => false}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );

    const status = screen.getByRole("status");
    expect(status).toHaveClass("chat-model-retry-status");
    expect(status).toHaveTextContent("model service 2/3");
    expect(screen.queryByText(/LazyMind/i)).not.toBeInTheDocument();
  });
});

describe("ChatMessageContent thinking duration", () => {
  it.each([
    { run_status: "completed" },
    { run_status: "cancelled" },
    { run_status: "interrupted" },
    { run_status: "failed" },
    { finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop },
  ])("ends reasoning-only thinking for %j", (terminal) => {
    const props = {
      isThinkingCollapsed: () => false,
      onToggleThinkingCollapse: vi.fn(),
    };
    const item = {
      role: "assistant", delta: "", reasoning_content: "reasoning",
      thinking_duration_s: 75,
    };
    const { rerender } = render(<ChatMessageContent {...props} item={item} />);
    expect(screen.getByText("chat.thinking (1m15s)")).toBeInTheDocument();

    rerender(<ChatMessageContent {...props} item={{ ...item, ...terminal }} />);
    expect(screen.getByText("chat.thinkingDone (1m15s)")).toBeInTheDocument();
    expect(screen.queryByText("chat.thinking (1m15s)")).not.toBeInTheDocument();

    rerender(<ChatMessageContent {...props} item={item} />);
    expect(screen.getByText("chat.thinking (1m15s)")).toBeInTheDocument();
  });

  it.each([
    [20, "20s"],
    [60, "1m"],
    [66, "1m6s"],
  ])("formats %s seconds as %s", (seconds, expected) => {
    render(
      <ChatMessageContent
        item={{
          role: "assistant",
          delta: "answer",
          reasoning_content: "reasoning",
          thinking_duration_s: seconds,
        }}
        isThinkingCollapsed={() => false}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );

    expect(screen.getByText(`chat.thinkingDone (${expected})`))
      .toBeInTheDocument();
  });
});
