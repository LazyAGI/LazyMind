import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { emptyLibrary } from './libraryModel';
import { buildZoteroImport, parseZoteroJSON, matchLocalZoteroFile, previewZotero, type ZoteroPreview } from './zoteroImport';
import { axiosInstance } from '@/components/request';
import { filterZoteroPreview, listZoteroPapers } from './zoteroSelection';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn() } }));
const sample = (): ZoteroPreview => ({
  source_id: 'users:42', warnings: [],
  collections: [{ key: 'ROOT0001', data: { key: 'ROOT0001', name: 'Research', parentCollection: false } }, { key: 'CHILD001', data: { key: 'CHILD001', name: 'Models', parentCollection: 'ROOT0001' } }],
  items: [
    { key: 'PAPER001', data: { key: 'PAPER001', itemType: 'journalArticle', title: 'A paper', creators: [{ firstName: 'Ada', lastName: 'Lovelace', creatorType: 'author' }], DOI: '10.1234/test', publicationTitle: 'Journal', date: '2024-03-02', collections: ['ROOT0001', 'CHILD001'], tags: [{ tag: 'important' }] } },
    { key: 'PDF00001', data: { key: 'PDF00001', itemType: 'attachment', parentItem: 'PAPER001', contentType: 'application/pdf', filename: 'paper.pdf', md5: 'abc' } },
    { key: 'NOTE0001', data: { key: 'NOTE0001', itemType: 'note', parentItem: 'PAPER001', note: '<p>Useful <b>note</b></p><script>alert(1)</script>', dateAdded: '2024-03-03T00:00:00Z' } },
    { key: 'ANNOT001', data: { key: 'ANNOT001', itemType: 'annotation', parentItem: 'PDF00001', annotationText: 'A result', annotationComment: 'Interesting', annotationPosition: '{"pageIndex":2}', annotationColor: '#ffd400' } },
  ],
});

