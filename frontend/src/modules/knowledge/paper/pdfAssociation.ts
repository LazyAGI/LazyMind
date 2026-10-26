import { normalizeArxiv, normalizeDoi, normalizeTitle, type PaperItem } from './model';

export interface PdfPaper extends PaperItem {
  identityVerified?: boolean;
  identificationError?: string;
}

export function pdfIdentityMatch(target: PaperItem, incoming: PdfPaper): 'matched' | 'conflict' | 'uncertain' {
  if (!incoming.identityVerified) return 'uncertain';
  const arxiv = incoming.arxiv ? normalizeArxiv(incoming.arxiv) : '';
  const targetArxiv = target.arxiv ? normalizeArxiv(target.arxiv) : '';
  const doi = incoming.doi ? normalizeDoi(incoming.doi) : '';
  const targetDoi = target.doi ? normalizeDoi(target.doi) : '';
  if (arxiv && targetArxiv) {
    if (arxiv === targetArxiv) return 'matched';
    if (doi && doi === targetDoi) return 'uncertain';
    return 'conflict';
  }
  if (doi && targetDoi) return doi === targetDoi ? 'matched' : 'conflict';
  return 'uncertain';
}

export function pdfTargetCandidates(papers: PaperItem[], incoming: PdfPaper): PaperItem[] {
  const strong = papers.filter(paper => pdfIdentityMatch(paper, incoming) === 'matched');
  if (strong.length) return strong;
  const title = normalizeTitle(incoming.title);
  return papers.filter(paper => pdfIdentityMatch(paper, incoming) !== 'conflict' && !!title && normalizeTitle(paper.title) === title);
}
