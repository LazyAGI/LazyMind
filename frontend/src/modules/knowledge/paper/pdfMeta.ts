import { pdfjs } from "react-pdf";
import { identifiersInText, normalizeTitle } from "./model";
import { lookupDoi } from "./scholar";
import { previewPaperLink } from '../api/academicReferences';
import type { PdfPaper } from './pdfAssociation';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';

if (!pdfjs.GlobalWorkerOptions.workerSrc) {
  pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;
}

export async function paperFromPdf(file: File, datasetId?: string): Promise<PdfPaper> {
  const data = new Uint8Array(await file.arrayBuffer());
  const document = await pdfjs.getDocument({ data }).promise;
  try {
    const metadata = await document.getMetadata().catch(() => ({ info: {} }));
    const info = metadata.info as { Title?: string; Author?: string };
    const page = await document.getPage(1);
    const content = await page.getTextContent().catch(() => ({ items: [] }));
    const text = content.items.map((item) => ("str" in item ? item.str : "")).join(" ");
    const frontMatter = text.split(/\b(?:abstract|introduction|references)\b/i)[0];
    const found = identifiersInText(`${info.Title || ""} ${text}`);
    const filename = file.name.replace(/\.pdf$/i, "");
    const title = (info.Title || "").trim();
    let paper: PdfPaper = {
      id: `pdf-${Date.now()}`,
      title: title || filename,
      authors: (info.Author || "").trim() || "佚名",
      venue: found.arxiv ? "arXiv" : "未标注",
      year: found.year || 0,
      folderId: null,
      status: "unread",
      important: false,
      tags: [],
      source: "local",
      importMethod: "upload",
      parse: "ready",
      doi: found.doi,
      arxiv: found.arxiv,
      arxivVersion: found.arxivVersion,
      incomplete: !title,
      identityVerified: false,
    };
    try {
      const arxiv = found.arxiv && datasetId ? await previewPaperLink(`${found.arxiv}${found.arxivVersion || ''}`, datasetId) : undefined;
      const looked = arxiv?.paper ? { title: arxiv.paper.title || arxiv.title, authors: arxiv.paper.authors?.join('、'), venue: arxiv.paper.venue, year: arxiv.paper.year, doi: arxiv.paper.doi } : found.doi ? await lookupDoi(found.doi) : undefined;
      if (!looked) return paper;
      const normalized = normalizeTitle(frontMatter);
      const verified = !!looked.title && (normalizeTitle(title) === normalizeTitle(looked.title) || normalized.includes(normalizeTitle(looked.title)));
      paper = {
        ...paper,
        ...(verified ? { title: looked.title, authors: looked.authors || paper.authors, venue: looked.venue || paper.venue, year: looked.year || paper.year, doi: looked.doi || paper.doi, incomplete: false } : {}),
        identityVerified: verified,
      };
    } catch (error) {
      paper.identificationError = error instanceof Error ? error.message : '无法验证题录，请人工核对';
    }
    return paper;
  } finally {
    await document.destroy();
  }
}
