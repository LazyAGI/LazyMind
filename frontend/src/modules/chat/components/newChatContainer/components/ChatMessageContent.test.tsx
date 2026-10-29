import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ChatMessageContent from "./ChatMessageContent";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import { useThinkingCollapse } from "../hooks/useThinkingCollapse";
import type { ChatMessage } from "../types";
import { ChatConversationsResponseFinishReasonEnum } from "@/api/generated/chatbot-client";

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
  it("renders SDK thinking and completed tool previews once outside the restored answer body", () => {
    const messages = buildChatMessageListFromHistory([{ id: "h", query: "task",
      result: "<tp>COMMITTED TOOL</tp><trp>COMMITTED RESULT</trp>FINAL BODY",
      reasoning_content: "COMMITTED SDK THINK", model_output: { run_id: "run", call_index: 2, phase: "commit" } }]);
    render(<ChatMessageContent item={messages[1]} isThinkingCollapsed={() => false} onToggleThinkingCollapse={vi.fn()} />);
    expect(screen.getByText("FINAL BODY")).toBeInTheDocument();
    expect(screen.getAllByText(/COMMITTED SDK THINK/)).toHaveLength(1);
    expect(screen.getAllByText(/COMMITTED TOOL/)).toHaveLength(1);
    expect(screen.getByText(/COMMITTED SDK THINK/)).toHaveTextContent("COMMITTED RESULT");
  });
  it.each([
    { terminal: { run_status: "completed" }, title: "chat.thinkingDone" },
    { terminal: { run_status: "cancelled" }, title: "chat.runStatus.cancelled" },
    { terminal: { run_status: "interrupted" }, title: "chat.runStatus.interrupted" },
    { terminal: { run_status: "failed" }, title: "chat.runStatus.failed" },
    { terminal: { finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop }, title: "chat.thinkingDone" },
  ])("ends reasoning-only thinking for $title", ({ terminal, title }) => {
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
    expect(screen.getByText(`${title} (1m15s)`)).toBeInTheDocument();
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

describe("ChatMessageContent thinking terminal", () => {
  const failedTerminal = {
    status: "failed" as const, reason: "model_failure" as const,
    code: "model_fallback_exhausted", partial_output: true,
  };

  it.each(["generating", "running"])("keeps %s messages streaming until completion", (runStatus) => {
    const isThinkingCollapsed = vi.fn((_key: string, collapsed?: boolean) => collapsed ?? false);
    const props = { isThinkingCollapsed, onToggleThinkingCollapse: vi.fn() };
    const item = {
      role: "assistant", delta: "", reasoning_content: "KEPT THINK",
      run_status: runStatus, thinking_duration_s: 82,
    };
    const { rerender } = render(<ChatMessageContent {...props} item={item} />);
    expect(screen.getByText("chat.thinking (1m22s)")).toBeInTheDocument();
    expect(isThinkingCollapsed).toHaveBeenLastCalledWith("default", false);
    expect(screen.queryByText(/chat\.runStatus\./)).not.toBeInTheDocument();

    rerender(<ChatMessageContent {...props} item={{ ...item, delta: "KEPT ANSWER" }} />);
    expect(screen.getByText("chat.thinkingDone (1m22s)")).toBeInTheDocument();
    expect(isThinkingCollapsed).toHaveBeenLastCalledWith("default", false);

    rerender(<ChatMessageContent {...props} item={{ ...item, run_status: "completed" }} />);
    expect(screen.getByText("chat.thinkingDone (1m22s)")).toBeInTheDocument();
    expect(isThinkingCollapsed).toHaveBeenLastCalledWith("default", true);
    expect(screen.getAllByText("KEPT THINK")).toHaveLength(1);
  });

  it.each(["generating", "running"])("honors a stop marker with a stale %s status", (runStatus) => {
    const isThinkingCollapsed = vi.fn((_key: string, collapsed?: boolean) => collapsed ?? false);
    render(<ChatMessageContent
      item={{ role: "assistant", reasoning_content: "KEPT THINK", run_status: runStatus,
        finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop }}
      isThinkingCollapsed={isThinkingCollapsed} onToggleThinkingCollapse={vi.fn()}
    />);
    expect(screen.getByText("chat.thinkingDone")).toBeInTheDocument();
    expect(isThinkingCollapsed).toHaveBeenCalledWith("default", true);
    expect(screen.queryByText(/chat\.runStatus\./)).not.toBeInTheDocument();
  });

  it.each([
    { name: "active reasoning", item: {}, title: "chat.thinking" },
    { name: "active answer", item: { delta: "KEPT ANSWER" }, title: "chat.thinkingDone" },
    { name: "completed without answer", item: { run_status: "completed" }, title: "chat.thinkingDone" },
    { name: "failed without answer", item: { run_status: "failed" }, title: "chat.runStatus.failed" },
    { name: "failed partial answer", item: { run_status: "failed", delta: "KEPT ANSWER" }, title: "chat.runStatus.failed" },
    { name: "interrupted", item: { run_status: "interrupted" }, title: "chat.runStatus.interrupted" },
    { name: "cancelled", item: { run_status: "cancelled" }, title: "chat.runStatus.cancelled" },
    { name: "runtime failure terminal only", item: { run_terminal: { ...failedTerminal, reason: "runtime_failure" } }, title: "chat.runStatus.runtimeFailed" },
    { name: "model failure terminal only", item: { run_terminal: failedTerminal }, title: "chat.runStatus.failed" },
    { name: "terminal failure overrides completed status", item: { run_terminal: failedTerminal, run_status: "completed" }, title: "chat.runStatus.failed" },
    { name: "terminal failure overrides generating status", item: { run_terminal: failedTerminal, run_status: "generating" }, title: "chat.runStatus.failed" },
    { name: "terminal success overrides generating status", item: { run_terminal: { ...failedTerminal, status: "completed", reason: "normal" }, run_status: "generating" }, title: "chat.thinkingDone" },
    { name: "terminal success overrides failed status", item: { run_terminal: { ...failedTerminal, status: "completed", reason: "normal" }, run_status: "failed" }, title: "chat.thinkingDone" },
    { name: "legacy stop without answer", item: { finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop }, title: "chat.thinkingDone" },
  ])("uses the actual state for $name", ({ item, title }) => {
    const onToggle = vi.fn();
    render(<ChatMessageContent
      item={{ role: "assistant", delta: "", reasoning_content: "KEPT THINK", thinking_duration_s: 2, ...item }}
      isThinkingCollapsed={(_key, collapsed) => collapsed ?? false} onToggleThinkingCollapse={onToggle}
    />);
    expect(screen.getByText(`${title} (2s)`)).toBeInTheDocument();
    expect(screen.getAllByText("KEPT THINK")).toHaveLength(1);
    if ("run_terminal" in item) {
      fireEvent.click(screen.getByText(`${title} (2s)`));
      expect(onToggle).toHaveBeenCalledWith("default", true);
    }
  });

  it("restores an exhausted tool-only history without an active thinking title", () => {
    const messages = buildChatMessageListFromHistory([{
      id: "exhausted-history", query: "save once", run_id: "exhausted-run", run_status: "failed",
      run_terminal: failedTerminal, thinking_time_s: 1,
      model_output: { run_id: "exhausted-run", call_index: 4, phase: "rollback" },
      reasoning_content: "KEPT SDK THINK",
      result: 'KEPT TEXT<tp>KEPT TOOL</tp><tool_call>{"name":"save_chat_artifact"}</tool_call><trp>KEPT FILE</trp><tool_result>{"saved_count":1}</tool_result>',
    }]);
    expect(messages[1].delta).toBe("");
    render(<ChatMessageContent item={messages[1]} isThinkingCollapsed={() => false} onToggleThinkingCollapse={vi.fn()} />);
    expect(screen.getByText("chat.runStatus.failed (1s)")).toBeInTheDocument();
    expect(screen.queryByText(/chat.thinking/)).not.toBeInTheDocument();
    for (const text of ["KEPT TEXT", "KEPT TOOL", "KEPT FILE", "KEPT SDK THINK"]) {
      expect(screen.getAllByText(new RegExp(text))).toHaveLength(1);
    }
    expect(screen.queryByText(/saved_count/)).not.toBeInTheDocument();
  });

  it.each([
    { status: "completed" as const, title: "chat.thinkingDone" },
    { status: "failed" as const, title: "chat.runStatus.failed" },
  ])("keeps user expansion and content when streaming becomes $status", ({ status, title }) => {
    const onToggle = vi.fn();
    function Harness({ item }: { item: ChatMessage }) {
      const { isThinkingCollapsed, toggleThinkingCollapse } = useThinkingCollapse();
      return <ChatMessageContent item={item} isThinkingCollapsed={isThinkingCollapsed}
        onToggleThinkingCollapse={(key, collapsed) => {
          onToggle(key, collapsed);
          toggleThinkingCollapse(key, collapsed);
        }} />;
    }
    const item: ChatMessage = { role: "assistant", history_id: "stable-history", delta: "", reasoning_content: "KEPT THINK" };
    const { rerender } = render(<Harness item={item} />);
    fireEvent.click(screen.getByText("chat.thinking"));
    fireEvent.click(screen.getByText("chat.thinking"));
    expect(onToggle).toHaveBeenLastCalledWith("stable-history", true);
    rerender(<Harness item={{ ...item, delta: "KEPT ANSWER" }} />);
    expect(screen.getByText("chat.thinkingDone")).toBeInTheDocument();
    rerender(<Harness item={{ ...item, delta: "KEPT ANSWER", run_status: status }} />);
    expect(screen.getByText(title)).toBeInTheDocument();
    expect(screen.getAllByText("KEPT THINK")).toHaveLength(1);
    expect(screen.getAllByText("KEPT ANSWER")).toHaveLength(1);
    fireEvent.click(screen.getByText(title));
    expect(onToggle).toHaveBeenLastCalledWith("stable-history", false);
  });

  it("does not introduce a thinking section when reasoning is absent", () => {
    render(<ChatMessageContent item={{ role: "assistant", delta: "KEPT ANSWER", run_status: "failed" }}
      isThinkingCollapsed={() => false} onToggleThinkingCollapse={vi.fn()} />);
    expect(screen.queryByText(/chat.thinking|chat.runStatus/)).not.toBeInTheDocument();
    expect(screen.getByText("KEPT ANSWER")).toBeInTheDocument();
  });
});
