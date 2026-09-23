import { jsPDF } from "jspdf";
import { pdfjs } from "react-pdf";
import type { PDFPageProxy } from "pdfjs-dist";
import { isRasterLayoutBlock, validatePdfLayoutBlocks, type PdfLayoutBlock } from "../api/pdfArtifacts";

export interface PdfRenderProgress { page: number; pages: number; progress: number }

function copyBuffer(data: ArrayBuffer): ArrayBuffer {
  const copy = new ArrayBuffer(data.byteLength);
  new Uint8Array(copy).set(new Uint8Array(data));
  return copy;
}

async function renderPage(page: PDFPageProxy, scale = 2) {
  const viewport = page.getViewport({ scale });
  const canvas = document.createElement("canvas");
  canvas.width = Math.ceil(viewport.width);
  canvas.height = Math.ceil(viewport.height);
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Canvas is unavailable");
  await page.render({ canvasContext: context, viewport, canvas }).promise;
  return { canvas, context, viewport, widthPt: viewport.width / scale, heightPt: viewport.height / scale, scale };
}

function createPdf(widthPt: number, heightPt: number): jsPDF {
  return new jsPDF({ orientation: widthPt > heightPt ? "landscape" : "portrait", unit: "pt", format: [widthPt, heightPt], compress: true });
}

let cjkFontPromise: Promise<string> | undefined;

async function getCjkFont(): Promise<string> {
  cjkFontPromise ||= fetch("/fonts/NotoSansSC-wght.ttf")
    .then((response) => {
      if (!response.ok) throw new Error("无法加载 PDF 中文字体");
      return response.arrayBuffer();
    })
    .then((buffer) => {
      const bytes = new Uint8Array(buffer);
      let binary = "";
      for (let start = 0; start < bytes.length; start += 0x8000) {
        binary += String.fromCharCode(...bytes.subarray(start, start + 0x8000));
      }
      return binary;
    });
  return cjkFontPromise;
}

function installCjkFont(pdf: jsPDF, fontData: string) {
  pdf.addFileToVFS("NotoSansSC.ttf", fontData);
  pdf.addFont("NotoSansSC.ttf", "NotoSansSC", "normal", "Identity-H");
  pdf.addFont("NotoSansSC.ttf", "NotoSansSC", "bold", "Identity-H");
  pdf.setFont("NotoSansSC", "normal");
}

function fitVisibleText(pdf: jsPDF, block: PdfLayoutBlock, bbox: [number, number, number, number]) {
  const [x0, y0, x1, y1] = bbox;
  const width = Math.max(1, x1 - x0), height = Math.max(1, y1 - y0);
  const text = block.text.replace(/\s+/g, " ").trim();
  if (!text) return;
  const heading = /title|heading|header/i.test(block.type || "");
  pdf.setFont("NotoSansSC", heading ? "bold" : "normal");
  pdf.setTextColor(17, 24, 39);
  let fontSize = Math.max(4, Math.min(heading ? 22 : 14, height * 0.72));
  let lines: string[] = [];
  while (fontSize >= 4) {
    pdf.setFontSize(fontSize);
    lines = pdf.splitTextToSize(text, width) as string[];
    if (lines.length * fontSize * 1.12 <= height) break;
    fontSize -= 0.5;
  }
  const visibleLines = lines.slice(0, Math.max(1, Math.floor(height / (fontSize * 1.12))));
  visibleLines.forEach((line, index) => {
    pdf.text(line, x0, y0 + fontSize * (index + 0.9));
  });
}

function addBlankPage(pdf: jsPDF, widthPt: number, heightPt: number, first: boolean) {
  if (!first) pdf.addPage([widthPt, heightPt], widthPt > heightPt ? "landscape" : "portrait");
  pdf.setFillColor(255, 255, 255);
  pdf.rect(0, 0, widthPt, heightPt, "F");
}

function cropPageRegion(canvas: HTMLCanvasElement, bbox: [number, number, number, number], widthPt: number, heightPt: number): string | undefined {
  const scaleX = canvas.width / widthPt, scaleY = canvas.height / heightPt;
  const [x0, y0, x1, y1] = bbox;
  const sx = Math.max(0, Math.floor(x0 * scaleX)), sy = Math.max(0, Math.floor(y0 * scaleY));
  const sw = Math.min(canvas.width - sx, Math.ceil((x1 - x0) * scaleX));
  const sh = Math.min(canvas.height - sy, Math.ceil((y1 - y0) * scaleY));
  if (sw <= 0 || sh <= 0) return undefined;
  const crop = document.createElement("canvas");
  crop.width = sw; crop.height = sh;
  crop.getContext("2d")?.drawImage(canvas, sx, sy, sw, sh, 0, 0, sw, sh);
  const data = crop.toDataURL("image/png");
  crop.width = 0;
  return data;
}

function addPageImage(pdf: jsPDF, image: string, widthPt: number, heightPt: number, first: boolean) {
  if (!first) pdf.addPage([widthPt, heightPt], widthPt > heightPt ? "landscape" : "portrait");
  pdf.addImage(image, "JPEG", 0, 0, widthPt, heightPt, undefined, "FAST");
}

function normalizeBBox(block: PdfLayoutBlock, widthPt: number, heightPt: number): [number, number, number, number] | undefined {
  if (!block.bbox) return undefined;
  const [x0, y0, x1, y1] = block.bbox;
  const sourceWidth = block.pageWidth || Math.max(widthPt, x1);
  const sourceHeight = block.pageHeight || Math.max(heightPt, y1);
  return [x0 / sourceWidth * widthPt, y0 / sourceHeight * heightPt, x1 / sourceWidth * widthPt, y1 / sourceHeight * heightPt];
}

