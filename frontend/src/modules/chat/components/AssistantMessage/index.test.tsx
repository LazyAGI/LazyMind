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
});
