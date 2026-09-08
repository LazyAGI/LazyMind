import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";
import AssistantMessage, { externalProviderDisplayName } from "./index";

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
  it.each([
    "initial_selection",
    "model_unavailable",
    "session_sticky",
    "retry_same_model",
    "fixed",
    "simple_task",
    "complex_task",
    "long_context",
    "default_balanced",
  ] as const)("shows the actual model and the %s routing reason", async (reason) => {
    expect(enUS.chat.modelRouteReason[reason]).toBeTruthy();
    expect(zhCN.chat.modelRouteReason[reason]).toBeTruthy();
    render(
      <AssistantMessage
        item={{
          role: "assistant",
          delta: "answer",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
          model_route: {
            mode: reason === "fixed" ? "fixed" : "auto",
            provider_name: "Fast",
            model_name: "fast-free",
            reason,
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

    const summary = screen.getByLabelText(
      reason === "fixed" ? "chat.modelRouteFixedLabel" : "chat.modelRouteAutoLabel",
    );
    expect(summary).toBeInTheDocument();
    fireEvent.mouseOver(summary);
    expect(
      await screen.findByText(`chat.modelRouteReason.${reason}`),
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

describe("externalProviderDisplayName", () => {
  it("presents the provider ID as WorkBuddy", () => {
    expect(externalProviderDisplayName("workbuddy")).toBe("WorkBuddy");
  });
});

describe("Fork message action", () => {
  const messageProps = {
    index: 0,
    length: 1,
    sendMessage: vi.fn(),
    regenerate: vi.fn(),
    regenerateDisabled: false,
    stopGeneration: vi.fn(),
    renderText: () => null,
    updateMessage: vi.fn(),
  };
  const persistedReply = {
    role: "assistant",
    history_id: "h1",
    delta: "answer",
    run_status: "completed",
    finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
  };

  it.each(["completed", undefined])(
    "forks a persisted %s reply with one toolbar click",
    (status) => {
      const onFork = vi.fn();
      render(
        <AssistantMessage
          {...messageProps}
          item={{ ...persistedReply, run_status: status }}
          onFork={onFork}
        />,
      );

      const action = screen.getByRole("button", { name: "chat.fork.title" });
      expect(action).toHaveTextContent(/^$/);
      expect(screen.queryByRole("button", { name: "chat.fork.more" })).toBeNull();
      fireEvent.click(action);
      expect(onFork).toHaveBeenCalledOnce();
      expect(onFork).toHaveBeenCalledWith("h1");
      expect(screen.queryByRole("menu")).toBeNull();
    },
  );

  it.each(["generating", "failed", "cancelled", "interrupted"])(
    "hides Fork on a %s reply while keeping the previous successful reply available",
    (status) => {
      const onFork = vi.fn();
      render(<>
        <AssistantMessage {...messageProps} length={2} item={persistedReply} onFork={onFork} />
        <AssistantMessage {...messageProps} index={1} length={2}
          item={{ ...persistedReply, history_id: "h2", run_status: status }} onFork={onFork} />
      </>);
      const actions = screen.getAllByRole("button", { name: "chat.fork.title" });
      expect(actions).toHaveLength(1);
      fireEvent.click(actions[0]);
      expect(onFork).toHaveBeenCalledOnce();
      expect(onFork).toHaveBeenCalledWith("h1");
    },
  );

  it("hides Fork while generating and restores it when the reply finishes", () => {
    const onFork = vi.fn();
    const { rerender } = render(
      <AssistantMessage
        {...messageProps}
        item={{ ...persistedReply, run_status: "generating" }}
        onFork={onFork}
      />,
    );

    expect(screen.queryByRole("button", { name: "chat.fork.title" })).toBeNull();
    expect(onFork).not.toHaveBeenCalled();

    rerender(
      <AssistantMessage {...messageProps} item={persistedReply} onFork={onFork} />,
    );
    const action = screen.getByRole("button", { name: "chat.fork.title" });
    expect(action).toBeEnabled();
    fireEvent.click(action);
    expect(onFork).toHaveBeenCalledWith("h1");
  });

  it("requires selecting a candidate before Fork and enables it after selection", async () => {
    const onFork = vi.fn();
    const item = {
      ...persistedReply,
      answers: [
        { index: 0, history_id: "h1", content: "first answer" },
        { index: 1, history_id: "h2", content: "second answer" },
      ],
    };
    const { rerender } = render(
      <AssistantMessage {...messageProps} item={item} onFork={onFork} />,
    );

    const action = screen.getByRole("button", { name: "chat.fork.title" });
    expect(action).toBeDisabled();
    fireEvent.click(action);
    expect(onFork).not.toHaveBeenCalled();
    fireEvent.focus(action.parentElement!);
    expect(
      await screen.findByText("chat.fork.selectAnswerFirst"),
    ).toBeInTheDocument();

    rerender(
      <AssistantMessage
        {...messageProps}
        item={{ ...item, selected_answer_index: 0 }}
        onFork={onFork}
      />,
    );
    const selectedAction = screen.getByRole("button", { name: "chat.fork.title" });
    expect(selectedAction).toBeEnabled();
    fireEvent.click(selectedAction);
    expect(onFork).toHaveBeenCalledOnce();
    expect(onFork).toHaveBeenCalledWith("h1");
  });

  it("disables Fork while another creation is pending", () => {
    const onFork = vi.fn();
    render(
      <AssistantMessage
        {...messageProps}
        item={persistedReply}
        onFork={onFork}
        forkPending
      />,
    );

    const action = screen.getByRole("button", { name: "chat.fork.title" });
    expect(action).toBeDisabled();
    fireEvent.click(action);
    expect(onFork).not.toHaveBeenCalled();
  });

  it.each([
    { archived_failure: true },
    { history_id: undefined },
  ])("hides Fork for an ineligible reply %j", (fields) => {
    render(
      <AssistantMessage
        {...messageProps}
        item={{ ...persistedReply, ...fields }}
        onFork={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: "chat.fork.title" })).toBeNull();
  });

  it("hides Fork when the conversation does not support it", () => {
    render(<AssistantMessage {...messageProps} item={persistedReply} />);
    expect(screen.queryByRole("button", { name: "chat.fork.title" })).toBeNull();
  });
});
