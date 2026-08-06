import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { WriterIRControl, type WriterIRSaveResult } from "./WriterIRControl";
import type { WriterDocument } from "./writerIR";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key),
  }),
}));

vi.mock("../MarkdownViewer/syntaxHighlight", () => ({
  highlightCode: () => "",
}));

const mockOnChange = vi.fn();

vi.mock("./WriterIRDocumentEditor", () => ({
  WriterIRDocumentEditor: ({ document, onChange }: { document: WriterDocument; onChange: (d: WriterDocument) => void }) => {
    mockOnChange.mockImplementation(onChange);
    return (
      <div data-testid="writer-ir-document-editor">
        <button
          data-testid="simulate-edit"
          onClick={() => onChange({ ...document, title: "Edited Title" })}
        >
          edit
        </button>
      </div>
    );
  },
}));

function makeDoc(overrides: Partial<WriterDocument> = {}): WriterDocument {
  return {
    document_id: "doc-1",
    stage: "draft",
    title: "Untitled",
    blocks: [
      { node_id: "p1", type: "paragraph", content: "hello", spans: [{ text: "hello", style: {} }], editable: true },
    ],
    ...overrides,
  };
}

describe("WriterIRControl", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders the editable document editor when onSave is provided and blocks are editable", () => {
    render(<WriterIRControl document={makeDoc()} onSave={vi.fn()} />);
    expect(screen.getByTestId("writer-ir-document-editor")).toBeInTheDocument();
  });

  it("renders a read-only preview (with heading/paragraph rendering) when onSave is absent", () => {
    const doc = makeDoc({
      blocks: [{ node_id: "h1", type: "heading", content: "Title", spans: [{ text: "Title", style: {} }], numbering: { level: 2 } }],
    });
    render(<WriterIRControl document={doc} />);
    expect(screen.queryByTestId("writer-ir-document-editor")).not.toBeInTheDocument();
    expect(screen.getByText("Title").tagName).toBe("H2");
  });

  it("shows an empty-document status message when there are no blocks and it is read-only", () => {
    render(<WriterIRControl document={makeDoc({ blocks: [] })} />);
    expect(screen.getByText("chat.writerIR.emptyDocument")).toBeInTheDocument();
  });

  it("auto-saves as a draft after the idle debounce once the document becomes dirty", async () => {
    vi.useRealTimers();
    const onSave = vi.fn().mockResolvedValue(undefined as WriterIRSaveResult | undefined);
    render(<WriterIRControl document={makeDoc()} onSave={onSave} />);

    fireEvent.click(screen.getByTestId("simulate-edit"));

    await waitFor(
      () => expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Untitled" }),
        expect.objectContaining({ title: "Edited Title" }),
        undefined,
        "draft",
      ),
      { timeout: 5000 },
    );
  }, 10000);

  it("surfaces a save error notice with retry/discard actions when onSave rejects", async () => {
    vi.useRealTimers();
    const onSave = vi.fn().mockRejectedValue(new Error("network down"));
    render(<WriterIRControl document={makeDoc()} onSave={onSave} />);

    fireEvent.click(screen.getByTestId("simulate-edit"));

    await waitFor(
      () => expect(screen.getByText(/chat.writerIR.saveError/)).toBeInTheDocument(),
      { timeout: 5000 },
    );
    expect(screen.getByText("common.retry")).toBeInTheDocument();
    expect(screen.getByText("chat.writerIR.discard")).toBeInTheDocument();
  }, 10000);
});
