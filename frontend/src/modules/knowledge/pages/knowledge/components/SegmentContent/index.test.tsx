import { describe, it, expect, vi } from "vitest";
import { fireEvent, waitFor, screen, render } from "@testing-library/react";
import SegmentContent from "./index";

const editSegmentMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  SegmentServiceApi: () => ({
    segmentServiceEditSegment: (...args: unknown[]) => editSegmentMock(...args),
  }),
}));

vi.mock("@/modules/knowledge/components/MarkdownViewer", () => ({
  __esModule: true,
  default: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="markdown-viewer">{children}</div>
  ),
}));

vi.mock("@/modules/knowledge/components/mdxeditor", () => ({
  __esModule: true,
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (v: string) => void;
  }) => (
    <textarea
      data-testid="mdx-editor"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));

describe("SegmentContent", () => {
  const segment = {
    dataset_id: "ds-1",
    document_id: "doc-1",
    segment_id: "seg-1",
    content: "Hello **world**",
    image_keys: [],
  };

  it("renders read-only content via MarkdownViewer when not editable", () => {
    render(<SegmentContent segment={segment as any} group="block" editable={false} />);

    expect(screen.getByTestId("markdown-viewer")).toHaveTextContent(
      "Hello **world**",
    );
    expect(screen.queryByTestId("mdx-editor")).not.toBeInTheDocument();
  });

  it("renders the editable MdxEditor when editable is true", () => {
    render(<SegmentContent segment={segment as any} group="block" editable />);

    expect(screen.getByTestId("mdx-editor")).toBeInTheDocument();
    expect(screen.queryByTestId("markdown-viewer")).not.toBeInTheDocument();
  });

  it("debounces edits and calls the API with the updated content", async () => {
    vi.useFakeTimers();
    render(<SegmentContent segment={segment as any} group="block" editable />);

    const editor = screen.getByTestId("mdx-editor");
    fireEvent.change(editor, { target: { value: "Updated content" } });

    vi.advanceTimersByTime(500);
    vi.useRealTimers();

    await waitFor(() => {
      expect(editSegmentMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        document: "doc-1",
        segment: "seg-1",
        editSegmentRequest: { name: "", group: "block", content: "Updated content" },
      });
    });
  });
});
