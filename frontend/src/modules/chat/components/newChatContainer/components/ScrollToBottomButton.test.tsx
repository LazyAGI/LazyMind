import { describe, expect, it, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import ScrollToBottomButton from "./ScrollToBottomButton";

describe("ScrollToBottomButton", () => {
  it("applies the hidden class when not visible", () => {
    const { container } = render(
      <ScrollToBottomButton visible={false} inputHeight={80} onClick={vi.fn()} />,
    );
    expect(container.querySelector(".toBottomContainer")).toHaveClass("hidden");
  });

  it("does not apply the hidden class when visible", () => {
    const { container } = render(
      <ScrollToBottomButton visible inputHeight={80} onClick={vi.fn()} />,
    );
    expect(container.querySelector(".toBottomContainer")).not.toHaveClass("hidden");
  });

  it("positions the container using the given inputHeight", () => {
    const { container } = render(
      <ScrollToBottomButton visible inputHeight={120} onClick={vi.fn()} />,
    );
    expect(container.querySelector(".toBottomContainer")).toHaveStyle({ bottom: "120px" });
  });

  it("calls onClick when clicked", () => {
    const onClick = vi.fn();
    const { container } = render(
      <ScrollToBottomButton visible inputHeight={0} onClick={onClick} />,
    );
    fireEvent.click(container.querySelector(".toBottom")!);
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
