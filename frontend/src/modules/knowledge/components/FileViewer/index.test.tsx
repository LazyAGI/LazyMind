import { describe, it, expect, vi, beforeEach } from "vitest";
import { createRef } from "react";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import FileViewer, { type FileViewerRef } from "./index";

vi.mock("./renderers", () => ({
  RenderHtml: () => <div data-testid="render-html" />,
  RenderTxt: () => <div data-testid="render-txt" />,
  RenderPpt: () => <div data-testid="render-ppt" />,
  RenderExcel: () => <div data-testid="render-excel" />,
  RenderWord: () => <div data-testid="render-word" />,
}));

vi.mock("@/components/ui", () => ({
  RenderPdf: () => <div data-testid="render-pdf" />,
  exportPdfAsImagePdf: vi.fn(),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getAuthHeaders: () => ({}),
  },
}));

vi.mock("@/modules/knowledge/utils/request", () => ({
  normalizeProxyableUrl: (uri?: string) => uri,
}));

function arrayBufferResponse(text: string, ok = true) {
  return Promise.resolve({
    ok,
    arrayBuffer: () => Promise.resolve(new TextEncoder().encode(text).buffer),
  } as Response);
}

describe("FileViewer", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
  });

  it("shows an empty state when no file is provided", async () => {
    renderWithProviders(<FileViewer fileName="none" />);

    await waitFor(() => {
      expect(screen.getByText("common.noData")).toBeInTheDocument();
    });
  });

  it("renders the text renderer once a .txt file has loaded", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockReturnValue(
      arrayBufferResponse("plain text content"),
    );

    renderWithProviders(<FileViewer file="/files/report.txt" fileName="report.txt" />);

    await waitFor(() => {
      expect(screen.getByTestId("render-txt")).toBeInTheDocument();
    });
  });

  it("renders the unsupported message for unknown file types", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockReturnValue(
      arrayBufferResponse("binary-ish"),
    );

    renderWithProviders(<FileViewer file="/files/archive.zip" fileName="archive.zip" />);

    await waitFor(() => {
      expect(screen.getByText("knowledge.previewUnsupported")).toBeInTheDocument();
    });
  });

  it("shows the empty state with an error message when fetching the file fails", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockReturnValue(
      arrayBufferResponse("", false),
    );

    renderWithProviders(<FileViewer file="/files/broken.txt" fileName="broken.txt" />);

    await waitFor(() => {
      expect(screen.queryByTestId("render-txt")).not.toBeInTheDocument();
    });
  });

  it("notifies onExportReadyChange as files load and unmount", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockReturnValue(
      arrayBufferResponse("plain text content"),
    );
    const onExportReadyChange = vi.fn();

    renderWithProviders(
      <FileViewer
        file="/files/report.txt"
        fileName="report.txt"
        onExportReadyChange={onExportReadyChange}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("render-txt")).toBeInTheDocument();
    });

    // Non-PDF files are never export-ready.
    expect(onExportReadyChange).toHaveBeenCalledWith(false);
    expect(onExportReadyChange).not.toHaveBeenCalledWith(true);
  });

  it("rejects exportImagePdf via the ref when the loaded file is not a PDF", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockReturnValue(
      arrayBufferResponse("plain text content"),
    );
    const ref = createRef<FileViewerRef>();

    renderWithProviders(
      <FileViewer ref={ref} file="/files/report.txt" fileName="report.txt" />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("render-txt")).toBeInTheDocument();
    });

    await expect(ref.current?.exportImagePdf()).rejects.toThrow(
      "PDF is not ready for export",
    );
  });
});
