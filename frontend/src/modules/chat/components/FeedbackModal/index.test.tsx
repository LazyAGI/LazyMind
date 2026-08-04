import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, fireEvent } from "@/test/testUtils";
import FeedbackModal from "./index";

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: { ...actual.message, error: vi.fn() },
  };
});

describe("FeedbackModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders nothing observable when not visible", () => {
    renderWithProviders(
      <FeedbackModal visible={false} onCancel={vi.fn()} onSubmit={vi.fn()} />,
    );
    expect(screen.queryByText("chat.feedbackAskUnsatisfied")).not.toBeInTheDocument();
  });

  it("renders feedback options and comment box when visible", () => {
    renderWithProviders(
      <FeedbackModal visible onCancel={vi.fn()} onSubmit={vi.fn()} />,
    );
    expect(screen.getByText("chatFeedback.didNotUnderstand")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("chat.expectedAnswer")).toBeInTheDocument();
  });

  it("shows an error message and does not submit when no reason is selected", async () => {
    const antd = await import("antd");
    const onSubmit = vi.fn();
    renderWithProviders(
      <FeedbackModal visible onCancel={vi.fn()} onSubmit={onSubmit} />,
    );
    fireEvent.click(screen.getByText("chat.submitFeedback"));
    expect(antd.message.error).toHaveBeenCalledWith("chat.atLeastOneUnsatisfiedReason");
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("toggles a reason and submits selected reasons plus the comment", () => {
    const onSubmit = vi.fn();
    renderWithProviders(
      <FeedbackModal visible onCancel={vi.fn()} onSubmit={onSubmit} />,
    );
    fireEvent.click(screen.getByText("chatFeedback.didNotUnderstand"));
    fireEvent.change(screen.getByPlaceholderText("chat.expectedAnswer"), {
      target: { value: "please add more detail" },
    });
    fireEvent.click(screen.getByText("chat.submitFeedback"));
    expect(onSubmit).toHaveBeenCalledWith(
      ["chatFeedback.didNotUnderstand"],
      "please add more detail",
    );
  });

  it("deselects a reason when clicked twice", () => {
    const onSubmit = vi.fn();
    renderWithProviders(
      <FeedbackModal visible onCancel={vi.fn()} onSubmit={onSubmit} />,
    );
    const reasonBtn = screen.getByText("chatFeedback.didNotUnderstand");
    fireEvent.click(reasonBtn);
    fireEvent.click(reasonBtn);
    fireEvent.click(screen.getByText("chat.submitFeedback"));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("does not submit while submitLoading is true", () => {
    const onSubmit = vi.fn();
    renderWithProviders(
      <FeedbackModal visible onCancel={vi.fn()} onSubmit={onSubmit} submitLoading />,
    );
    fireEvent.click(screen.getByText("chatFeedback.didNotUnderstand"));
    fireEvent.click(screen.getByText("chat.submitFeedback"));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("resets selections and calls onCancel when cancel is clicked", () => {
    const onCancel = vi.fn();
    renderWithProviders(
      <FeedbackModal visible onCancel={onCancel} onSubmit={vi.fn()} />,
    );
    fireEvent.click(screen.getByText("chatFeedback.didNotUnderstand"));
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onCancel).toHaveBeenCalled();
  });
});
