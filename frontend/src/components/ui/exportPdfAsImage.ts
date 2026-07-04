import { jsPDF } from "jspdf";
import { pdfjs } from "react-pdf";

if (!pdfjs.GlobalWorkerOptions.workerSrc) {
  pdfjs.GlobalWorkerOptions.workerSrc = `https://unpkg.com/pdfjs-dist@${pdfjs.version}/build/pdf.worker.min.mjs`;
}

const EXPORT_SCALE = 2;
const EXPORT_JPEG_QUALITY = 0.85;

export interface ExportPdfAsImageOptions {
  fileName?: string;
  /** Re-fetch source when the in-memory buffer was transferred/detached by pdf.js. */
  fetchUrl?: string;
  fetchHeaders?: HeadersInit;
}

function buildExportFileName(fileName?: string): string {
  const base = (fileName || "document").replace(/\.pdf$/i, "");
  return `${base}-images.pdf`;
}

function isDetachedArrayBuffer(buffer: ArrayBuffer): boolean {
  try {
    // Detached buffers throw when sliced or when their bytes are accessed.
    buffer.slice(0, 0);
    return false;
  } catch {
    return true;
  }
}

function copyArrayBuffer(buffer: ArrayBuffer): ArrayBuffer {
  const copy = new ArrayBuffer(buffer.byteLength);
  new Uint8Array(copy).set(new Uint8Array(buffer));
  return copy;
}

async function fetchPdfBuffer(
  url: string,
  headers?: HeadersInit,
): Promise<ArrayBuffer> {
  const response = await fetch(url, { headers });
  if (!response.ok) {
    throw new Error(`Network response was not ok: ${response.status}`);
  }
  return response.arrayBuffer();
}

async function resolvePdfSource(
  fileData: string | ArrayBuffer | File,
  options?: ExportPdfAsImageOptions,
): Promise<{ data: ArrayBuffer } | string> {
  if (typeof fileData === "string") {
    return fileData;
  }
  if (fileData instanceof File) {
    return { data: await fileData.arrayBuffer() };
  }

  if (!isDetachedArrayBuffer(fileData)) {
    // Always copy: pdf.js may transfer/detach the buffer it receives.
    return { data: copyArrayBuffer(fileData) };
  }

  if (options?.fetchUrl) {
    return {
      data: await fetchPdfBuffer(options.fetchUrl, options.fetchHeaders),
    };
  }

  throw new Error("PDF data is no longer available, please reload the page");
}

export async function exportPdfAsImagePdf(
  fileData: string | ArrayBuffer | File,
  fileNameOrOptions?: string | ExportPdfAsImageOptions,
): Promise<void> {
  const options: ExportPdfAsImageOptions =
    typeof fileNameOrOptions === "string"
      ? { fileName: fileNameOrOptions }
      : fileNameOrOptions || {};
  const source = await resolvePdfSource(fileData, options);
  const pdfDoc = await pdfjs.getDocument(source).promise;
  try {
    let pdf: jsPDF | null = null;

    for (let pageNum = 1; pageNum <= pdfDoc.numPages; pageNum++) {
      const page = await pdfDoc.getPage(pageNum);
      const viewport = page.getViewport({ scale: EXPORT_SCALE });
      const canvas = document.createElement("canvas");
      canvas.width = Math.ceil(viewport.width);
      canvas.height = Math.ceil(viewport.height);
      const context = canvas.getContext("2d");
      if (!context) {
        throw new Error("Failed to create canvas context");
      }
      await page.render({ canvasContext: context, viewport, canvas }).promise;
      const imgData = canvas.toDataURL("image/jpeg", EXPORT_JPEG_QUALITY);
      const pageWidthPt = viewport.width / EXPORT_SCALE;
      const pageHeightPt = viewport.height / EXPORT_SCALE;
      const orientation = pageWidthPt > pageHeightPt ? "landscape" : "portrait";

      if (!pdf) {
        pdf = new jsPDF({
          orientation,
          unit: "pt",
          format: [pageWidthPt, pageHeightPt],
        });
      } else {
        pdf.addPage([pageWidthPt, pageHeightPt], orientation);
      }
      pdf.addImage(imgData, "JPEG", 0, 0, pageWidthPt, pageHeightPt);
      canvas.width = 0;
      canvas.height = 0;
    }

    if (!pdf) {
      throw new Error("PDF has no pages");
    }
    pdf.save(buildExportFileName(options.fileName));
  } finally {
    await pdfDoc.destroy();
  }
}