describe('Zotero import graph', () => {
  it.skipIf(!process.env.ZOTERO_REAL_PREVIEW_PATH)('builds the opt-in local database preview with valid paper and material ownership', () => {
    const input = JSON.parse(readFileSync(process.env.ZOTERO_REAL_PREVIEW_PATH!, 'utf8')) as ZoteroPreview;
    const papers = listZoteroPapers(input);
    const plan = buildZoteroImport(filterZoteroPreview(input, papers.map(paper => paper.key)), emptyLibrary());
    expect(plan.counts.papers + plan.counts.existing).toBe(papers.length);
    const paperIds = new Set(plan.library.papers.map(paper => paper.id));
    const attachmentIds = new Set(plan.library.attachments.map(attachment => attachment.id));
    expect(plan.library.notes.every(note => paperIds.has(note.paperId))).toBe(true);
    expect(plan.library.attachments.every(attachment => paperIds.has(attachment.paperId))).toBe(true);
    expect(plan.library.annotations.every(annotation => paperIds.has(annotation.paperId) && attachmentIds.has(annotation.attachmentId!))).toBe(true);
  }, 15000);
  it('preserves detected database libraries for selection and subsequent attachment requests', async () => {
    const database = { path: '/data/Zotero/zotero.sqlite', libraries: [{ type: 'users', id: '0', name: '我的文献库' }], library_type: 'users', library_id: '0' };
    vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { ...sample(), database } });
    const result = await previewZotero('dataset', { source: 'database', library_type: 'users', library_id: '0' });
    expect(result.database).toEqual(database);
  });
  it('preserves nested collections, authors, memberships, notes and PDF annotations without pretending files are stored', () => {
    const plan = buildZoteroImport(sample(), emptyLibrary());
    expect(plan.library.folders).toHaveLength(2);
    expect(plan.library.folders[1].parentId).toBe(plan.library.folders[0].id);
    const paper = plan.library.papers[0];
    expect(paper).toMatchObject({ metadata: { title: 'A paper', authors: ['Ada Lovelace'], year: 2024, identifiers: { doi: '10.1234/test' } } });
    expect(paper.collectionIds).toHaveLength(2);
    expect(plan.library.notes[0].content).toBe('Useful note');
    expect(plan.library.annotations[0]).toMatchObject({ paperId: paper.id, quote: 'A result', comment: 'Interesting', page: 3 });
    expect(plan.library.attachments[0].contentRef).toBeUndefined();
    expect(plan.attachments[0]).toMatchObject({ key: 'PDF00001', paperId: paper.id, name: 'paper.pdf' });
  });

  it('is idempotent and preserves personal edits and uploaded attachment identities', () => {
    const first = buildZoteroImport(sample(), emptyLibrary());
    first.library.papers[0].metadata.title = 'My edited title';
    first.library.papers[0].status = 'read';
    first.library.papers[0].important = true;
    first.library.attachments[0].contentRef = 'stored-document';
    const next = buildZoteroImport(sample(), first.library);
    expect(next.library.papers).toHaveLength(1);
    expect(next.library.papers[0]).toMatchObject({ metadata: { title: 'My edited title' }, status: 'read', important: true });
    expect(next.library.notes).toHaveLength(1);
    expect(next.library.annotations).toHaveLength(1);
    expect(next.attachments).toEqual([]);
    expect(next.counts).toMatchObject({ papers: 0, existing: 1, folders: 0, notes: 0 });
  });

  it('imports selected PDF attachment notes under their paper with stable replay identities', () => {
    const input = sample();
    Object.assign((input.items[1] as { data: Record<string, unknown> }).data, { note: '<p>PDF <b>reading note</b></p><script>hidden()</script>', dateAdded: '2024-04-01T00:00:00Z' });
    input.items.push(
      { key: 'SOLOPDF1', data: { itemType: 'attachment', filename: 'standalone.pdf', note: '<p>Standalone note</p>' } },
      { key: 'PAPER002', data: { itemType: 'book', title: 'Unselected' } },
      { key: 'PDF00002', data: { itemType: 'attachment', parentItem: 'PAPER002', filename: 'other.pdf', note: '<p>Unselected attachment note</p>' } },
      { key: 'EMPTY001', data: { itemType: 'attachment', parentItem: 'PAPER001', filename: 'empty.pdf', note: '  \n ' } },
      { key: 'HTML0001', data: { itemType: 'attachment', parentItem: 'PAPER001', contentType: 'text/html', note: 'Excluded snapshot note' } },
    );
    const selected = filterZoteroPreview(input, ['PAPER001', 'SOLOPDF1']);
    const first = buildZoteroImport(selected, emptyLibrary());
    expect(first.library.notes.map(note => note.content).sort()).toEqual(['PDF reading note', 'Standalone note', 'Useful note']);
    const childNote = first.library.notes.find(note => note.content === 'PDF reading note')!;
    const standaloneNote = first.library.notes.find(note => note.content === 'Standalone note')!;
    const paper = first.library.papers.find(item => item.metadata.title === 'A paper')!;
    const standalone = first.library.papers.find(item => item.metadata.title === 'standalone.pdf')!;
    expect(childNote).toMatchObject({ paperId: paper.id, createdAt: '2024-04-01T00:00:00.000Z' });
    expect(standaloneNote.paperId).toBe(standalone.id);
    expect(paper.noteIds).toContain(childNote.id);
    expect(standalone.noteIds).toEqual([standaloneNote.id]);
    expect(first.counts.notes).toBe(3);
    childNote.content = 'Personal edit';
    const replay = buildZoteroImport(selected, first.library);
    expect(replay.library.notes.map(note => note.id)).toEqual(first.library.notes.map(note => note.id));
    expect(replay.library.notes.find(note => note.id === childNote.id)?.content).toBe('Personal edit');
    expect(replay.counts.notes).toBe(0);
    const otherSource = buildZoteroImport({ ...selected, source_id: 'groups:17' }, emptyLibrary());
    expect(otherSource.library.notes.some(note => note.id === childNote.id)).toBe(false);
  });

  it('matches an existing DOI and augments relationships without overwriting existing organization', () => {
    const first = buildZoteroImport(sample(), emptyLibrary());
    first.library.papers[0].id = 'my-existing-paper';
    first.library.papers[0].zoteroSources = [];
    first.library.papers[0].source = 'doi';
    first.library.papers[0].folderId = null;
    first.library.papers[0].tags = ['mine'];
    first.library.attachments = [];
    first.library.notes = [];
    first.library.annotations = [];
    first.library.papers[0].attachmentIds = [];
    first.library.papers[0].noteIds = [];
    first.library.papers[0].annotationIds = [];
    const next = buildZoteroImport(sample(), first.library);
    expect(next.library.papers).toHaveLength(1);
    expect(next.library.papers[0].id).toBe('my-existing-paper');
    expect(next.library.papers[0].tags).toEqual(['mine', 'important']);
    expect(next.library.papers[0].folderId).toBeNull();
    expect(next.library.attachments[0].paperId).toBe('my-existing-paper');
  });

  it('rejects cycles and missing collection parents before producing a partial graph', () => {
    const cycle = sample();
    (cycle.collections[0] as { data: { parentCollection: unknown } }).data.parentCollection = 'CHILD001';
    expect(() => buildZoteroImport(cycle, emptyLibrary())).toThrow(/循环/);
    const missing = sample();
    (missing.collections[0] as { data: { parentCollection: unknown } }).data.parentCollection = 'MISSING1';
    expect(() => buildZoteroImport(missing, emptyLibrary())).toThrow(/父/);
  });

  it('places imported root collections beneath an existing chosen folder', () => {
    const library = emptyLibrary();
    library.folders.push({ id: 'destination', name: 'Imported' });
    const plan = buildZoteroImport(sample(), library, 'destination');
    expect(plan.library.folders.find(folder => folder.name === 'Research')?.parentId).toBe('destination');
  });

  it('keeps a matched existing folder identity after the user renames or moves it', () => {
    const library = emptyLibrary();
    library.folders.push({ id: 'mine', name: 'Research' }, { id: 'parent', name: 'Personal' });
    const input = sample();
    input.items = input.items.slice(0, 2);
    const first = buildZoteroImport(input, library).library;
    first.folders.find(folder => folder.id === 'mine')!.name = 'Renamed';
    first.folders.find(folder => folder.id === 'mine')!.parentId = 'parent';
    const next = buildZoteroImport(input, first).library;
    expect(next.folders).toHaveLength(3);
    expect(next.folders.find(folder => folder.id === 'mine')).toMatchObject({ name: 'Renamed', parentId: 'parent' });
    expect(next.papers[0].collectionIds).toHaveLength(2);
  });

  it('flattens exported nested items, notes and collections using stable external identities', () => {
    const input = JSON.stringify({ items: [{ key: 'PAPER001', itemType: 'book', title: 'Book', uri: 'http://zotero.org/users/42/items/PAPER001', attachments: [{ key: 'PDF00001', contentType: 'application/pdf', path: 'storage/PDF00001/paper.pdf' }], notes: [{ key: 'NOTE0001', note: '<p>Note</p>' }] }], collections: [{ key: 'ROOT0001', name: 'Root', items: ['PAPER001'], children: [{ key: 'CHILD001', name: 'Child', items: ['PAPER001'] }] }] });
    const parsed = parseZoteroJSON(input);
    const graph = buildZoteroImport(parsed, emptyLibrary());
    expect(graph.library.notes).toHaveLength(1);
    expect(graph.library.attachments).toHaveLength(1);
    expect(graph.library.papers[0].collectionIds).toHaveLength(2);
    expect(parsed.source_id).toBe('users:42');

  });

  it('uses API library identities for stable imports across changing exports', () => {
    expect(parseZoteroJSON(JSON.stringify([{ key: 'PAPER001', library: { type: 'group', id: 17 }, data: { key: 'PAPER001', itemType: 'book', title: 'Book' } }])).source_id).toBe('groups:17');
  });

  it('preserves BetterBibTeX collection maps, numeric item memberships and relative PDF paths', () => {
    const parsed = parseZoteroJSON(JSON.stringify({
      items: [{ itemID: 123, itemType: 'journalArticle', title: 'Exported paper', uri: 'http://zotero.org/users/42/items/PAPER001', attachments: [{ uri: 'http://zotero.org/users/42/items/PDF00001', mimeType: 'application/pdf', path: 'files/456/paper.pdf' }] }],
      collections: { ROOT0001: { key: 'ROOT0001', name: 'Root', parent: '', items: [123] }, CHILD001: { key: 'CHILD001', name: 'Child', parent: 'ROOT0001', items: [123] } },
    }));
    const plan = buildZoteroImport(parsed, emptyLibrary());
    expect(plan.library.folders).toHaveLength(2);
    expect(plan.library.folders[1].parentId).toBe(plan.library.folders[0].id);
    expect(plan.library.papers[0].collectionIds).toHaveLength(2);
    expect(plan.attachments[0]).toMatchObject({ key: 'PDF00001', name: 'paper.pdf', localPath: 'files/456/paper.pdf' });
    const one = new File(['%PDF-one'], 'paper.pdf');
    const two = new File(['%PDF-two'], 'paper.pdf');
    Object.defineProperty(one, 'webkitRelativePath', { value: 'Export/files/456/paper.pdf' });
    Object.defineProperty(two, 'webkitRelativePath', { value: 'Export/files/789/paper.pdf' });
    expect(matchLocalZoteroFile(plan.attachments[0], [one, two])).toBe(one);
  });

  it('matches local storage by attachment key and never chooses an ambiguous filename', () => {
    const one = new File(['%PDF-one'], 'paper.pdf');
    const two = new File(['%PDF-two'], 'paper.pdf');
    Object.defineProperty(one, 'webkitRelativePath', { value: 'storage/PDF00001/paper.pdf' });
    Object.defineProperty(two, 'webkitRelativePath', { value: 'storage/OTHER001/paper.pdf' });
    expect(matchLocalZoteroFile({ key: 'PDF00001', name: 'paper.pdf' }, [one, two])).toBe(one);
    expect(() => matchLocalZoteroFile({ key: 'MISSING1', name: 'paper.pdf' }, [one, two])).toThrow(/多个/);
  });

  it('does not attach a different storage item merely because its filename is unique', () => {
    const wrong = new File(['%PDF-wrong'], 'paper.pdf');
    Object.defineProperty(wrong, 'webkitRelativePath', { value: 'storage/OTHER001/paper.pdf' });
    expect(() => matchLocalZoteroFile({ key: 'PDF00001', name: 'paper.pdf' }, [wrong])).toThrow(/匹配/);
  });

  it('does not treat web snapshots without filenames as PDF files', () => {
    const input = sample();
    input.items = input.items.slice(0, 2);
    input.items.push({ key: 'WEB00001', data: { itemType: 'attachment', parentItem: 'PAPER001', contentType: 'text/html', title: 'Snapshot' } });
    const plan = buildZoteroImport(input, emptyLibrary());
    expect(plan.attachments).toHaveLength(1);
    expect(plan.library.attachments).toHaveLength(1);
  });

  it('surfaces provider errors and malformed successes instead of yielding an empty library', async () => {
    const config = { source: 'remote' as const, library_type: 'users' as const, library_id: '42' };
    vi.mocked(axiosInstance.post).mockRejectedValueOnce({ response: { status: 403, data: { message: 'API key has no library access' } } });
    await expect(previewZotero('dataset', config)).rejects.toThrow('API key has no library access');
    vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { items: [], collections: [] } });
    await expect(previewZotero('dataset', config)).rejects.toThrow(/格式/);
    vi.mocked(axiosInstance.post).mockResolvedValueOnce({ data: { ...sample(), success: false, message: 'Provider rejected request' } });
    await expect(previewZotero('dataset', config)).rejects.toThrow('Provider rejected request');
  });
});
