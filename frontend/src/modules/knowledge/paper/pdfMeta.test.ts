import { beforeEach, describe, expect, it, vi } from "vitest";
import { pdfjs } from "react-pdf";
import { paperFromPdf } from "./pdfMeta";
import { previewPaperLink } from '../api/academicReferences';
import { pdfIdentityMatch } from './pdfAssociation';
import samples from './__tests__/pdfTitleSamples.json';
vi.mock("react-pdf", () => ({ pdfjs: { GlobalWorkerOptions: { workerSrc: "test-worker" }, getDocument: vi.fn() } }));
vi.mock('../api/academicReferences', () => ({ previewPaperLink: vi.fn() }));
const file = { name: "research.pdf", arrayBuffer: async () => new ArrayBuffer(0) } as File;
beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(previewPaperLink).mockRejectedValue(new Error('External lookup unavailable'));
});
// Title/header text and geometry come from the real PDFs in the experiment;
// body paragraphs are replaced with neutral text to keep fixtures small.
function readablePdf(sample: typeof samples[number]) {
    const document = {
        destroy: vi.fn(),
        getMetadata: vi.fn(async () => ({ info: sample.info, metadata: sample.xmpTitle ? { get: () => sample.xmpTitle } : null })),
        getPage: async () => ({ view: sample.view, getTextContent: async () => ({ items: sample.items }) }),
    };
    vi.mocked(pdfjs.getDocument).mockReturnValue({ promise: Promise.resolve(document) } as never);
    return document;
}
describe('PDF title recognition', () => {
    it.each(samples)('extracts the complete $id title when external lookup is unavailable', async (sample) => {
        readablePdf(sample);
        await expect(paperFromPdf(file, 'dataset')).resolves.toMatchObject({
            metadata: {title: sample.expectedTitle}, incomplete: false, identityVerified: false,
        });
    });
    it.each(['PDF metadata', 'verified bibliography'])('preserves long author lists from %s while filtering punctuation', async (source) => {
        const authors = ['DeepSeek-AI', '张三', 'Ke Xu', 'Ke Xu', ...Array.from({ length: 1000 }, (_, i) => `Author ${i}`)];
        const input = [' ', ' : ', '：', '—', '...', ...authors];
        readablePdf({ ...samples[0], info: { ...samples[0].info, Author: input.join(';') } });
        if (source === 'verified bibliography') {
            vi.mocked(previewPaperLink).mockResolvedValue({ paper: { title: samples[0].expectedTitle, authors: input } } as never);
        }
        const paper = await paperFromPdf(file, 'dataset');
        expect(paper.metadata.authors).toEqual(authors);
    });
    it('verifies the arXiv identity after reconstructing small caps and line-end hyphenation', async () => {
        readablePdf(samples[0]);
        const title = 'LoRA: Low-Rank Adaptation of Large Language Models';
        vi.mocked(previewPaperLink).mockResolvedValue({ paper: { title, authors: ['Edward Hu'] } } as never);
        const paper = await paperFromPdf(file, 'dataset');
        expect(paper).toMatchObject({ metadata:{title,authors:['Edward Hu'],identifiers:{arxiv:'2106.09685'}}, arxivVersion: 'v2', incomplete: false, identityVerified: true });
        expect(previewPaperLink).toHaveBeenCalledWith('2106.09685v2', 'dataset');
        expect(pdfIdentityMatch({ ...paper, metadata: {
                ...paper.metadata,
                title: title
            } }, paper)).toBe('matched');
    });
    it('does not verify another paper just because its title appears in the body', async () => {
        const sample = samples[0];
        const otherTitle = 'Attention Is All You Need';
        readablePdf({ ...sample, items: [...sample.items, { str: otherTitle, transform: [10, 0, 0, 10, 108, 130], width: 150 }] });
        vi.mocked(previewPaperLink).mockResolvedValue({ paper: { title: otherTitle } } as never);
        const paper = await paperFromPdf(file, 'dataset');
        expect(paper).toMatchObject({ metadata: {title: sample.expectedTitle}, incomplete: false, identityVerified: false });
        expect(pdfIdentityMatch({ ...paper, metadata: {
                ...paper.metadata,
                title: otherTitle
            } }, paper)).toBe('uncertain');
    });
    it('keeps an unidentifiable PDF incomplete instead of accepting a source-document filename as its title', async () => {
        readablePdf({ ...samples[0], info: { Title: 'Microsoft Word - draft.doc', Author: '' }, items: [] });
        await expect(paperFromPdf(file)).resolves.toMatchObject({metadata:{title:'research'}, incomplete: true, identityVerified: false });
    });
});
describe("PDF import errors", () => {
    it("keeps a locally readable PDF available for manual review when DOI verification fails", async () => {
        const sample = samples.find(sample => sample.id === 'dnn-testing-cn')!;
        const document = readablePdf(sample);
        const failure = new Error("Crossref HTTP 503");
        vi.mocked(previewPaperLink).mockRejectedValue(failure);
        await expect(paperFromPdf(file,'dataset')).resolves.toMatchObject({ metadata: {title: sample.expectedTitle}, incomplete: false, identityVerified: false, identificationError: "Crossref HTTP 503" });
        expect(document.destroy).toHaveBeenCalledOnce();
    });
    it("propagates corrupt PDF errors", async () => {
        const failure = new Error("Invalid PDF structure");
        vi.mocked(pdfjs.getDocument).mockReturnValue({ promise: Promise.reject(failure) } as never);
        await expect(paperFromPdf(file)).rejects.toBe(failure);
    });
});
