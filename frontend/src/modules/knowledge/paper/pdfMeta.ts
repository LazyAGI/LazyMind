import { v4 as uuidv4 } from 'uuid';
import { pdfjs } from 'react-pdf';
import { createPaperRecord, identifiersInText, parseAuthorInput } from './model';
import { extractPdfTitle, pdfTitlesMatch } from './pdfTitle';
import { previewPaperLink } from '../api/academicReferences';
import type { PdfPaper } from './pdfAssociation';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
if (!pdfjs.GlobalWorkerOptions.workerSrc)
    pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;
export async function paperFromPdf(file: File, datasetId?: string): Promise<PdfPaper> {
    const document = await pdfjs.getDocument({ data: new Uint8Array(await file.arrayBuffer()) }).promise;
    try {
        const raw = await document.getMetadata().catch(() => ({ info: {}, metadata: null }));
        const info = raw.info as {
            Title?: string;
            Author?: string;
        };
        const page = await document.getPage(1), content = await page.getTextContent().catch(() => ({ items: [] }));
        const text = content.items.map(item => 'str' in item ? item.str : '').join(' ');
        const found = identifiersInText(`${info.Title || ''} ${text}`);
        const title = extractPdfTitle(content.items, page.view, [info.Title, raw.metadata?.get('dc:title')]);
        let paper: PdfPaper = { ...createPaperRecord({ title: title || file.name.replace(/\.pdf$/i, ''), authors: parseAuthorInput(info.Author || ''), venue: found.arxiv ? 'arXiv' : '', year: found.year || 0, identifiers: { doi: found.doi, arxiv: found.arxiv } }, { id: uuidv4(), source: 'local', importMethod: 'upload', arxivVersion: found.arxivVersion, incomplete: !title }), identityVerified: false };
        const identifier = found.arxiv ? `${found.arxiv}${found.arxivVersion || ''}` : found.doi;
        if (!datasetId || !identifier)
            return paper;
        try {
            const preview = await previewPaperLink(identifier, datasetId), ref = preview.paper;
            const lookedTitle = ref?.title || preview.title;
            if (lookedTitle && pdfTitlesMatch(title, lookedTitle)) {
                paper = { ...paper, identityVerified: true, incomplete: false, metadata: { ...paper.metadata, title: lookedTitle, authors: ref?.authors?.length ? ref.authors : paper.metadata.authors, venue: ref?.venue || paper.metadata.venue, year: ref?.year || paper.metadata.year, abstract: ref?.abstract || undefined, citationCount: ref?.citation_count, identifiers: { doi: ref?.doi || preview.doi || found.doi, arxiv: ref?.arxiv_id || preview.arxiv_id || found.arxiv } } };
            }
        }
        catch (error) {
            paper.identificationError = error instanceof Error ? error.message : '无法验证题录，请人工核对';
        }
        return paper;
    }
    finally {
        await document.destroy();
    }
}
