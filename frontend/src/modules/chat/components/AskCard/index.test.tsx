import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import AskCard, { type AskPending } from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const singleQuestionPending: AskPending = {
  ask_id: "ask-1",
  questions: [
    { text: "Continue?", type: "boolean" },
  ],
};

const multiQuestionPending: AskPending = {
  ask_id: "ask-2",
  questions: [
    { text: "Pick one", type: "single", choices: ["A", "B", "其他"] },
    { text: "Describe", type: "text" },
  ],
};

describe("AskCard", () => {
  it("renders the current question text and progress", () => {
    render(<AskCard askPending={singleQuestionPending} onSubmit={vi.fn()} />);
    expect(screen.getByText("Continue?")).toBeInTheDocument();
    expect(screen.getByText("1 / 1")).toBeInTheDocument();
  });

  it("submits a boolean answer with the formatted text and structured payload", () => {
    const onSubmit = vi.fn();
    render(<AskCard askPending={singleQuestionPending} onSubmit={onSubmit} />);

    fireEvent.click(screen.getByText("common.yes"));
    fireEvent.click(screen.getByText("chat.askCardSubmit"));

    expect(onSubmit).toHaveBeenCalledWith({
      text: "Continue?: common.yes",
      structured: {
        ask_id: "ask-1",
        questions: [
          {
            text: "Continue?",
            type: "boolean",
            choices: [],
            custom_choices: [],
            answer: { type: "boolean", value: "common.yes" },
          },
        ],
      },
    });
  });

  it("navigates between multiple questions with the next/prev buttons", () => {
    render(<AskCard askPending={multiQuestionPending} onSubmit={vi.fn()} />);

    expect(screen.getByText("Pick one")).toBeInTheDocument();
    fireEvent.click(screen.getByText("chat.askCardNext"));
    expect(screen.getByText("Describe")).toBeInTheDocument();

    fireEvent.click(screen.getByText("chat.askCardPrev"));
    expect(screen.getByText("Pick one")).toBeInTheDocument();
  });

  it("reveals a free-text input when the 'other' choice is selected", () => {
    render(<AskCard askPending={multiQuestionPending} onSubmit={vi.fn()} />);
    fireEvent.click(screen.getByText("chat.askCardOtherOption"));
    expect(
      screen.getByPlaceholderText("chat.askCardOtherPlaceholder"),
    ).toBeInTheDocument();
  });

  it("calls onAnswerChange whenever an answer is updated", () => {
    const onAnswerChange = vi.fn();
    render(
      <AskCard
        askPending={singleQuestionPending}
        onSubmit={vi.fn()}
        onAnswerChange={onAnswerChange}
      />,
    );
    fireEvent.click(screen.getByText("common.no"));
    expect(onAnswerChange).toHaveBeenCalledWith(0, {
      type: "boolean",
      value: "common.no",
    });
  });

  it("disables all interaction when disabled is true", () => {
    render(<AskCard askPending={singleQuestionPending} onSubmit={vi.fn()} disabled />);
    expect(screen.getByText("common.yes").closest("button")).toBeDisabled();
    expect(screen.queryByText("chat.askCardSubmit")).not.toBeInTheDocument();
  });
});
