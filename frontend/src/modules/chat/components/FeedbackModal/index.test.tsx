import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { message } from "antd";
import { afterEach, describe, expect, it, vi } from "vitest";

import FeedbackModal from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("FeedbackModal", () => {
  it("selects Other and submits when the user enters a custom reason", () => {
    const onSubmit = vi.fn();
    const errorSpy = vi
      .spyOn(message, "error")
      .mockImplementation(() => undefined as never);

    render(
      <FeedbackModal
        visible
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />,
    );

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "The response missed an important constraint" },
    });

    expect(
      screen.getByRole("button", { name: "chatFeedback.other" }),
    ).toHaveClass("ant-btn-primary");

    fireEvent.click(
      screen.getByRole("button", { name: "chat.submitFeedback" }),
    );

    expect(errorSpy).not.toHaveBeenCalled();
    expect(onSubmit).toHaveBeenCalledWith(
      ["chatFeedback.other"],
      "The response missed an important constraint",
    );
  });

  it("still rejects a whitespace-only custom reason without a selection", () => {
    const onSubmit = vi.fn();
    const errorSpy = vi
      .spyOn(message, "error")
      .mockImplementation(() => undefined as never);

    render(
      <FeedbackModal
        visible
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />,
    );

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "   " },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "chat.submitFeedback" }),
    );

    expect(onSubmit).not.toHaveBeenCalled();
    expect(errorSpy).toHaveBeenCalledWith(
      "chat.atLeastOneUnsatisfiedReason",
    );
  });
});
