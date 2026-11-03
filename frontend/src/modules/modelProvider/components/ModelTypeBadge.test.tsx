import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ModelTypeBadge, ModelTypeIcon } from "./ModelTypeBadge";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

describe("model type badge", () => {
  it("keeps the icon decorative and reserves its requested square size", () => {
    const { container } = render(<ModelTypeIcon type="rerank" size={20} />);
    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      "/model-type-icons/rerank.svg",
    );
    expect(container.querySelector("img")).toHaveAttribute("alt", "");
    expect(container.querySelector("img")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    expect(container.querySelector("img")).toHaveAttribute("width", "20");
    expect(container.querySelector("img")).toHaveAttribute("height", "20");
  });

  it("retains a readable task name in compact rows and explains the task on hover", async () => {
    const { container } = render(<ModelTypeBadge type="embedding" compact />);
    expect(screen.getByText("modelProvider.taskType.embedding")).toBeVisible();
    expect(container.firstChild).toHaveClass("model-type-badge", "is-compact");
    expect(container.firstChild).toHaveAttribute(
      "data-model-type",
      "embedding",
    );
    fireEvent.mouseOver(screen.getByText("modelProvider.taskType.embedding"));
    expect(
      await screen.findByText("modelProvider.taskTypeDescription.embedding"),
    ).toBeInTheDocument();
  });

  it("shows unknown types explicitly instead of presenting them as chat", () => {
    const { container } = render(<ModelTypeBadge type="unknown" />);
    expect(screen.getByText("modelProvider.taskType.unknown")).toBeVisible();
    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      "/model-type-icons/unknown.svg",
    );
    expect(
      screen.queryByText("modelProvider.taskType.chat"),
    ).not.toBeInTheDocument();
  });
});
