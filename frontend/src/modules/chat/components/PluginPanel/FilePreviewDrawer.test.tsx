import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { FilePreviewDrawer } from "./FilePreviewDrawer";

const mockResolveCoreAssetUrl = vi.fn((url: string) => `resolved:${url}`);

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key),
  }),
}));

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveCoreAssetUrl: (url: string) => mockResolveCoreAssetUrl(url),
}));

// FileViewer re-exports @/components/ui, which also re-exports RenderPdf backed by
// react-pdf/pdfjs-dist; that library needs browser canvas APIs (DOMMatrix) that jsdom
// does not implement, so stub the heavy viewer away.
vi.mock("@/modules/knowledge/components/FileViewer", () => ({
  default: ({ file, fileName }: { file: string; fileName: string }) => (
    <div data-testid="file-viewer">{fileName}:{file}</div>
  ),
}));

describe("FilePreviewDrawer", () => {
  it("renders nothing when closed", () => {
    render(
      <FilePreviewDrawer open={false} filename="report.pdf" url="/files/report.pdf" onClose={vi.fn()} />,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("renders a loading state before the URL resolves, then shows the file viewer", async () => {
    render(
      <FilePreviewDrawer open={true} filename="report.pdf" url="/files/report.pdf" onClose={vi.fn()} />,
    );
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByTestId("file-viewer")).toHaveTextContent(
        "report.pdf:resolved:/files/report.pdf",
      ),
    );
  });

  it("shows a loading state and does not resolve when the URL is empty", () => {
    render(<FilePreviewDrawer open={true} filename="untitled" url="" onClose={vi.fn()} />);
    expect(screen.getByText("common.loading")).toBeInTheDocument();
  });

  it("calls onClose when the close button is clicked", async () => {
    const onClose = vi.fn();
    render(
      <FilePreviewDrawer open={true} filename="report.pdf" url="/files/report.pdf" onClose={onClose} />,
    );
    fireEvent.click(screen.getByLabelText("chat.closePreview"));
    expect(onClose).toHaveBeenCalled();
  });

  it("closes when clicking the overlay backdrop but not when clicking inside the drawer", () => {
    const onClose = vi.fn();
    render(
      <FilePreviewDrawer open={true} filename="report.pdf" url="/files/report.pdf" onClose={onClose} />,
    );
    fireEvent.click(screen.getByRole("dialog"));
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("presentation"));
    expect(onClose).toHaveBeenCalled();
  });
});
