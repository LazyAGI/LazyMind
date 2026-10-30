import { describe, expect, it } from 'vitest';
import { paperFixture, libraryFixture } from './__tests__/fixtures';
import { applyPaperEdits, assignFolders, classifyIdentifier, duplicateOf, filterPapers, formatCitation, identifiersInText, parseBibTeX, parseCSV, parseRIS, toBibTeX, toCSV, toRIS, safePaperUrl } from './model';
describe('paper bibliography and identity', () => {
    const authors = ['Alice Example', 'Bob Example', 'Chen, Dana', 'Eve Example'];
    const items = [paperFixture({ metadata: { title: 'A study', authors, year: 2024, identifiers: { doi: '10.1234/study' } } })];
    it('preserves all four authors across BibTeX, RIS and CSV', () => {
        expect(toBibTeX(items)).toContain('Alice Example and Bob Example and Chen, Dana and Eve Example');
        expect(toRIS(items).match(/^AU  - /gm)).toHaveLength(4);
        for (const papers of [parseBibTeX(toBibTeX(items), 'local'), parseRIS(toRIS(items), 'local'), parseCSV(toCSV(items, [])).papers]) {
            expect(papers[0].metadata).toMatchObject({ title: 'A study', authors, identifiers: { doi: '10.1234/study' }, year: 2024 });
        }
    });
    it('uses strong identity for duplicate detection and does not merge titles alone', () => {
        expect(duplicateOf(items, paperFixture({ id: 'copy', metadata: { identifiers: { doi: 'https://doi.org/10.1234/STUDY' } } }))?.id).toBe('paper');
        expect(duplicateOf(items, paperFixture({ id: 'different', metadata: { title: 'A study', year: 2024 } }))).toBeUndefined();
        expect(duplicateOf(items, paperFixture({ metadata: { identifiers: { doi: '10.1234/other' } } }))).toBeUndefined();
    });
    it('keeps structured authors until the edit form boundary', () => {
        const edited = applyPaperEdits(items[0], { title: 'Corrected', authors: [' : ', '：', '...', ...authors].join('；'), venue: 'Journal', year: 2025, doi: 'https://doi.org/10.1234/corrected', arxiv: '', tags: ['tag', ' '] });
        expect(edited.metadata).toMatchObject({ title: 'Corrected', authors, identifiers: { doi: '10.1234/corrected' } });
        expect(edited.tags).toEqual(['tag']);
    });
    it('searches authors, venue and tags without changing data', () => {
        const papers = [...items, paperFixture({ id: 'other', tags: ['topic'] })];
        expect(filterPapers(papers, 'alice').map(p => p.id)).toEqual(['paper']);
        expect(filterPapers(papers, 'topic').map(p => p.id)).toEqual(['other']);
    });
    it('keeps CSV organization and exports human-readable citations', () => {
        const parsed = parseCSV('题名,作者,出处,年份,DOI,arXiv,标签,文件夹\nNew paper,Alice,Journal,2024,,,RAG,Topic\n');
        const placed = assignFolders(libraryFixture({ folders: [{ id: 'topic', name: 'Topic' }] }), parsed.papers, parsed.folderNames, null);
        expect(placed.papers[0].folderId).toBe('topic');
        expect(formatCitation(items[0], 'apa')).toContain('Alice Example');
    });
    it('normalizes identifiers and rejects unsafe material links', () => {
        expect(classifyIdentifier('https://arxiv.org/pdf/1706.03762v3.pdf')).toEqual({ kind: 'arxiv', value: '1706.03762' });
        expect(identifiersInText('10.48550/arXiv.1706.03762')).toMatchObject({ doi: '10.48550/arxiv.1706.03762', arxiv: '1706.03762' });
        expect(safePaperUrl('javascript:alert(1)')).toBeUndefined();
        expect(safePaperUrl('https://user:pass@example.com')).toBeUndefined();
    });
    it.each(['https://publisher.example/paper', 'javascript:alert(1)', 'https://user:pass@example.com'])('retains only safe bibliography source URLs: %s', url => {
        const papers = [
            parseBibTeX(`@article{a, title={A study}, url={${url}}}`, 'local')[0],
            parseRIS(`TY  - JOUR\nTI  - A study\nUR  - ${url}\nER  -`, 'local')[0],
            parseCSV(`title,url\nA study,${url}`).papers[0],
        ];
        for (const paper of papers) {
            expect(paper.sourceUrl).toBe(url.startsWith('https://publisher.') ? url : undefined);
            expect(paper.source).toBe('local');
        }
    });
});
