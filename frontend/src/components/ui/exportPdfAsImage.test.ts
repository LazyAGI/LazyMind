import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let addImageMock: ReturnType<typeof vi.fn>;
let addPageMock: ReturnType<typeof vi.fn>;
let saveMock: ReturnType<typeof vi.fn>;
let jsPDFMock: ReturnType<typeof vi.fn>;

vi.mock("jspdf", async (importOriginal) => {
  addImageMock = vi.fn();
  addPageMock = vi.fn();
  saveMock = vi.fn();
  jsPDFMock = vi.fn(function PDFClass() {
    return {
      addImage: addImageMock,
      addPage: addPageMock,
      save: saveMock,
    };
  });
  return { jsPDF: jsPDFMock };
});

const getPageMock = vi.fn();
const destroyMock = vi.fn();
const getDocumentMock = vi.fn();

vi.mock("react-pdf", () => ({
  pdfjs: {
    GlobalWorkerOptions: {},
    version: "1.0.0",
    getDocument: (...args: unknown[]) => ({ promise: getDocumentMock(...args) }),
  },
}));

describe("exportPdfAsImagePdf", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getDocumentMock.mockResolvedValue({
      numPages: 2,
      getPage: getPageMock,
      destroy: destroyMock,
    });
    getPageMock.mockResolvedValue({
      getViewport: () => ({ width: 100, height: 200 }),
      render: () => ({ promise: Promise.resolve() }),
    });

    const fakeContext = {} as CanvasRenderingContext2D;
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(fakeContext);
    vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue("data:image/jpeg;base64,AAA");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders each page into the pdf and saves with a default name", async () => {
    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    await exportPdfAsImagePdf("https://example.com/file.pdf");

    expect(getPageMock).toHaveBeenCalledTimes(2);
    expect(jsPDFMock).toHaveBeenCalledTimes(1);
    expect(addPageMock).toHaveBeenCalledTimes(1);
    expect(addImageMock).toHaveBeenCalledTimes(2);
    expect(saveMock).toHaveBeenCalledWith("document-images.pdf");
    expect(destroyMock).toHaveBeenCalledTimes(1);
  });

  it("strips a trailing .pdf extension and appends -images.pdf", async () => {
    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    await exportPdfAsImagePdf("https://example.com/file.pdf", "report.PDF");
    expect(saveMock).toHaveBeenCalledWith("report-images.pdf");
  });

  it("accepts an options object with a custom fileName", async () => {
    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    await exportPdfAsImagePdf("https://example.com/file.pdf", { fileName: "custom" });
    expect(saveMock).toHaveBeenCalledWith("custom-images.pdf");
  });

  it("passes a File's array buffer through to pdf.js", async () => {
    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    const file = new File([new Uint8Array([1, 2, 3])], "doc.pdf", {
      type: "application/pdf",
    });
    await exportPdfAsImagePdf(file);
    expect(getDocumentMock).toHaveBeenCalledWith(
      expect.objectContaining({ data: expect.any(ArrayBuffer) }),
    );
  });

  it("throws when the pdf has no pages", async () => {
    getDocumentMock.mockResolvedValue({
      numPages: 0,
      getPage: getPageMock,
      destroy: destroyMock,
    });
    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    await expect(exportPdfAsImagePdf("https://example.com/file.pdf")).rejects.toThrow(
      "PDF has no pages",
    );
    expect(destroyMock).toHaveBeenCalledTimes(1);
  });

  it("throws when an ArrayBuffer is detached and no fetchUrl fallback is provided", async () => {
    const buffer = new ArrayBuffer(8);
    // Detach the buffer by transferring it away.
    new MessageChannel().port1.postMessage(buffer, [buffer]);

    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    await expect(exportPdfAsImagePdf(buffer)).rejects.toThrow(
      "PDF data is no longer available, please reload the page",
    );
  });

  it("re-fetches via fetchUrl when the buffer is detached", async () => {
    const buffer = new ArrayBuffer(8);
    new MessageChannel().port1.postMessage(buffer, [buffer]);

    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        arrayBuffer: async () => new ArrayBuffer(4),
      } as Response),
    );

    const { exportPdfAsImagePdf } = await import("./exportPdfAsImage");
    await exportPdfAsImagePdf(buffer, { fetchUrl: "https://example.com/refetch.pdf" });
    expect(fetch).toHaveBeenCalledWith(
      "https://example.com/refetch.pdf",
      expect.objectContaining({ headers: undefined }),
    );
    expect(saveMock).toHaveBeenCalled();
  });
});
