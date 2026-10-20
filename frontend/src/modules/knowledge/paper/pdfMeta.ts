import { pdfjs } from "react-pdf";
import { identifiersInText, type PaperItem } from "./model";
import { lookupDoi } from "./scholar";

if (!pdfjs.GlobalWorkerOptions.workerSrc) {
  pdfjs.GlobalWorkerOptions.workerSrc = `https://unpkg.com/pdfjs-dist@${pdfjs.version}/build/pdf.worker.min.mjs`;
}

export async function paperFromPdf(file: File): Promise<PaperItem> {
  const data = new Uint8Array(await file.arrayBuffer());
  const document = await pdfjs.getDocument({ data }).promise;
  const metadata = await document.getMetadata();
  const info = metadata.info as { Title?: string; Author?: string };
  const page = await document.getPage(1);
  const content = await page.getTextContent();
  const text = content.items.map((item) => ("str" in item ? item.str : "")).join(" ");
  const found = identifiersInText(`${info.Title || ""} ${text}`);
  const filename = file.name.replace(/\.pdf$/i, "");
  const title = (info.Title || "").trim();
  let paper: PaperItem = {
    id: `pdf-${Date.now()}`,
    title: title || filename,
    authors: (info.Author || "").trim() || "佚名",
    venue: found.arxiv ? "arXiv" : "未标注",
    year: found.year || new Date().getFullYear(),
    folderId: null,
    status: "unread",
    important: false,
    tags: [],
    source: "local",
    parse: "ready",
    doi: found.doi,
    arxiv: found.arxiv,
    incomplete: !title,
  };
  if (found.doi) {
    try {
      const looked = await lookupDoi(found.doi);
      paper = {
        ...paper,
        title: title || looked.title,
        authors: info.Author?.trim() ? paper.authors : looked.authors,
        venue: looked.venue || paper.venue,
        year: looked.year || paper.year,
        doi: looked.doi || paper.doi,
        incomplete: !title && !looked.title,
      };
    } catch {
      paper.incomplete = true;
    }
  }
  return paper;
}
