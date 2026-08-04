import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import MultiAnswerDisplay, { type Answer } from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const answers: Answer[] = [
  { content: "first answer", index: 0, history_id: "h1" },
  { content: "second answer", index: 1, history_id: "h2" },
];

const renderText = (content: string) => <span>{content}</span>;

describe("MultiAnswerDisplay", () => {
  it("renders nothing when fewer than two answers are provided", () => {
    const { container } = render(
      <MultiAnswerDisplay answers={[answers[0]]} renderText={renderText} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders both answers side by side with a preference selector", () => {
    render(<MultiAnswerDisplay answers={answers} renderText={renderText} />);
    expect(screen.getByText("first answer")).toBeInTheDocument();
    expect(screen.getByText("second answer")).toBeInTheDocument();
    expect(screen.getByText("chat.preferredAnswerVersion")).toBeInTheDocument();
  });

  it("selects the first answer and reports the preference when prefer_first is chosen", () => {
    const onSelectAnswer = vi.fn();
    render(
      <MultiAnswerDisplay
        answers={answers}
        renderText={renderText}
        onSelectAnswer={onSelectAnswer}
      />,
    );

    fireEvent.click(
      screen.getByRole("radio", { name: "chat.lazyMindModel" }),
    );

    expect(onSelectAnswer).toHaveBeenCalledWith(0, "prefer_first");
    expect(screen.getByText("first answer")).toBeInTheDocument();
    expect(screen.queryByText("second answer")).not.toBeInTheDocument();
  });

  it("selects the second answer when prefer_second is chosen", () => {
    const onSelectAnswer = vi.fn();
    render(
      <MultiAnswerDisplay
        answers={answers}
        renderText={renderText}
        onSelectAnswer={onSelectAnswer}
      />,
    );

    fireEvent.click(screen.getByRole("radio", { name: "DeepSeek" }));

    expect(onSelectAnswer).toHaveBeenCalledWith(1, "prefer_second");
  });

  it("defaults to the first answer for similar/neither preferences", () => {
    const onSelectAnswer = vi.fn();
    render(
      <MultiAnswerDisplay
        answers={answers}
        renderText={renderText}
        onSelectAnswer={onSelectAnswer}
      />,
    );

    fireEvent.click(screen.getByRole("radio", { name: "chat.bothGood" }));

    expect(onSelectAnswer).toHaveBeenCalledWith(0, "similar");
  });

  it("hides the preference section when disabled is true", () => {
    render(
      <MultiAnswerDisplay answers={answers} renderText={renderText} disabled />,
    );
    expect(screen.queryByText("chat.preferredAnswerVersion")).not.toBeInTheDocument();
  });

  it("respects an initialSelectedIndex to preselect an answer", () => {
    render(
      <MultiAnswerDisplay
        answers={answers}
        renderText={renderText}
        initialSelectedIndex={1}
        initialPreference="prefer_second"
      />,
    );
    expect(screen.getByText("second answer")).toBeInTheDocument();
    expect(screen.queryByText("first answer")).not.toBeInTheDocument();
  });

  it("renders footer and knowledge base content via the provided render props", () => {
    render(
      <MultiAnswerDisplay
        answers={answers}
        renderText={renderText}
        renderFooter={(index, full) => (
          <div>footer-{index}-{full ? "full" : "compact"}</div>
        )}
        renderKnowledgeBase={(index) => <div>kb-{index}</div>}
      />,
    );
    expect(screen.getByText("footer-0-compact")).toBeInTheDocument();
    expect(screen.getByText("footer-1-compact")).toBeInTheDocument();
    expect(screen.getByText("kb-0")).toBeInTheDocument();
    expect(screen.queryByText("kb-1")).not.toBeInTheDocument();
  });
});
