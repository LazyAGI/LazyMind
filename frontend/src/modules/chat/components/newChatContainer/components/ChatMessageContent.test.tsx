import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ChatMessageContent from "./ChatMessageContent";

vi.mock("react-i18next", () => ({
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