function wrapCanvasText(context: CanvasRenderingContext2D, text: string, maxWidth: number): string[] {
  const tokens = /[\u3400-\u9fff]/.test(text) ? Array.from(text) : text.split(/(\s+)/).filter(Boolean);
  const lines: string[] = [];
  let line = "";
  for (const token of tokens) {
    const next = line + token;
    if (line && context.measureText(next).width > maxWidth) {
      lines.push(line.trimEnd());
      line = token.trimStart();
    } else line = next;
  }
  if (line) lines.push(line.trimEnd());
  return lines;
}

export async function buildSearchablePdf(source: ArrayBuffer, layoutBlocks: PdfLayoutBlock[], onProgress?: (value: PdfRenderProgress) => void): Promise<Blob> {
  const document = await pdfjs.getDocument({ data: copyBuffer(source) }).promise;
  validatePdfLayoutBlocks(layoutBlocks, document.numPages);
  const cjkFont = await getCjkFont();
  let output: jsPDF | undefined;
  try {
    for (let pageNo = 1; pageNo <= document.numPages; pageNo++) {
      const page = await document.getPage(pageNo);
      const viewport = page.getViewport({ scale: 1 });
      const widthPt = viewport.width, heightPt = viewport.height;
      output ||= createPdf(widthPt, heightPt);
      if (pageNo === 1) installCjkFont(output, cjkFont);
      addBlankPage(output, widthPt, heightPt, pageNo === 1);
      const pageBlocks = layoutBlocks.filter((block) => block.page === pageNo)
        .sort((a, b) => (a.bbox?.[1] || 0) - (b.bbox?.[1] || 0) || (a.bbox?.[0] || 0) - (b.bbox?.[0] || 0));
      const rasterBlocks = pageBlocks.filter((block) => isRasterLayoutBlock(block) && block.bbox);
      const rendered = rasterBlocks.length ? await renderPage(page) : undefined;
      for (const block of pageBlocks) {
        const bbox = normalizeBBox(block, widthPt, heightPt);
        if (!bbox) continue;
        if (isRasterLayoutBlock(block) && rendered) {
          const image = cropPageRegion(rendered.canvas, bbox, widthPt, heightPt);
          if (image) output.addImage(image, "PNG", bbox[0], bbox[1], bbox[2] - bbox[0], bbox[3] - bbox[1], undefined, "FAST");
        } else {
          fitVisibleText(output, block, bbox);
        }
      }
      if (rendered) rendered.canvas.width = 0;
      onProgress?.({ page: pageNo, pages: document.numPages, progress: Math.round(pageNo / document.numPages * 100) });
    }
    if (!output) throw new Error("PDF has no pages");
    return output.output("blob");
  } finally { await document.destroy(); }
}

export async function buildTranslatedPdf(source: ArrayBuffer, blocks: PdfLayoutBlock[], translations: Map<string, string>, onProgress?: (value: PdfRenderProgress) => void): Promise<{ blob: Blob; warnings: number }> {
  const document = await pdfjs.getDocument({ data: copyBuffer(source) }).promise;
  let output: jsPDF | undefined;
  let warnings = 0;
  try {
    for (let pageNo = 1; pageNo <= document.numPages; pageNo++) {
      const page = await document.getPage(pageNo);
      const rendered = await renderPage(page);
      for (const block of blocks.filter((item) => item.page === pageNo)) {
        const translated = translations.get(block.id);
        const bboxPt = normalizeBBox(block, rendered.widthPt, rendered.heightPt);
        if (!translated || !bboxPt || /formula|image|code/i.test(block.type || "")) continue;
        const [x0, y0, x1, y1] = bboxPt.map((value) => value * rendered.scale) as [number, number, number, number];
        const width = Math.max(4, x1 - x0), height = Math.max(4, y1 - y0);
        rendered.context.fillStyle = "rgba(255,255,255,0.96)";
        rendered.context.fillRect(x0 - 2, y0 - 2, width + 4, height + 4);
        let fontSize = Math.min(26, Math.max(10, height * 0.32));
        let lines: string[] = [];
        while (fontSize >= 9) {
          rendered.context.font = `${fontSize}px -apple-system, BlinkMacSystemFont, "PingFang SC", "Noto Sans SC", sans-serif`;
          lines = wrapCanvasText(rendered.context, translated, width);
          if (lines.length * fontSize * 1.25 <= height) break;
          fontSize -= 1;
        }
        if (lines.length * fontSize * 1.25 > height) warnings++;
        rendered.context.fillStyle = "#111827";
        rendered.context.textBaseline = "top";
        lines.slice(0, Math.max(1, Math.floor(height / (fontSize * 1.25)))).forEach((line, index) => rendered.context.fillText(line, x0, y0 + index * fontSize * 1.25, width));
      }
      output ||= createPdf(rendered.widthPt, rendered.heightPt);
      addPageImage(output, rendered.canvas.toDataURL("image/jpeg", 0.94), rendered.widthPt, rendered.heightPt, pageNo === 1);
      rendered.canvas.width = 0;
      onProgress?.({ page: pageNo, pages: document.numPages, progress: Math.round(pageNo / document.numPages * 100) });
    }
    if (!output) throw new Error("PDF has no pages");
    output.setProperties({ title: "LazyMind translated PDF", subject: "Translated by LazyMind" });
    return { blob: output.output("blob"), warnings };
  } finally { await document.destroy(); }
}
