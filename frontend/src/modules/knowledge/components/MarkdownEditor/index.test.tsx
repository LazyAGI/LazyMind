import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import MarkdownEditor from "./index";

vi.mock("react-markdown-editor-lite", () => ({
  default: ({ value, onChange, readOnly, view }: any) => (
    <div data-testid="md-editor" data-readonly={String(!!readOnly)} data-view={JSON.stringify(view || null)}>
      <textarea
        aria-label="markdown-input"
        value={value}
        onChange={(e) => onChange?.({ text: e.target.value })}
      />
    </div>
  ),
}));

vi.mock("react-markdown-editor-lite/lib/index.css", () => ({}));

vi.mock("markdown-it", () => ({
  default: class MarkdownIt {
    render(text: string) {
      return `<p>${text}</p>`;
    }
  },
}));

describe("MarkdownEditor", () => {
  it("renders the underlying editor with the provided value", () => {
    render(<MarkdownEditor value="hello world" />);
    expect(screen.getByLabelText("markdown-input")).toHaveValue("hello world");
  });

  it("forwards onChange events from the underlying editor", () => {
    const onChange = vi.fn();
    render(<MarkdownEditor value="" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("markdown-input"), {
      target: { value: "new text" },
    });
    expect(onChange).toHaveBeenCalledWith({ text: "new text" });
  });

  it("is editable by default (readOnly false, no restricted view)", () => {
    render(<MarkdownEditor value="" />);
    const editor = screen.getByTestId("md-editor");
    expect(editor.dataset.readonly).toBe("false");
    expect(editor.dataset.view).toBe("null");
  });

  it("switches to a read-only, preview-only view when readOnly is true", () => {
    render(<MarkdownEditor value="content" readOnly />);
    const editor = screen.getByTestId("md-editor");
    expect(editor.dataset.readonly).toBe("true");
    expect(editor.dataset.view).toBe(
      JSON.stringify({ menu: false, md: false, html: true }),
    );
  });
});
