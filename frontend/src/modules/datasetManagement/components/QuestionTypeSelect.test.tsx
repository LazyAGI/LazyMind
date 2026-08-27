// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import QuestionTypeSelect from "./QuestionTypeSelect";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) =>
      ({
        "datasetManagement.questionTypes.fact": "事实问答",
        "datasetManagement.detail.placeholders.questionType": "请选择问题类型",
      })[key] || key,
  }),
}));

describe("QuestionTypeSelect", () => {
  it("focuses when inline editing starts", () => {
    render(<QuestionTypeSelect autoFocus />);

    expect(document.activeElement).toBe(screen.getByRole("combobox"));
  });
});
