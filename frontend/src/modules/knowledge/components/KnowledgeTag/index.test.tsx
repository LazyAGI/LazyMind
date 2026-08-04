import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import KnowledgeTag from "./index";

describe("KnowledgeTag", () => {
  it("renders a plain Tag when not checkable", () => {
    render(<KnowledgeTag title="foo" checkable={false} />);
    expect(screen.getByText("foo")).toBeInTheDocument();
  });

  it("renders a CheckableTag reflecting the checked state via the checked callback", () => {
    const checked = vi.fn().mockReturnValue(true);
    render(<KnowledgeTag title="bar" checkable checked={checked} />);
    expect(checked).toHaveBeenCalledWith("bar");
    const tag = screen.getByText("bar").closest(".knowledge-tag");
    expect(tag).toHaveClass("ant-tag-checkable-checked");
  });

  it("defaults to unchecked when no checked callback is provided", () => {
    render(<KnowledgeTag title="baz" checkable />);
    const tag = screen.getByText("baz").closest(".knowledge-tag");
    expect(tag).not.toHaveClass("ant-tag-checkable-checked");
  });

  it("calls onChange with the toggled checked state when clicked", () => {
    const onChange = vi.fn();
    render(
      <KnowledgeTag title="clickable" checkable checked={() => false} onChange={onChange} />,
    );
    fireEvent.click(screen.getByText("clickable"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("does not throw when checkable but onChange is not provided", () => {
    render(<KnowledgeTag title="noop" checkable checked={() => false} />);
    expect(() => fireEvent.click(screen.getByText("noop"))).not.toThrow();
  });
});
