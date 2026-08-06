import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { WriterIRDocumentEditor } from "./WriterIRDocumentEditor";
import type { WriterDocument } from "./writerIR";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

function makeDoc(overrides: Partial<WriterDocument> = {}): WriterDocument {
  return {
    document_id: "doc-1",
    stage: "draft",
    title: "My Document",
    blocks: [
      {
        node_id: "p1",
        type: "paragraph",
        content: "hello world",
        spans: [{ text: "hello world", style: {} }],
        editable: true,
      },
    ],
    ...overrides,
  };
}

function renderEditor(props: Partial<Parameters<typeof WriterIRDocumentEditor>[0]> = {}) {
  const defaultProps = {
    document: makeDoc(),
    ariaLabel: "writer document",
    onChange: vi.fn(),
    onFocus: vi.fn(),
    onBlur: vi.fn(),
  };
  return render(<WriterIRDocumentEditor {...defaultProps} {...props} />);
}

describe("WriterIRDocumentEditor", () => {
  it("renders the document title and paragraph content into the contentEditable area", () => {
    renderEditor();
    const editor = screen.getByRole("textbox", { name: "writer document" });
    expect(editor.querySelector("[data-writer-document-title]")).toHaveTextContent("My Document");
    expect(editor.querySelector("[data-writer-block-content]")).toHaveTextContent("hello world");
  });

  it("marks the editor as non-editable and title as non-editable when disabled", () => {
    renderEditor({ disabled: true });
    const editor = screen.getByRole("textbox", { name: "writer document" });
    expect(editor).toHaveAttribute("contenteditable", "false");
  });

  it("calls onFocus when the editor receives focus", () => {
    const onFocus = vi.fn();
    renderEditor({ onFocus });
    const editor = screen.getByRole("textbox", { name: "writer document" });
    fireEvent.focus(editor);
    expect(onFocus).toHaveBeenCalled();
  });

  it("calls onBlur when focus leaves the editor shell entirely", () => {
    const onBlur = vi.fn();
    renderEditor({ onBlur });
    const editor = screen.getByRole("textbox", { name: "writer document" });
    fireEvent.blur(editor, { relatedTarget: null });
    expect(onBlur).toHaveBeenCalled();
  });

  it("renders a foldable heading with a collapse/expand toggle and toggles collapsed state on click", () => {
    renderEditor({
      document: makeDoc({
        blocks: [
          {
            node_id: "h1",
            type: "heading",
            content: "Section",
            spans: [{ text: "Section", style: {} }],
            numbering: { level: 2 },
          },
          {
            node_id: "p2",
            type: "paragraph",
            content: "under the heading",
            spans: [{ text: "under the heading", style: {} }],
          },
        ],
      }),
    });
    const editor = screen.getByRole("textbox", { name: "writer document" });
    const toggle = editor.querySelector<HTMLButtonElement>("[data-writer-fold-toggle]");
    expect(toggle).toBeTruthy();
    expect(toggle).toHaveAttribute("data-fold-collapsed", "false");

    fireEvent.mouseDown(toggle!);

    expect(editor.querySelector("[data-writer-fold-toggle]")).toHaveAttribute(
      "data-fold-collapsed",
      "true",
    );
  });

  it("renders a code block with a language selector and toolbar actions", () => {
    renderEditor({
      document: makeDoc({
        blocks: [
          {
            node_id: "c1",
            type: "code",
            content: "print(1)",
            language: "python",
          },
        ],
      }),
    });
    const editor = screen.getByRole("textbox", { name: "writer document" });
    expect(editor.querySelector('[data-writer-code-language]')).toBeTruthy();
    expect(editor.querySelector('[data-writer-code-action="copy"]')).toBeTruthy();
    expect(editor.querySelector('[data-writer-code-action="wrap"]')).toBeTruthy();
  });
});
