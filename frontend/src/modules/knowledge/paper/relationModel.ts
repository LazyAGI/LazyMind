import type { AcademicReference } from '../api/academicReferences';
import type { Expansion, PaperRef } from '../api/paperWorkflows';
import type { PaperLibraryData, PaperRecord } from './libraryModel';
import { identifiersInText, normalizeArxiv, normalizeDoi, paperLink, safePaperUrl } from './model';
export interface RelatedPaper {
    id: string;
    title: string;
    paper?: PaperRecord;
    url?: string;
    metadata?: string;
    source: string;
    detail?: string;
}
export function findLibraryPaper(data: PaperLibraryData, identity: Pick<PaperRef, 'paper_id' | 'document_id' | 'doi' | 'arxiv_id'>): PaperRecord | undefined {
    const doi = normalizeDoi(identity.doi || '');
    const arxiv = normalizeArxiv(identity.arxiv_id || '');
    return (identity.paper_id ? data.papers.find(paper => paper.academicWorkId === identity.paper_id) : undefined)
        || (identity.document_id ? data.papers.find(paper => data.attachments.some(attachment => paper.attachmentIds.includes(attachment.id) && attachment.available !== false && attachment.contentRef === identity.document_id)) : undefined)
        || data.papers.find(paper => (!!doi && normalizeDoi(paper.metadata.identifiers.doi || paper.metadata.identifiers.doi || '') === doi)
            || (!!arxiv && normalizeArxiv(paper.metadata.identifiers.arxiv || paper.metadata.identifiers.arxiv || '') === arxiv));
}
export function relatedPaperItem(paper: PaperRecord, source: string): RelatedPaper {
    return { id: paper.id, title: paper.metadata.title, paper, source, metadata: [paper.metadata.authors, paper.metadata.venue, paper.metadata.year || ''].filter(Boolean).join(' · ') };
}
export function referenceRelations(references: AcademicReference[], data: PaperLibraryData, current: PaperRecord): RelatedPaper[] {
    const items = new Map<string,RelatedPaper>();
    const ordered = [...references].sort((left, right) => (left.ReferenceKey || left.reference_key || '').localeCompare(right.ReferenceKey || right.reference_key || '', undefined, { numeric: true }));
    for (const reference of ordered) {
        const raw = (reference.RawText || reference.raw_text || '').trim();
        const identifiers = identifiersInText(raw);
        const doi = normalizeDoi(reference.DOINormalized || reference.doi_normalized || identifiers.doi || '');
        const arxiv = normalizeArxiv(reference.ArxivIDBase || reference.arxiv_id_base || identifiers.arxiv || '');
        const workId = reference.ResolvedWorkID || reference.resolved_work_id || '';
        const paper = findLibraryPaper(data, { paper_id: workId, doi, arxiv_id: arxiv });
        if (paper?.id === current.id)
            continue;
        const id = paper?.id || workId || doi || arxiv || raw.toLowerCase().replace(/\s+/g, ' ');
        if (!id)
            continue;
        const key = reference.ReferenceKey || reference.reference_key;
        const page = reference.Page ?? reference.page;
        const documentId = reference.SourceDocumentID || reference.source_document_id;
        const attachment = data.attachments.find(item => current.attachmentIds.includes(item.id) && item.contentRef === documentId);
        const source = ['参考文献', key && `[${key.replace(/^\[|\]$/g, '')}]`, current.attachmentIds.length > 1 && attachment && (attachment.sourceVersion || attachment.name), page && page > 0 ? `第 ${page} 页` : ''].filter(Boolean).join(' · ');
        const title = paper?.metadata.title || reference.Title || reference.title || raw || doi || arxiv;
        const authors = reference.AuthorsJSON || reference.authors_json;
        const url = safePaperUrl(doi ? `https://doi.org/${doi}` : arxiv ? `https://arxiv.org/abs/${arxiv}` : paper ? paperLink(paper) || undefined : undefined);
        items.set(id, paper ? { ...relatedPaperItem(paper, source), url, detail: raw && raw !== paper.metadata.title ? raw : undefined } : {
            id, title, source,
            url,
            metadata: title !== raw ? [authors?.join(', '), reference.PublicationYear || reference.publication_year].filter(Boolean).join(' · ') : undefined,
            detail: raw && raw !== title ? raw : undefined,
        });
    }
    return [...items.values()];
}
export function paperExpansions(records: Expansion[], data: PaperLibraryData, paper: PaperRecord) {
    return records.flatMap(record => {
        if (record.version < 1)
            return [];
        const papers = record.result?.papers || [];
        const current = papers.find(item => findLibraryPaper(data, item.paper)?.id === paper.id);
        return current ? [{ record, current, others: papers.filter(item => item !== current) }] : [];
    });
}
