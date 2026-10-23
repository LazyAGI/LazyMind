import type { Attachment } from "./libraryModel";
import type { PaperItem, ReadingStatus } from "./model";

export interface PaperFilters {
  star: "all" | "starred" | "unstarred";
  pdf: "all" | "has-pdf" | "needs-pdf" | "uploaded";
  folder: "all" | "filed" | "unfiled";
  status: "all" | ReadingStatus;
  yearRange: [number, number] | null;
  minCitations: number;
}

export const DEFAULT_PAPER_FILTERS: PaperFilters = {
  star: "all", pdf: "all", folder: "all", status: "all", yearRange: null, minCitations: 0,
};

/** Metadata-only migration placeholders do not mean a PDF is available. */
export function isReadablePdfAttachment(attachment: Attachment): boolean {
  const isPdf = attachment.kind === "pdf" || attachment.mimeType?.toLowerCase() === "application/pdf";
  if (!isPdf) return false;
  if (attachment.contentRef?.trim()) return true;
  try {
    const url = new URL(attachment.sourceUrl || "");
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

export function filterPaperRecords<T extends PaperItem & { collectionIds?: string[] }>(papers: T[], attachments: Attachment[], filters: PaperFilters): T[] {
  const withPdf = new Set<string>();
  const uploaded = new Set<string>();
  for (const attachment of attachments) {
    if (!isReadablePdfAttachment(attachment)) continue;
    withPdf.add(attachment.paperId);
    if (attachment.source === "local") uploaded.add(attachment.paperId);
  }
  return papers.filter((paper) => {
    if (filters.star === "starred" && !paper.important) return false;
    if (filters.star === "unstarred" && paper.important) return false;
    if (filters.pdf === "has-pdf" && !withPdf.has(paper.id)) return false;
    if (filters.pdf === "needs-pdf" && withPdf.has(paper.id)) return false;
    if (filters.pdf === "uploaded" && !uploaded.has(paper.id)) return false;
    const filed = !!paper.folderId || !!paper.collectionIds?.length;
    if (filters.folder === "filed" && !filed) return false;
    if (filters.folder === "unfiled" && filed) return false;
    if (filters.status !== "all" && paper.status !== filters.status) return false;
    if (filters.yearRange && (!Number.isFinite(paper.year) || paper.year < filters.yearRange[0] || paper.year > filters.yearRange[1])) return false;
    if (filters.minCitations > 0 && (!Number.isSafeInteger(paper.citationCount) || (paper.citationCount ?? -1) < filters.minCitations)) return false;
    return true;
  });
}

export function getFilterBounds(papers: PaperItem[]): { years: [number, number] | null; maxCitations: number } {
  const years = papers.map((paper) => paper.year).filter((year) => Number.isSafeInteger(year) && year > 0);
  const counts = papers.map((paper) => paper.citationCount).filter((count): count is number => Number.isSafeInteger(count) && (count ?? -1) >= 0);
  return {
    years: years.length ? [Math.min(...years), Math.max(...years)] : null,
    maxCitations: Math.max(0, ...counts),
  };
}

export function hasPaperFilters(filters: PaperFilters): boolean {
  return filters.star !== "all" || filters.pdf !== "all" || filters.folder !== "all"
    || filters.status !== "all" || filters.yearRange !== null || filters.minCitations > 0;
}
