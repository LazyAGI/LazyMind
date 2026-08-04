import { describe, it, expect, vi } from "vitest";
import { fireEvent, screen, renderWithProviders } from "@/test/testUtils";
import DragUpload from "./index";

// `@/components/ui`'s barrel file re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). This component only needs
// RiskTip, so stub the barrel with a minimal implementation.
vi.mock("@/components/ui", () => ({
  RiskTip: () => <span data-testid="risk-tip" />,
}));

function makeFile(name: string, size: number) {
  const file = new File(["x".repeat(size)], name);
  Object.defineProperty(file, "size", { value: size });
  return file;
}

describe("DragUpload", () => {
  it("renders the default drag/select prompt", () => {
    renderWithProviders(<DragUpload value={[]} onChange={vi.fn()} accept={["pdf"]} />);

    expect(screen.getByText("knowledge.dragUploadOr")).toBeInTheDocument();
    expect(screen.getByText("knowledge.selectFileBtn")).toBeInTheDocument();
  });

  it("shows the folder select label when selectDirectory is true", () => {
    renderWithProviders(
      <DragUpload value={[]} onChange={vi.fn()} accept={["pdf"]} selectDirectory />,
    );

    expect(screen.getByText("knowledge.selectFolder")).toBeInTheDocument();
  });

  it("adds a selected file to the list via the hidden file input", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <DragUpload value={[]} onChange={onChange} accept={["pdf"]} />,
    );

    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = makeFile("doc.pdf", 100);

    fireEvent.change(input, { target: { files: [file] } });

    expect(onChange).toHaveBeenCalledTimes(1);
    const newFileList = onChange.mock.calls[0][0];
    expect(newFileList).toHaveLength(1);
    expect(newFileList[0].path).toBe("doc.pdf");
  });

  it("rejects files with an unsupported extension", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <DragUpload value={[]} onChange={onChange} accept={["pdf"]} />,
    );

    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = makeFile("image.png", 100);

    fireEvent.change(input, { target: { files: [file] } });

    expect(onChange).not.toHaveBeenCalled();
  });

  it("rejects files exceeding maxFileSize", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <DragUpload
        value={[]}
        onChange={onChange}
        accept={["pdf"]}
        maxFileSize={10}
      />,
    );

    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = makeFile("big.pdf", 100);

    fireEvent.change(input, { target: { files: [file] } });

    expect(onChange).not.toHaveBeenCalled();
  });

  it("renders existing files in the list and removes one on delete click", () => {
    const onChange = vi.fn();
    const value = [{ uid: "u1", path: "existing.pdf", size: 10 }];
    const { container } = renderWithProviders(
      <DragUpload value={value} onChange={onChange} accept={["pdf"]} />,
    );

    expect(screen.getByText("existing.pdf")).toBeInTheDocument();

    const deleteIcon = container.querySelector(".deleteIcon");
    expect(deleteIcon).toBeTruthy();
    fireEvent.click(deleteIcon!);

    expect(onChange).toHaveBeenCalledWith([]);
  });
});
