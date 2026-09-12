import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { FeedBackChatHistoryRequestTypeEnum as Feedback } from "@/api/generated/chatbot-client";
import AssistantMessage from "./index";

const mocks = vi.hoisted(() => ({
  feedback: vi.fn(),
  requireReason: true,
}));
vi.mock("react-i18next", () => ({
  initReactI18next: { type: "3rdParty", init: () => undefined },
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ chatUnlikeSwitch: mocks.requireReason }) },
}));
vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({ conversationServiceFeedBackChatHistory: mocks.feedback }),
}));
vi.mock("@/modules/chat/store/workflowPanel", () => ({ useWorkflowStore: () => null }));
vi.mock("@/modules/chat/components/WorkflowPanel", () => ({ WorkflowPanel: () => null }));
vi.mock("@/modules/identityAvatar", () => ({ IdentityAvatar: () => null }));
vi.mock("../ArtifactCollectorCard/ArtifactDownloadButton", () => ({ default: () => null }));
vi.mock("../FeedbackModal", () => ({
  default: ({ visible, onSubmit }: any) => visible ? (
    <div role="dialog"><button onClick={() => onSubmit(["incorrect"], "")}>submit reason</button></div>
  ) : null,
}));
vi.mock("@/modules/knowledge/api/translation", () => ({
  getTranslationStatus: vi.fn().mockResolvedValue(false),
}));

function mountFeedback(feedBack?: Feedback) {
  const updateMessage = vi.fn();
  render(<AssistantMessage
    item={{ role: "assistant", history_id: "answer-1", delta: "Answer", run_status: "completed", feed_back: feedBack }}
    index={0} length={1} sendMessage={vi.fn()} regenerate={vi.fn()}
    stopGeneration={vi.fn()} renderText={() => <p>Answer</p>} updateMessage={updateMessage}
  />);
  return updateMessage;
}

describe("assistant feedback cancellation", () => {
  beforeEach(() => {
    mocks.requireReason = true;
    mocks.feedback.mockReset().mockResolvedValue({});
  });

  it("cancels an existing dislike without reopening the required-reason dialog", async () => {
    const updateMessage = mountFeedback(Feedback.FeedBackTypeUnlike);
    fireEvent.click(screen.getByRole("img", { name: "dislike" }));
    await waitFor(() => expect(mocks.feedback).toHaveBeenCalledWith({
      feedBackChatHistoryRequest: { history_id: "answer-1", type: Feedback.FeedBackTypeUnspecified },
    }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(updateMessage).toHaveBeenCalledWith(expect.objectContaining({ feed_back: undefined }));
    // The server prop may lag behind the successful cancellation. Clicking again is a new dislike.
    fireEvent.click(screen.getByRole("img", { name: "dislike" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(mocks.feedback).toHaveBeenCalledTimes(1);
  });

  it("still collects a reason for a new dislike", async () => {
    mountFeedback();
    fireEvent.click(screen.getByRole("img", { name: "dislike" }));
    expect(mocks.feedback).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "submit reason" }));
    await waitFor(() => expect(mocks.feedback).toHaveBeenCalledWith({
      feedBackChatHistoryRequest: { history_id: "answer-1", type: Feedback.FeedBackTypeUnlike, reason: "incorrect", expected_answer: "" },
    }));
  });

  it("can dislike again after cancelling while the server prop still contains the old dislike", async () => {
    mocks.requireReason = false;
    mountFeedback(Feedback.FeedBackTypeUnlike);
    await act(async () => { fireEvent.click(screen.getByRole("img", { name: "dislike" })); });
    await act(async () => { fireEvent.click(screen.getByRole("img", { name: "dislike" })); });
    expect(mocks.feedback.mock.calls.map(([request]) => request.feedBackChatHistoryRequest.type))
      .toEqual([Feedback.FeedBackTypeUnspecified, Feedback.FeedBackTypeUnlike]);
  });

  it("keeps the dislike selected when cancelling fails", async () => {
    mocks.feedback.mockRejectedValueOnce(new Error("offline"));
    mountFeedback(Feedback.FeedBackTypeUnlike);
    await act(async () => { fireEvent.click(screen.getByRole("img", { name: "dislike" })); });
    fireEvent.click(screen.getByRole("img", { name: "dislike" }));
    await waitFor(() => expect(mocks.feedback).toHaveBeenCalledTimes(2));
    expect(mocks.feedback.mock.calls[1][0].feedBackChatHistoryRequest.type).toBe(Feedback.FeedBackTypeUnspecified);
  });
});
