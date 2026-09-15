import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import AskCard from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

describe("AskCard read-only history", () => {
  it("keeps answers immutable while allowing previous, next, and direct page navigation", () => {
    const onSubmit = vi.fn();
    const onAnswerChange = vi.fn();
    render(
      <AskCard
        askPending={{
          ask_id: "ask-history",
          questions: [
            { text: "第一题", type: "single", choices: ["答案 A", "答案 B"] },
            { text: "第二题", type: "text" },
          ],
        }}
        disabled
        savedAnswers={{
          0: { type: "single", value: "答案 A", otherText: "" },
          1: { type: "text", value: "已保存答案" },
        }}
        onSubmit={onSubmit}
        onAnswerChange={onAnswerChange}
      />,
    );

    expect(screen.getByRole("radio", { name: "答案 A" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /chat\.askCardNext/ }));
    expect(screen.getByText("第二题")).toBeInTheDocument();
    expect(screen.getByDisplayValue("已保存答案")).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Go to question 1" }));
    expect(screen.getByText("第一题")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Go to question 2" }));
    fireEvent.click(screen.getByRole("button", { name: /chat\.askCardPrev/ }));
    expect(screen.getByText("第一题")).toBeInTheDocument();
    expect(onAnswerChange).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
