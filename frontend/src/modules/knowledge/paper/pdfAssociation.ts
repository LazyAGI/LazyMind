import { normalizeArxiv, normalizeDoi, normalizeTitle, type PaperRecord } from './model';
export interface PdfPaper extends PaperRecord {
    identityVerified?: boolean;
    identificationError?: string;
}
export function pdfIdentityMatch(target: PaperRecord, incoming: PdfPaper): 'matched' | 'conflict' | 'uncertain' {
    if (!incoming.identityVerified)
        return 'uncertain';
    const arxiv = incoming.metadata.identifiers.arxiv ? normalizeArxiv(incoming.metadata.identifiers.arxiv) : '';
    const targetArxiv = target.metadata.identifiers.arxiv ? normalizeArxiv(target.metadata.identifiers.arxiv) : '';
    const doi = incoming.metadata.identifiers.doi ? normalizeDoi(incoming.metadata.identifiers.doi) : '';
    const targetDoi = target.metadata.identifiers.doi ? normalizeDoi(target.metadata.identifiers.doi) : '';
    if (arxiv && targetArxiv) {
        if (arxiv === targetArxiv)
            return 'matched';
        if (doi && doi === targetDoi)
            return 'uncertain';
        return 'conflict';
    }
    if (doi && targetDoi)
        return doi === targetDoi ? 'matched' : 'conflict';
    return 'uncertain';
}
export function pdfTargetCandidates(papers: PaperRecord[], incoming: PdfPaper): PaperRecord[] {
    const strong = papers.filter(paper => pdfIdentityMatch(paper, incoming) === 'matched');
    if (strong.length)
        return strong;
    const title = normalizeTitle(incoming.metadata.title);
    return papers.filter(paper => pdfIdentityMatch(paper, incoming) !== 'conflict' && !!title && normalizeTitle(paper.metadata.title) === title);
}
