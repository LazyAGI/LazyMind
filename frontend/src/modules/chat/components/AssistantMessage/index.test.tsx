import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import AssistantMessage from "./index";

vi.mock("react-i18next", () => ({
  initReactI18next: {
    type: "3rdParty",
    init: () => undefined,
  },
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/chat/store/workflowPanel", () => ({
  useWorkflowStore: () => null,
}));

vi.mock("@/modules/chat/components/WorkflowPanel", () => ({
  WorkflowPanel: () => null,
}));

vi.mock("@/modules/identityAvatar", () => ({
  IdentityAvatar: () => null,
}));

describe("AssistantMessage cancellation", () => {
  it("shows the actual model selected for an Auto response", () => {
    render(
      <AssistantMessage
        item={{
          role: "assistant",
          delta: "answer",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
          model_route: {
            mode: "auto",
            provider_name: "Fast",
            model_name: "fast-free",
            reason: "simple_task",
          },
        }}
        index={0}
        length={1}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        regenerateDisabled={false}
        stopGeneration={vi.fn()}
        renderText={() => null}
        updateMessage={vi.fn()}
      />,
    );

    expect(
      screen.getByLabelText("chat.modelRouteAutoLabel"),
    ).toBeInTheDocument();
  });

  it("offers regeneration after the user stops generation", () => {
    const regenerate = vi.fn();

    render(
      <AssistantMessage
        item={{
          role: "assistant",
          delta: "partial answer",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
          run_status: "cancelled",
          run_terminal: {
            status: "cancelled",
            reason: "user_cancelled",
            partial_output: true,
          },
        }}
        index={0}
        length={1}
        sendMessage={vi.fn()}
        regenerate={regenerate}
        regenerateDisabled={false}
        stopGeneration={vi.fn()}
        renderText={() => null}
        updateMessage={vi.fn()}
      />,
    );

    const retryButton = screen.getByRole("button", {
      name: "chat.regenerate",
    });
    fireEvent.click(retryButton);

    expect(regenerate).toHaveBeenCalledOnce();
  });

  it("offers retry only on the latest failed assistant message", () => {
    const regenerate = vi.fn();
    const props = {
      sendMessage: vi.fn(),
      regenerate,
      regenerateDisabled: false,
      stopGeneration: vi.fn(),
      renderText: () => null,
      updateMessage: vi.fn(),
    };
    const failedItem = {
      role: "assistant",
      delta: "",
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "model_failure",
        code: "rate_limited",
        partial_output: false,
      },
    };
    const { rerender } = render(
      <AssistantMessage
        item={failedItem}
        index={1}
        length={3}
        {...props}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "chat.tryAgain" }),
    ).not.toBeInTheDocument();

    rerender(
      <AssistantMessage
        item={failedItem}
        index={2}
        length={3}
        {...props}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "chat.tryAgain" }));
    expect(regenerate).toHaveBeenCalledOnce();
  });

  it("does not render a legacy provider error message", () => {
    render(
      <AssistantMessage
        item={{
          role: "assistant",
          delta: "",
          errMessage: "provider-secret: invalid api key",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown,
        }}
        index={0}
        length={1}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        regenerateDisabled={false}
        stopGeneration={vi.fn()}
        renderText={() => null}
        updateMessage={vi.fn()}
      />,
    );

    expect(screen.queryByText(/provider-secret/)).not.toBeInTheDocument();
    expect(screen.getByText("chat.runStatus.providerError")).toBeInTheDocument();
  });

  it("opens a side chat with the selected text and source position", () => {
    const onOpenSideChat = vi.fn();
    render(
      <AssistantMessage
        item={{
          id: "assistant-1",
          history_id: "history-1",
          seq: 7,
          role: "assistant",
          delta: "selected answer",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
        }}
        index={0}
        length={1}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        regenerateDisabled={false}
        stopGeneration={vi.fn()}
        renderText={() => <span>selected answer</span>}
        updateMessage={vi.fn()}
        onOpenSideChat={onOpenSideChat}
      />,
    );

    const selected = screen.getByText("selected answer");
    const range = document.createRange();
    range.selectNodeContents(selected);
    Object.defineProperty(range, "getClientRects", {
      value: () => [
        { top: 80, left: 100, right: 220, width: 120, height: 20 },
      ],
    });
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);

    fireEvent.mouseUp(selected);
    fireEvent.click(
      screen.getByRole("button", {
        name: "chat.sideChat.askFromSelection",
      }),
    );

    expect(onOpenSideChat).toHaveBeenCalledWith({
      selectedText: "selected answer",
      historyId: "history-1",
      sequence: 7,
    });
  });

  it("opens the selection actions for a keyboard text selection", () => {
    const onOpenSideChat = vi.fn();
    render(
      <AssistantMessage
        item={{
          id: "assistant-keyboard",
          history_id: "history-keyboard",
          seq: 8,
          role: "assistant",
          delta: "keyboard selection",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
        }}
        index={0}
        length={1}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        regenerateDisabled={false}
        stopGeneration={vi.fn()}
        renderText={() => <span>keyboard selection</span>}
        updateMessage={vi.fn()}
        onOpenSideChat={onOpenSideChat}
      />,
    );

    const selected = screen.getByText("keyboard selection");
    const range = document.createRange();
    range.selectNodeContents(selected);
    Object.defineProperty(range, "getClientRects", {
      value: () => [
        { top: 80, left: 100, right: 220, width: 120, height: 20 },
      ],
    });
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);

    fireEvent.keyUp(selected, { key: "ArrowRight", shiftKey: true });
    fireEvent.click(
      screen.getByRole("button", {
        name: "chat.sideChat.askFromSelection",
      }),
    );

    expect(onOpenSideChat).toHaveBeenCalledWith({
      selectedText: "keyboard selection",
      historyId: "history-keyboard",
      sequence: 8,
    });
  });
});
