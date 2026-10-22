import type { ArxivImportResult } from "./arxivImport";
import type { PaperLibraryDataV3, PaperRecord } from "./libraryModel";
import { duplicateOf, normalizeArxiv } from "./model";

// Server documents are authoritative for downloaded attachments; local folders,
// notes and reading preferences remain attached to the same paper identity.
export function mergeArxivImports(current: PaperLibraryDataV3, imports: ArxivImportResult[], folderId: string | null = null, restoreRemoved = false): PaperLibraryDataV3 {
  const papers = [...current.papers];
  let attachments = [...current.attachments];
  const hiddenWorkIds = new Set(current.hiddenArxivWorkIds || []);
  let hiddenPapers = [...(current.hiddenArxivPapers || [])];
  for (const imported of imports) {
    if (hiddenWorkIds.has(imported.work_id) && !restoreRemoved) continue;
    const archived = restoreRemoved ? hiddenPapers.find((item) => item.academicWorkId === imported.work_id) : undefined;
    if (archived && !papers.some((item) => item.id === archived.id)) {
      papers.push({
        ...archived,
        folderId: current.folders.some((folder) => folder.id === archived.folderId) ? archived.folderId : folderId,
        collectionIds: archived.collectionIds.filter((id) => current.collections.some((item) => item.id === id) || current.folders.some((item) => item.id === id)),
      });
    }
    if (restoreRemoved) {
      hiddenWorkIds.delete(imported.work_id);
      hiddenPapers = hiddenPapers.filter((item) => item.academicWorkId !== imported.work_id);
    }
    const { paper: metadata } = imported;
    const incoming: PaperRecord = {
      id: `arxiv-${metadata.base_id}`, title: metadata.title, authors: metadata.authors.join("、"),
      year: metadata.year, venue: metadata.venue || "arXiv", doi: metadata.doi || undefined,
      arxiv: metadata.base_id, arxivVersion: metadata.version, abstract: metadata.abstract,
      academicWorkId: imported.work_id, sourceUrl: metadata.pdf_url, source: "arxiv",
      folderId, status: "unread", important: false, tags: [], parse: "ready",
      metadata: { abstract: metadata.abstract, identifiers: { arxiv: metadata.base_id, doi: metadata.doi || undefined }, confidence: {} },
      attachmentIds: [], noteIds: [], annotationIds: [], collectionIds: [], relatedPaperIds: [],
    };
    const existing = papers.find((item) => item.academicWorkId === imported.work_id)
      || papers.find((item) => normalizeArxiv(item.arxiv || "") === metadata.base_id)
      || duplicateOf(papers.filter((item) => !normalizeArxiv(item.arxiv || "")), incoming) as PaperRecord | undefined;
    const oldVersion = Number(existing?.arxivVersion?.replace(/^v/, "") || 0);
    const incomingVersion = Number(metadata.version.replace(/^v/, ""));
    const currentVersion = !existing || incomingVersion >= oldVersion;
    const newerMetadata = !existing || !existing.academicWorkId || incomingVersion > oldVersion;
    const paper = existing ? {
      ...existing,
      ...(newerMetadata ? { title: incoming.title, authors: incoming.authors, year: incoming.year, venue: incoming.venue, arxivVersion: incoming.arxivVersion, abstract: incoming.abstract, sourceUrl: incoming.sourceUrl, metadata: { ...existing.metadata, abstract: metadata.abstract } } : {}),
      arxiv: metadata.base_id, doi: existing.doi || incoming.doi, academicWorkId: imported.work_id,
    } : incoming;
    paper.metadata = { ...paper.metadata, identifiers: { ...paper.metadata.identifiers, arxiv: metadata.base_id, doi: paper.doi } };
    // A failed older version must not turn a healthy current version into a failure.
    if (currentVersion) {
      paper.parse = imported.processing_status === "failed" ? "failed" : imported.processing_status === "ready" ? "ready" : "parsing";
      paper.processingError = imported.processing_error || undefined;
    }
    const priorAttachment = attachments.find((item) => item.contentRef === imported.document_id);
    const attachmentId = priorAttachment?.id || `arxiv-document-${imported.document_id}`;
    if (currentVersion) attachments = attachments.map((item) => item.paperId === paper.id ? { ...item, isPrimary: false } : item);
    const attachment = {
      ...priorAttachment, id: attachmentId, paperId: paper.id, name: imported.filename,
      kind: "pdf" as const, mimeType: "application/pdf", size: imported.size, source: "arxiv" as const,
      addedAt: priorAttachment?.addedAt || new Date().toISOString(), isPrimary: currentVersion,
      contentRef: imported.document_id, sourceVersion: imported.source_version,
      sourceUrl: imported.source_url, contentHash: imported.content_sha256,
    };
    attachments = [...attachments.filter((item) => item.id !== attachmentId), attachment];
    paper.attachmentIds = [...new Set([...paper.attachmentIds, attachmentId])];
    const index = papers.findIndex((item) => item.id === paper.id);
    if (index >= 0) papers[index] = paper; else papers.unshift(paper);
  }
  return { ...current, papers, attachments, hiddenDocumentIds: restoreRemoved ? current.hiddenDocumentIds?.filter(id => !imports.some(item => item.document_id === id)) : current.hiddenDocumentIds, hiddenArxivWorkIds: [...hiddenWorkIds], hiddenArxivPapers: hiddenPapers };
}
