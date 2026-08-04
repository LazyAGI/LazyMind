import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ToolbarComponent from "./toolbar";

vi.mock("@mdxeditor/editor", () => ({
  UndoRedo: () => <div data-testid="undo-redo" />,
  BoldItalicUnderlineToggles: () => <div data-testid="bold-italic-underline" />,
  BlockTypeSelect: () => <div data-testid="block-type-select" />,
  InsertTable: () => <div data-testid="insert-table" />,
}));

describe("mdxeditor ToolbarComponent", () => {
  it("renders all toolbar controls inside the toolbar wrapper", () => {
    const { container } = render(<ToolbarComponent />);

    expect(container.querySelector(".mdx-editor-toolbar")).toBeInTheDocument();
    expect(screen.getByTestId("undo-redo")).toBeInTheDocument();
    expect(screen.getByTestId("bold-italic-underline")).toBeInTheDocument();
    expect(screen.getByTestId("block-type-select")).toBeInTheDocument();
    expect(screen.getByTestId("insert-table")).toBeInTheDocument();
  });
});
