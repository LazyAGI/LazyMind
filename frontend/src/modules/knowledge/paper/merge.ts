import { normalizeArxiv, normalizeDoi, normalizeTitle } from './model';
import type { PaperLibraryDataV3, PaperRecord } from './libraryModel';

export interface DuplicateCandidate { paper: PaperRecord; score: number; reasons: string[] }

export function duplicateCandidates(existing: PaperRecord[], incoming: PaperRecord): DuplicateCandidate[] {
  return existing.filter((item) => item.id !== incoming.id).map((paper) => {
    const reasons: string[] = [];
    if (incoming.doi && paper.doi && normalizeDoi(incoming.doi) === normalizeDoi(paper.doi)) reasons.push('DOI 相同');
    if (incoming.arxiv && paper.arxiv && normalizeArxiv(incoming.arxiv) === normalizeArxiv(paper.arxiv)) reasons.push('arXiv 相同');
    if (normalizeTitle(incoming.title) === normalizeTitle(paper.title)) reasons.push('题名相同');
    if (incoming.year && paper.year === incoming.year && incoming.authors && paper.authors && normalizeTitle(incoming.authors) === normalizeTitle(paper.authors)) reasons.push('作者与年份相同');
    const score = reasons.reduce((total, reason) => total + (reason.includes('DOI') || reason.includes('arXiv') ? 1 : 0.45), 0);
    return { paper, score, reasons };
  }).filter((item) => item.score >= 0.45).sort((a, b) => b.score - a.score);
}

export function mergePapers(data: PaperLibraryDataV3, primaryId: string, duplicateId: string): PaperLibraryDataV3 {
  const primary = data.papers.find((item) => item.id === primaryId);
  const duplicate = data.papers.find((item) => item.id === duplicateId);
  if (!primary || !duplicate || primaryId === duplicateId) return data;
  const snapshot = { id: `merge-${Date.now()}`, createdAt: new Date().toISOString(), primaryId, duplicateId, before: data };
  const merged: PaperRecord = { ...primary, title: primary.title || duplicate.title, authors: primary.authors || duplicate.authors, venue: primary.venue || duplicate.venue, year: primary.year || duplicate.year, doi: primary.doi || duplicate.doi, arxiv: primary.arxiv || duplicate.arxiv, tags: [...new Set([...primary.tags, ...duplicate.tags])], attachmentIds: [...new Set([...primary.attachmentIds, ...duplicate.attachmentIds])], noteIds: [...new Set([...primary.noteIds, ...duplicate.noteIds])], annotationIds: [...new Set([...primary.annotationIds, ...duplicate.annotationIds])], collectionIds: [...new Set([...primary.collectionIds, ...duplicate.collectionIds])], relatedPaperIds: [...new Set([...primary.relatedPaperIds, ...duplicate.relatedPaperIds])], metadata: { ...duplicate.metadata, ...primary.metadata, identifiers: { ...duplicate.metadata.identifiers, ...primary.metadata.identifiers }, confidence: { ...duplicate.metadata.confidence, ...primary.metadata.confidence } } };
  return { ...data, papers: data.papers.filter((item) => item.id !== duplicateId).map((item) => item.id === primaryId ? merged : item), attachments: data.attachments.map((item) => item.paperId === duplicateId ? { ...item, paperId: primaryId } : item), notes: data.notes.map((item) => item.paperId === duplicateId ? { ...item, paperId: primaryId } : item), annotations: data.annotations.map((item) => item.paperId === duplicateId ? { ...item, paperId: primaryId } : item), relations: data.relations.map((item) => ({ ...item, fromPaperId: item.fromPaperId === duplicateId ? primaryId : item.fromPaperId, toPaperId: item.toPaperId === duplicateId ? primaryId : item.toPaperId })).filter((item) => item.fromPaperId !== item.toPaperId), mergeSnapshots: [...data.mergeSnapshots, snapshot] };
}

export function undoMerge(data: PaperLibraryDataV3): PaperLibraryDataV3 {
  const snapshot = data.mergeSnapshots[data.mergeSnapshots.length - 1];
  return snapshot ? snapshot.before : data;
}
