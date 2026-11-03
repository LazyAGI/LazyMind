import type { Attachment } from "./libraryModel";
import { safePaperUrl, type PaperRecord, type ReadingStatus } from "./model";
export interface PaperFilters {
    minCitations?: number;
    star: "all" | "starred" | "unstarred";
    pdf: "all" | "has-pdf" | "needs-pdf" | "uploaded";
    folder: "all" | "filed" | "unfiled";
    status: "all" | ReadingStatus;
    yearRange: [
        number,
        number
    ] | null;
}
export const DEFAULT_PAPER_FILTERS: PaperFilters = {
    star: "all", pdf: "all", folder: "all", status: "all", yearRange: null, minCitations: 0,
};
/** Metadata-only migration placeholders do not mean a PDF is available. */
export function isReadablePdfAttachment(attachment: Attachment): boolean {
    if (attachment.available === false)
        return false;
    const isPdf = attachment.kind === "pdf" || attachment.mimeType?.toLowerCase() === "application/pdf";
    if (!isPdf)
        return false;
    if (attachment.contentRef?.trim())
        return true;
    return !!safePaperUrl(attachment.sourceUrl);
}
export function isStoredPdfAttachment(attachment: Attachment): boolean {
    return attachment.kind === "pdf" && isReadablePdfAttachment(attachment) && !!attachment.contentRef?.trim();
}
export function filterPaperRecords<T extends PaperRecord>(papers: T[], attachments: Attachment[], filters: PaperFilters): T[] {
    const withPdf = new Set<string>();
    const uploaded = new Set<string>();
    for (const attachment of attachments) {
        if (!isStoredPdfAttachment(attachment))
            continue;
        withPdf.add(attachment.paperId);
        if (attachment.importMethod === "upload" || (!attachment.importMethod && attachment.source === "local"))
            uploaded.add(attachment.paperId);
    }
    return papers.filter((paper) => {
        if (filters.star === "starred" && !paper.important)
            return false;
        if (filters.star === "unstarred" && paper.important)
            return false;
        if (filters.pdf === "has-pdf" && !withPdf.has(paper.id))
            return false;
        if (filters.pdf === "needs-pdf" && withPdf.has(paper.id))
            return false;
        if (filters.pdf === "uploaded" && !uploaded.has(paper.id))
            return false;
        const filed = !!paper.folderId || !!paper.collectionIds?.length;
        if (filters.folder === "filed" && !filed)
            return false;
        if (filters.folder === "unfiled" && filed)
            return false;
        if (filters.status !== "all" && paper.status !== filters.status)
            return false;
        if (filters.yearRange && (!Number.isFinite(paper.metadata.year) || paper.metadata.year < filters.yearRange[0] || paper.metadata.year > filters.yearRange[1]))
            return false;
        if ((filters.minCitations || 0) > 0 && (!Number.isSafeInteger(paper.metadata.citationCount) || (paper.metadata.citationCount ?? -1) < filters.minCitations!)) return false;
        return true;
    });
}
export function getFilterBounds(papers: PaperRecord[]): {
    maxCitations: number;
    years: [
        number,
        number
    ] | null;
} {
    const years = papers.map((paper) => paper.metadata.year).filter((year) => Number.isSafeInteger(year) && year > 0);
    return {
        maxCitations: Math.max(0, ...papers.map(paper => paper.metadata.citationCount).filter((count): count is number => Number.isSafeInteger(count) && (count ?? -1) >= 0)),
        years: years.length ? [Math.min(...years), Math.max(...years)] : null,
    };
}
export function hasPaperFilters(filters: PaperFilters): boolean {
    return filters.star !== "all" || filters.pdf !== "all" || filters.folder !== "all"
        || filters.status !== "all" || filters.yearRange !== null || (filters.minCitations || 0) > 0;
}
