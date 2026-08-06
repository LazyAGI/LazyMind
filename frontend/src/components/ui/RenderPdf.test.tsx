import { describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import RenderPdf from "./RenderPdf";

vi.mock("react-pdf/dist/Page/AnnotationLayer.css", () => ({}));
vi.mock("react-pdf/dist/Page/TextLayer.css", () => ({}));

let lastDocumentProps: Record<string, unknown> = {};

vi.mock("react-pdf", () => ({
  pdfjs: { GlobalWorkerOptions: {}, version: "test-version" },
  Document: (props: Record<string, unknown>) => {
    lastDocumentProps = props;
    return (
      <div data-testid="pdf-document">
        {props.loading as React.ReactNode}
        {props.children as React.ReactNode}
      </div>
    );
  },
  Page: (props: Record<string, unknown>) => (
    <div data-testid={`pdf-page-${props.pageNumber}`} />
  ),
}));

describe("RenderPdf", () => {
  it("renders a loading placeholder before the document has loaded", () => {
    render(<RenderPdf fileData={null} loadingText="Loading PDF..." />);
    expect(screen.getByText("Loading PDF...")).toBeTruthy();
  });

  it("renders pages once the document reports onLoadSuccess", async () => {
    render(<RenderPdf fileData="data:application/pdf;base64,AAAA" />);

    const onLoadSuccess = lastDocumentProps.onLoadSuccess as (doc: unknown) => void;
    await act(async () => {
      onLoadSuccess({
        numPages: 2,
        getPage: () => Promise.resolve({ view: [0, 0, 595, 842] }),
      });
    });

    expect(screen.getByTestId("pdf-page-1")).toBeTruthy();
  });

  it("shows an error message when the document fails to load", () => {
    render(<RenderPdf fileData="broken" />);

    const onLoadError = lastDocumentProps.onLoadError as (err: Error) => void;
    act(() => {
      onLoadError(new Error("Corrupted PDF"));
    });

    expect(screen.getByText(/加载失败/)).toBeTruthy();
    expect(screen.getByText(/Corrupted PDF/)).toBeTruthy();
  });
});
