import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { AutoInteractionStatus, ChatComposer, ChatMessageStream } from "./ChatSection";
import type { SelfEvolutionChatMessage } from "./types";

function makeMessage(overrides: Partial<SelfEvolutionChatMessage> = {}): SelfEvolutionChatMessage {
  return {
    id: "msg-1",
    role: "assistant",
    content: "hello world",
    time: "10:00",
    ...overrides,
  };
}

describe("ChatMessageStream", () => {
  it("renders visible messages with agent label and content", () => {
    const messages = [makeMessage({ agentLabel: "Planner" })];
    renderWithProviders(
      <ChatMessageStream isAutoInteractionActive={false} messages={messages} streamRef={createRef()} />,
    );
    expect(screen.getByText("Planner")).toBeInTheDocument();
    expect(screen.getByText("hello world")).toBeInTheDocument();
    expect(screen.getByText("10:00")).toBeInTheDocument();
  });

  it("strips the legacy planning thinking text from message content", () => {
    const messages = [makeMessage({ content: "正在理解你的请求并规划下一步。real content" })];
    renderWithProviders(
      <ChatMessageStream isAutoInteractionActive={false} messages={messages} streamRef={createRef()} />,
    );
    expect(screen.getByText("real content")).toBeInTheDocument();
  });

  it("hides messages that start with a hidden status prefix", () => {
    const messages = [
      makeMessage({ id: "hidden-1", content: "已解析意图：do something" }),
      makeMessage({ id: "visible-1", content: "visible message" }),
    ];
    renderWithProviders(
      <ChatMessageStream isAutoInteractionActive={false} messages={messages} streamRef={createRef()} />,
    );
    expect(screen.queryByText(/已解析意图/)).not.toBeInTheDocument();
    expect(screen.getByText("visible message")).toBeInTheDocument();
  });

  it("shows the auto placeholder when there are no visible messages and auto interaction is active", () => {
    renderWithProviders(<ChatMessageStream isAutoInteractionActive messages={[]} streamRef={createRef()} />);
    expect(screen.getByText("selfEvolutionRun.autoMessagesPlaceholder")).toBeInTheDocument();
  });

  it("shows the empty placeholder when there are no visible messages and auto interaction is inactive", () => {
    renderWithProviders(
      <ChatMessageStream isAutoInteractionActive={false} messages={[]} streamRef={createRef()} />,
    );
    expect(screen.getByText("selfEvolutionRun.emptyChatPlaceholder")).toBeInTheDocument();
  });
});

describe("AutoInteractionStatus", () => {
  it("renders the auto interaction status text", () => {
    renderWithProviders(<AutoInteractionStatus />);
    expect(screen.getByText("selfEvolutionRun.autoInteractionStatus")).toBeInTheDocument();
  });
});

describe("ChatComposer", () => {
  const baseProps = {
    activeStepText: "step text",
    isSendingMessage: false,
    prompt: "",
    onPromptChange: vi.fn(),
    onSend: vi.fn(),
    renderKnowledgeAndModeTools: () => <div>tools</div>,
    renderSendButton: () => <button type="button">send</button>,
  };

  it("renders the auto interaction status and no textarea when in auto mode and not ended", () => {
    renderWithProviders(<ChatComposer {...baseProps} isAutoMode />);
    expect(screen.getByText("selfEvolutionRun.autoInteractionStatus")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("renders nothing when in auto mode and read-only ended", () => {
    const { container } = renderWithProviders(
      <ChatComposer {...baseProps} isAutoMode isReadOnlyEnded />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("calls onPromptChange when the textarea value changes", () => {
    const onPromptChange = vi.fn();
    renderWithProviders(
      <ChatComposer {...baseProps} isAutoMode={false} onPromptChange={onPromptChange} />,
    );
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "new text" } });
    expect(onPromptChange).toHaveBeenCalledWith("new text");
  });

  it("shows the sending label instead of the active step text while sending", () => {
    renderWithProviders(
      <ChatComposer {...baseProps} isAutoMode={false} isSendingMessage prompt="hi" />,
    );
    expect(screen.getByText("selfEvolutionRun.sendingMessage")).toBeInTheDocument();
    expect(screen.queryByText("step text")).not.toBeInTheDocument();
  });

  it("shows the checkpoint placeholder with the pending command when waiting on a checkpoint", () => {
    renderWithProviders(
      <ChatComposer
        {...baseProps}
        isAutoMode={false}
        pendingCheckpointWaitPrompt={{ message: "waiting", command: "continue" }}
      />,
    );
    expect(screen.getByPlaceholderText("selfEvolutionRun.checkpointInputPlaceholder")).toBeInTheDocument();
  });

  it("renders the knowledge/mode tools and send button", () => {
    renderWithProviders(<ChatComposer {...baseProps} isAutoMode={false} />);
    expect(screen.getByText("tools")).toBeInTheDocument();
    expect(screen.getByText("send")).toBeInTheDocument();
  });
});
