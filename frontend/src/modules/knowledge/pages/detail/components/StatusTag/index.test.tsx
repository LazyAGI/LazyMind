import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import StatusTag from "./index";

describe("StatusTag", () => {
  it("renders nothing when statusConfig has no title", () => {
    const { container } = render(<StatusTag />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the status title with the given color", () => {
    render(
      <StatusTag
        statusConfig={{ title: "Running", color: "rgba(0, 128, 0, 1)" }}
      />,
    );

    const tag = screen.getByText("Running");
    expect(tag).toHaveStyle({ color: "rgba(0, 128, 0, 1)" });
  });

  it("uses a custom background when provided instead of deriving from color", () => {
    render(
      <StatusTag
        statusConfig={{
          title: "Failed",
          color: "rgba(255, 0, 0, 1)",
          background: "rgba(255, 0, 0, 0.2)",
        }}
      />,
    );

    expect(screen.getByText("Failed")).toHaveStyle({
      background: "rgba(255, 0, 0, 0.2)",
    });
  });

  it("shows a tooltip icon when tips.show is true", () => {
    const { container } = render(
      <StatusTag
        statusConfig={{ title: "Failed", color: "rgba(255, 0, 0, 1)" }}
        tips={{ show: true, content: "error detail" }}
      />,
    );

    expect(container.querySelector(".anticon-info-circle")).toBeInTheDocument();
  });

  it("does not render a tooltip icon when tips.show is false", () => {
    const { container } = render(
      <StatusTag
        statusConfig={{ title: "Failed", color: "rgba(255, 0, 0, 1)" }}
        tips={{ show: false }}
      />,
    );

    expect(container.querySelector(".anticon-info-circle")).not.toBeInTheDocument();
  });
});
